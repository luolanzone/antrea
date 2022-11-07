// Copyright 2022 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package multicluster

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"strings"
	"sync"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"
	corelisters "k8s.io/client-go/listers/core/v1"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	k8smcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	k8smcsinformer "sigs.k8s.io/mcs-api/pkg/client/informers/externalversions/apis/v1alpha1"

	mcclientset "antrea.io/antrea/multicluster/pkg/client/clientset/versioned"
	"antrea.io/antrea/pkg/agent/config"
	"antrea.io/antrea/pkg/agent/interfacestore"
	"antrea.io/antrea/pkg/agent/openflow"
	"antrea.io/antrea/pkg/ovs/ovsconfig"
)

const (
	emptyLabels = "<none>"
)

type endpointChangeInfo struct {
	trigger           endpointChangeEvent
	labels            string
	podIP             string
	podNodeIP         string
	svcNamespacedName types.NamespacedName
}

type endpointChangeEvent string

const (
	svcExportDelete endpointChangeEvent = "svcExportDelete"
	svcAdd          endpointChangeEvent = "svcAdd"
	svcUpdate       endpointChangeEvent = "svcUpdate"
	svcDelete       endpointChangeEvent = "svcDelete"
	podAdd          endpointChangeEvent = "podAdd"
	podDelete       endpointChangeEvent = "podDelete"
)

// MCWithPolicyOnlyNodeRouteController generates necessary L3 forwarding rules
// for any exported multi-cluster Service's Pod to forward cross-cluster
// traffic between Nodes inside a member cluster when agent
// is enabled with networkPolicyOnly mode.
type MCWithPolicyOnlyNodeRouteController struct {
	mcClient               mcclientset.Interface
	ovsBridgeClient        ovsconfig.OVSBridgeClient
	ofClient               openflow.Client
	interfaceStore         interfacestore.InterfaceStore
	nodeConfig             *config.NodeConfig
	queue                  workqueue.RateLimitingInterface
	svcInformer            cache.SharedIndexInformer
	svcLister              corelisters.ServiceLister
	podInformer            cache.SharedIndexInformer
	podLister              corelisters.PodLister
	svcExportInformer      k8smcsinformer.ServiceExportInformer
	mutex                  sync.RWMutex
	selectorToService      map[string]types.NamespacedName
	installedServiceExport map[types.NamespacedName]struct{}
	isNetworkPolicyOnly    bool
}

func NewMCWithPolicyOnlyNodeRouteController(
	mcClient mcclientset.Interface,
	svcInformer cache.SharedIndexInformer,
	podInformer cache.SharedIndexInformer,
	svcExportInformer k8smcsinformer.ServiceExportInformer,
	client openflow.Client,
	ovsBridgeClient ovsconfig.OVSBridgeClient,
	interfaceStore interfacestore.InterfaceStore,
	nodeConfig *config.NodeConfig,
) *MCWithPolicyOnlyNodeRouteController {
	controller := &MCWithPolicyOnlyNodeRouteController{
		mcClient:               mcClient,
		ovsBridgeClient:        ovsBridgeClient,
		ofClient:               client,
		interfaceStore:         interfaceStore,
		nodeConfig:             nodeConfig,
		queue:                  workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(minRetryDelay, maxRetryDelay), "MCWithPolicyOnlyNodeRouteController"),
		svcInformer:            svcInformer,
		svcLister:              corelisters.NewServiceLister(svcInformer.GetIndexer()),
		podInformer:            podInformer,
		podLister:              corelisters.NewPodLister(podInformer.GetIndexer()),
		svcExportInformer:      svcExportInformer,
		selectorToService:      make(map[string]types.NamespacedName),
		installedServiceExport: make(map[types.NamespacedName]struct{}),
	}
	controller.svcExportInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(cur interface{}) {
				controller.enqueueServiceExport(cur, false)
			},
			// We only care about ServiceExport's Namespace and Name which are immutable,
			// so we can ignore update events.
			DeleteFunc: func(old interface{}) {
				controller.enqueueServiceExport(old, true)
			},
		},
		resyncPeriod,
	)
	controller.svcInformer.AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(cur interface{}) {
				controller.enqueueService(cur, false)
			},
			UpdateFunc: controller.enqueueServiceUpdate,
			DeleteFunc: func(old interface{}) {
				controller.enqueueService(old, true)
			},
		},
		resyncPeriod,
	)
	controller.podInformer.AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(cur interface{}) {
				controller.enqueuePod(cur, false)
			},
			UpdateFunc: controller.enqueuePodUpdate,
			DeleteFunc: func(old interface{}) {
				controller.enqueuePod(old, true)
			},
		},
		resyncPeriod,
	)
	return controller
}

func (c *MCWithPolicyOnlyNodeRouteController) initialize() {
	svcExports, err := c.svcExportInformer.Lister().List(labels.Everything())
	if err == nil {
		for _, svcExport := range svcExports {
			svcNamespacedName := types.NamespacedName{Namespace: svcExport.Namespace, Name: svcExport.Name}
			c.installedServiceExport[svcNamespacedName] = struct{}{}
			svc, err := c.svcLister.Services(svcExport.Namespace).Get(svcExport.Name)
			if err != nil || svc.Spec.Selector == nil {
				continue
			}
			c.selectorToService[labels.FormatLabels(svc.Spec.Selector)] = svcNamespacedName
		}
		klog.InfoS("initialized done", "selectorToService", c.selectorToService)
	}
}

func (c *MCWithPolicyOnlyNodeRouteController) enqueueServiceExport(obj interface{}, isDelete bool) {
	svcExport, isSvcExport := obj.(*k8smcsv1alpha1.ServiceExport)
	if !isSvcExport {
		klog.ErrorS(errors.New("received unexpected object"), "enqueueServiceExport can't process event", "obj", obj)
		return
	}

	klog.InfoS("enqueueServiceExport", svcExport.Namespace+"/"+svcExport.Name)

	svcExportNamespacedName := types.NamespacedName{Namespace: svcExport.Namespace, Name: svcExport.Name}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if isDelete {
		delete(c.installedServiceExport, svcExportNamespacedName)
		c.queue.Add(&endpointChangeInfo{
			trigger:           svcExportDelete,
			svcNamespacedName: svcExportNamespacedName,
		})
		return
	}
	c.installedServiceExport[svcExportNamespacedName] = struct{}{}
}

func (c *MCWithPolicyOnlyNodeRouteController) enqueueServiceUpdate(old, cur interface{}) {
	oldSvc, oldIsSvc := old.(*corev1.Service)
	curSvc, curIsSvc := cur.(*corev1.Service)
	if !oldIsSvc || !curIsSvc {
		klog.ErrorS(errors.New("received unexpected object"), "enqueueServiceUpdate can't process event", "obj", cur)
		return
	}
	klog.InfoS("enqueueServiceUpdate", curSvc.Namespace+"/"+curSvc.Name)

	if reflect.DeepEqual(oldSvc.Spec.Selector, curSvc.Spec.Selector) {
		// The Endpoints selected by the Service should be no difference
		// when the selector are not changed.
		klog.InfoS("Service's selector has no change, skip it")
		return
	}

	svcNamespacedName := types.NamespacedName{Namespace: curSvc.Namespace, Name: curSvc.Name}
	c.mutex.Lock()
	defer c.mutex.Unlock()
	if _, ok := c.installedServiceExport[svcNamespacedName]; !ok {
		return
	}

	oldSvcSelector := labels.FormatLabels(oldSvc.Spec.Selector)
	curSvcSelector := labels.FormatLabels(curSvc.Spec.Selector)
	delete(c.selectorToService, oldSvcSelector)
	c.selectorToService[curSvcSelector] = svcNamespacedName
	c.queue.Add(&endpointChangeInfo{
		trigger:           svcUpdate,
		svcNamespacedName: svcNamespacedName,
	})
}

func (c *MCWithPolicyOnlyNodeRouteController) enqueueService(obj interface{}, isDelete bool) {
	svc, isSvc := obj.(*corev1.Service)
	if !isSvc {
		klog.ErrorS(errors.New("received unexpected object"), "enqueueService can't process event", "obj", obj)
		return
	}
	klog.InfoS("enqueueService", svc.Namespace+"/"+svc.Name)

	isMCService := strings.HasPrefix(svc.Name, "antrea-mc-")

	if isMCService || svc.Spec.Selector == nil {
		klog.InfoS("The Service is auto-created multi-cluster Service or has no selector, skip it")
		return
	}

	svcNamespacedName := types.NamespacedName{Namespace: svc.Namespace, Name: svc.Name}

	c.mutex.Lock()
	defer c.mutex.Unlock()
	if _, ok := c.installedServiceExport[svcNamespacedName]; !ok {
		// Return immediately since it's not an exported Service.
		return
	}

	selector := labels.FormatLabels(svc.Spec.Selector)
	if isDelete {
		if _, ok := c.selectorToService[selector]; ok {
			delete(c.selectorToService, selector)
			c.queue.Add(&endpointChangeInfo{
				trigger:           svcDelete,
				svcNamespacedName: svcNamespacedName,
			})
		}
		return
	}

	c.selectorToService[selector] = svcNamespacedName
	c.queue.Add(&endpointChangeInfo{
		trigger:           svcAdd,
		labels:            selector,
		svcNamespacedName: svcNamespacedName,
	})
}

func (c *MCWithPolicyOnlyNodeRouteController) matchedService(podLabels map[string]string) (bool, types.NamespacedName) {
	podLabelSet := labels.Set(podLabels)

	c.mutex.RLock()
	defer c.mutex.RUnlock()
	for l, svc := range c.selectorToService {
		selector, _ := labels.Parse(l)
		if selector.Matches(podLabelSet) {
			return true, svc
		}
	}
	return false, types.NamespacedName{}
}

func (c *MCWithPolicyOnlyNodeRouteController) enqueuePod(obj interface{}, isDelete bool) {
	pod, isPod := obj.(*corev1.Pod)
	if !isPod {
		klog.ErrorS(errors.New("received unexpected object"), "enqueuePod can't process event", "obj", obj)
		return
	}

	klog.InfoS("enqueuePod", pod.Namespace+"/"+pod.Name)
	matched, svc := c.matchedService(pod.Labels)

	if !matched {
		klog.InfoS("Pod doesn't match any Service", "pod", klog.KObj(pod))
		return
	}

	if isDelete {
		c.queue.Add(&endpointChangeInfo{
			trigger:   podDelete,
			podNodeIP: pod.Status.HostIP,
			podIP:     pod.Status.PodIP,
		})
		return
	}

	if pod.Status.HostIP != "" && pod.Status.PodIP != "" && pod.Spec.NodeName != c.nodeConfig.Name {
		c.queue.Add(&endpointChangeInfo{
			svcNamespacedName: svc,
			podNodeIP:         pod.Status.HostIP,
			podIP:             pod.Status.PodIP,
		})
	}
}

func (c *MCWithPolicyOnlyNodeRouteController) enqueuePodUpdate(old, cur interface{}) {
	oldPod, oldIsPod := old.(*corev1.Pod)
	curPod, curIsPod := cur.(*corev1.Pod)
	if !oldIsPod || !curIsPod {
		klog.ErrorS(errors.New("received unexpected object"), "enqueuePodUpdate can't process event", "obj", cur)
		return
	}

	klog.InfoS("enqueuePodUpdate", curPod.Namespace+"/"+curPod.Name)

	if curPod.Spec.HostNetwork || curPod.Spec.NodeName == c.nodeConfig.Name {
		return
	}

	oldMatched, oldMatchedSvc := c.matchedService(oldPod.Labels)
	curMatched, curMatchedSvc := c.matchedService(curPod.Labels)

	if !oldMatched && !curMatched {
		klog.InfoS("Pod doesn't match any Service", "pod", klog.KObj(curPod))
		// The Pod doesn't run workload for any exported Service or
		// it has no labels.
		return
	}

	if curMatched && oldMatched && oldPod.Status.PodIP == curPod.Status.PodIP &&
		oldPod.Status.HostIP == curPod.Status.HostIP {
		klog.V(2).InfoS("Received a Pod update event, "+
			"but no change impact rules. Skip it", "name", curPod.Name, "namespace", curPod.Namespace)
		return
	}

	if oldMatched {
		c.queue.Add(&endpointChangeInfo{
			trigger:           podDelete,
			svcNamespacedName: oldMatchedSvc,
			podNodeIP:         oldPod.Status.HostIP,
			podIP:             oldPod.Status.PodIP,
		})
	}

	if curMatched && curPod.Status.PodIP != "" && curPod.Status.HostIP != "" {
		c.queue.Add(&endpointChangeInfo{
			svcNamespacedName: curMatchedSvc,
			podNodeIP:         curPod.Status.HostIP,
			podIP:             curPod.Status.PodIP,
		})
	}
}

// Run will create defaultWorkers workers (go routines) which will process
// the exported Service's Endpoints change events from the workqueue.
func (c *MCWithPolicyOnlyNodeRouteController) Run(stopCh <-chan struct{}) {
	controllerName := "AntreaAgentMCWithPolicyOnlyNodeRouteController"
	defer c.queue.ShutDown()

	cacheSyncs := []cache.InformerSynced{c.svcInformer.HasSynced, c.podInformer.HasSynced,
		c.svcExportInformer.Informer().HasSynced}
	klog.InfoS("Starting controller", "controller", controllerName)
	defer klog.InfoS("Shutting down controller", "controller", controllerName)
	if !cache.WaitForNamedCacheSync(controllerName, stopCh, cacheSyncs...) {
		return
	}

	c.initialize()

	for i := 0; i < defaultWorkers; i++ {
		go wait.Until(c.worker, time.Second, stopCh)
	}
	<-stopCh
}

func (c *MCWithPolicyOnlyNodeRouteController) worker() {
	for c.processNextWorkItem() {
	}
}

func (c *MCWithPolicyOnlyNodeRouteController) processNextWorkItem() bool {
	obj, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(obj)

	if k, ok := obj.(*endpointChangeInfo); !ok {
		c.queue.Forget(obj)
		klog.InfoS("Expected endpointChangeInfo in work queue but got", "object", obj)
		return true
	} else if err := c.syncPodFlows(k); err == nil {
		c.queue.Forget(k)
	} else {
		// Put the item back on the workqueue to handle any transient errors.
		c.queue.AddRateLimited(k)
		klog.ErrorS(err, "Error syncing key, requeuing", "key", k)
	}
	return true
}

func (c *MCWithPolicyOnlyNodeRouteController) syncPodFlows(key *endpointChangeInfo) error {
	switch key.trigger {
	case podAdd:
		klog.InfoS("handling podAdd", "podIP", key.podIP)
		if err := c.ofClient.InstallMulticlusterLocalForwardingFlows(key.svcNamespacedName.String(), net.ParseIP(key.podIP), net.ParseIP(key.podNodeIP)); err != nil {
			klog.ErrorS(err, "Failed to install local Pod forwarding flows", "podIP", key.podIP)
			return err
		}
	case podDelete:
		klog.InfoS("handling podDelete", "podIP", key.podIP)
		if err := c.ofClient.UninstallMulticlusterSingleFlow(net.ParseIP(key.podIP), net.ParseIP(key.podNodeIP)); err != nil {
			klog.ErrorS(err, "Failed to uninstall local Pod forwarding flows", "podIP", key.podIP)
			return err
		}
	case svcExportDelete, svcDelete:
		klog.InfoS("handling svcExportDelete/svcDelete", "service", key.svcNamespacedName.String())
		svcName := key.svcNamespacedName.String()
		if err := c.ofClient.UninstallMulticlusterFlows(fmt.Sprintf("service_%s", svcName)); err != nil {
			klog.ErrorS(err, "Failed to uninstall multi-cluster flows with given Service", "service", svcName)
			return err
		}
	case svcAdd:
		klog.InfoS("handling svcAdd", "service", key.svcNamespacedName.String())
		err := c.addPodFlows(key.labels, key)
		if err != nil {
			klog.ErrorS(err, "Failed to install local Pod forwarding flows for Service", "service", key.svcNamespacedName.String())
			return err
		}
	}
	return nil
}

func (c *MCWithPolicyOnlyNodeRouteController) addPodFlows(svcLabels string, key *endpointChangeInfo) error {
	klog.InfoS("add flows for service", "svcLabels", svcLabels, "service", key.svcNamespacedName.String())
	selector, _ := labels.Parse(svcLabels)
	pods, err := c.podLister.List(selector)
	if err != nil {
		klog.ErrorS(err, "Failed to find corresponding Pods with Service labels", "service", key.svcNamespacedName.String(), "labels", svcLabels)
		return err
	}
	for _, pod := range pods {
		if pod.Status.PodIP != "" && pod.Status.HostIP != "" {
			if err := c.ofClient.InstallMulticlusterLocalForwardingFlows(key.svcNamespacedName.String(), net.ParseIP(pod.Status.PodIP), net.ParseIP(pod.Status.HostIP)); err != nil {
				klog.ErrorS(err, "Failed to install multi-cluster local forwarding flows", "podIP", pod.Status.PodIP, "nodeIP", pod.Status.HostIP)
				return err
			}
		}
	}
	return nil
}
