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

package noderoute

import (
	"time"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	mcclientset "antrea.io/antrea/multicluster/pkg/client/clientset/versioned"
	mcinformers "antrea.io/antrea/multicluster/pkg/client/informers/externalversions/multicluster/v1alpha1"
	mclisters "antrea.io/antrea/multicluster/pkg/client/listers/multicluster/v1alpha1"
	"antrea.io/antrea/pkg/agent/config"
	"antrea.io/antrea/pkg/agent/interfacestore"
	"antrea.io/antrea/pkg/agent/openflow"
	"antrea.io/antrea/pkg/agent/route"
	"antrea.io/antrea/pkg/ovs/ovsconfig"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	controllerName = "AntreaAgentMCNodeRouteController"
	// Interval of reprocessing every node.
	teResyncPeriod = 60 * time.Second
	// How long to wait before retrying the processing of a TunnelEndpoint change
	minRetryDelay = 2 * time.Second
	maxRetryDelay = 120 * time.Second

	// Default number of workers processing a TunnelEndpoint change
	defaultWorkers = 1
)

// Controller is responsible for setting up necessary tunnel, IP routes and
// Openflow entries for multi-cluster traffic.
type Controller struct {
	mcClient        mcclientset.Interface
	ovsBridgeClient ovsconfig.OVSBridgeClient
	ofClient        openflow.Client
	routeClient     route.Interface
	interfaceStore  interfacestore.InterfaceStore
	networkConfig   *config.NetworkConfig
	nodeConfig      *config.NodeConfig
	teInformer      mcinformers.TunnelEndpointInformer
	teLister        mclisters.TunnelEndpointLister
	teListerSynced  cache.InformerSynced
	queue           workqueue.RateLimitingInterface
	namespace       string
}

func NewMCAgentRouteController(
	mcClient mcclientset.Interface,
	teInformer mcinformers.TunnelEndpointInformer,
	client openflow.Client,
	ovsBridgeClient ovsconfig.OVSBridgeClient,
	routeClient route.Interface,
	interfaceStore interfacestore.InterfaceStore,
	networkConfig *config.NetworkConfig,
	nodeConfig *config.NodeConfig,
	namespace string,
) *Controller {
	controller := &Controller{
		mcClient:        mcClient,
		ovsBridgeClient: ovsBridgeClient,
		ofClient:        client,
		routeClient:     routeClient,
		interfaceStore:  interfaceStore,
		networkConfig:   networkConfig,
		nodeConfig:      nodeConfig,
		teInformer:      teInformer,
		teLister:        teInformer.Lister(),
		teListerSynced:  teInformer.Informer().HasSynced,
		queue:           workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(minRetryDelay, maxRetryDelay), "gatewayroute"),
		namespace:       namespace,
	}
	controller.teInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(cur interface{}) {
				controller.enqueueTunnelEndpoint(cur)
			},
			UpdateFunc: func(old, cur interface{}) {
				controller.enqueueTunnelEndpoint(cur)
			},
			DeleteFunc: func(old interface{}) {
				controller.enqueueTunnelEndpoint(old)
			},
		},
		teResyncPeriod,
	)
	return controller
}

func (c *Controller) enqueueTunnelEndpoint(obj interface{}) {
	te, isTE := obj.(*mcv1alpha1.TunnelEndpoint)
	if !isTE {
		deletedState, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			klog.Errorf("Received unexpected object: %v", obj)
			return
		}
		te, ok = deletedState.Obj.(*mcv1alpha1.TunnelEndpoint)
		if !ok {
			klog.Errorf("DeletedFinalStateUnknown contains non-Node object: %v", deletedState.Obj)
			return
		}
	}

	// Ignore notifications for this TunnelEndpoint, no need to establish connectivity to itself.
	if te.Spec.Hostname != c.nodeConfig.Name {
		c.queue.Add(te.Spec.Hostname)
	}
}

// Run will create defaultWorkers workers (go routines) which will process the TunnelEndpoint events from the
// workqueue.
func (c *Controller) Run(stopCh <-chan struct{}) {
	defer c.queue.ShutDown()

	klog.Infof("Starting %s", controllerName)
	defer klog.Infof("Shutting down %s", controllerName)

	if !cache.WaitForNamedCacheSync(controllerName, stopCh, c.teListerSynced) {
		return
	}

	if err := c.reconcile(); err != nil {
		klog.ErrorS(err, "Error during reconciliation", "controller", controllerName)
	}

	if err := c.initialize(); err != nil {
		klog.ErrorS(err, "Error during initialization", "controller", controllerName)
	}

	for i := 0; i < defaultWorkers; i++ {
		go wait.Until(c.worker, time.Second, stopCh)
	}
	<-stopCh
}

// worker is a long-running function that will continually call the processNextWorkItem function in
// order to read and process a message on the workqueue.
func (c *Controller) worker() {
	for c.processNextWorkItem() {
	}
}

func (c *Controller) processNextWorkItem() bool {
	obj, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(obj)

	// We expect strings (Node name) to come off the workqueue.
	if key, ok := obj.(string); !ok {
		c.queue.Forget(obj)
		klog.Errorf("Expected string in work queue but got %#v", obj)
		return true
	} else if err := c.syncMCRoute(key); err == nil {
		c.queue.Forget(key)
	} else {
		// Put the item back on the workqueue to handle any transient errors.
		c.queue.AddRateLimited(key)
		klog.Errorf("Error syncing TunnelEndpoint %s, requeuing. Error: %v", key, err)
	}
	return true
}

func (c *Controller) syncMCRoute(teName string) error {
	startTime := time.Now()
	defer func() {
		klog.Infof("Finished syncing Node Route for Multicluster %s. (%v)", teName, time.Since(startTime))
	}()
	te, err := c.teLister.TunnelEndpoints(c.namespace).Get(teName)
	if err != nil {
		return c.deleteMCNodeRoute(teName)
	}
	return c.addMCNodeRoute(te)
}

func (c *Controller) deleteMCNodeRoute(teName string) error {
	klog.InfoS("Deleting Node Route", "tunnelenpoint", teName)
	return nil
}

func (c *Controller) addMCNodeRoute(te *mcv1alpha1.TunnelEndpoint) error {
	klog.InfoS("Adding Node Route", "tunnelenpoint", klog.KObj(te), "node", c.nodeConfig.Name)
	return nil
}

func (c *Controller) reconcile() error {
	klog.Infof("Reconciliation for %s", controllerName)
	klog.InfoS("Remove stale Multicluster route")
	return nil
}

func (c *Controller) initialize() error {
	klog.Infof("Initialization for %s", controllerName)
	klog.InfoS("Create default tunnel port for Multicluster")
	return nil
}
