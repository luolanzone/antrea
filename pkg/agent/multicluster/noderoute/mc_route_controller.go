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
	"fmt"
	"net"
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
	"github.com/containernetworking/plugins/pkg/ip"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
)

const (
	controllerName = "AntreaAgentMCNodeRouteController"
)

// Controller watches TunnelEndpoint and TunnelEndpointImport events.
// It is responsible for setting up necessary tunnel, IP routes and
// Openflow entries for multi-cluster traffic in a non-Gateway Node.
type Controller struct {
	mcClient             mcclientset.Interface
	ovsBridgeClient      ovsconfig.OVSBridgeClient
	ofClient             openflow.Client
	routeClient          route.Interface
	interfaceStore       interfacestore.InterfaceStore
	networkConfig        *config.NetworkConfig
	nodeConfig           *config.NodeConfig
	teInformer           mcinformers.TunnelEndpointInformer
	teLister             mclisters.TunnelEndpointLister
	teListerSynced       cache.InformerSynced
	teImportInformer     mcinformers.TunnelEndpointImportInformer
	teImportLister       mclisters.TunnelEndpointImportLister
	teImportListerSynced cache.InformerSynced
	queue                workqueue.RateLimitingInterface
	installedTE          cache.Indexer
	installedTEImport    cache.Indexer
	namespace            string
}
type teInfo struct {
	name    string
	subnets []string
	peerIP  string
}

type teImportInfo struct {
	localTEName string
	teImport    mcv1alpha1.TunnelEndpointImport
}

type key struct {
	kind string
	name string
}

func NewMCAgentRouteController(
	mcClient mcclientset.Interface,
	teInformer mcinformers.TunnelEndpointInformer,
	teImportInformer mcinformers.TunnelEndpointImportInformer,
	client openflow.Client,
	ovsBridgeClient ovsconfig.OVSBridgeClient,
	routeClient route.Interface,
	interfaceStore interfacestore.InterfaceStore,
	networkConfig *config.NetworkConfig,
	nodeConfig *config.NodeConfig,
	namespace string,
) *Controller {
	controller := &Controller{
		mcClient:             mcClient,
		ovsBridgeClient:      ovsBridgeClient,
		ofClient:             client,
		routeClient:          routeClient,
		interfaceStore:       interfaceStore,
		networkConfig:        networkConfig,
		nodeConfig:           nodeConfig,
		teInformer:           teInformer,
		teLister:             teInformer.Lister(),
		teListerSynced:       teInformer.Informer().HasSynced,
		teImportInformer:     teImportInformer,
		teImportLister:       teImportInformer.Lister(),
		teImportListerSynced: teImportInformer.Informer().HasSynced,
		queue:                workqueue.NewNamedRateLimitingQueue(workqueue.NewItemExponentialFailureRateLimiter(minRetryDelay, maxRetryDelay), "gatewayroute"),
		installedTE:          cache.NewIndexer(tunnelEndpointInfoKeyFunc, cache.Indexers{tunnelEndpointInfoSubnetsIndexName: tunnelEndpointInfoSubnetsIndexFunc}),
		installedTEImport:    cache.NewIndexer(tunnelEndpointImportKeyFunc, cache.Indexers{tunnelEndpointImportPeerIPIndexName: tunnelEndpointImportIndexFunc}),
		namespace:            namespace,
	}
	controller.teInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(cur interface{}) {
				controller.enqueueTunnelEndpoint(cur)
			},
			UpdateFunc: func(old, cur interface{}) {
				oldTE := old.(*mcv1alpha1.TunnelEndpoint)
				curTE := cur.(*mcv1alpha1.TunnelEndpoint)
				// For general Node, we only concerns about the Gateway Node's IP change for route.
				if oldTE.Spec.PrivateIP == curTE.Spec.PrivateIP && oldTE.Spec.PublicIP == curTE.Spec.PublicIP {
					return
				}
				controller.enqueueTunnelEndpoint(cur)
			},
			DeleteFunc: func(old interface{}) {
				controller.enqueueTunnelEndpoint(old)
			},
		},
		resyncPeriod,
	)
	controller.teImportInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(cur interface{}) {
				controller.enqueueTunnelEndpointImport(cur)
			},
			UpdateFunc: func(old, cur interface{}) {
				controller.enqueueTunnelEndpointImport(cur)
			},
			DeleteFunc: func(old interface{}) {
				controller.enqueueTunnelEndpointImport(old)
			},
		},
		resyncPeriod,
	)
	return controller
}

func tunnelEndpointInfoKeyFunc(obj interface{}) (string, error) {
	return obj.(*teInfo).name, nil
}

func tunnelEndpointInfoSubnetsIndexFunc(obj interface{}) ([]string, error) {
	return obj.(*teInfo).subnets, nil
}

func tunnelEndpointImportKeyFunc(obj interface{}) (string, error) {
	return obj.(*teImportInfo).teImport.Name, nil
}

func tunnelEndpointImportIndexFunc(obj interface{}) ([]string, error) {
	return obj.(*teImportInfo).teImport.Spec.Subnets, nil
}

func (c *Controller) enqueueTunnelEndpoint(obj interface{}) {
	te, err := checkTunnelEndpoint(obj)
	if err != nil {
		return
	}
	if te.Name != c.nodeConfig.Name {
		c.queue.Add(key{
			kind: "TunnelEndpoint",
			name: te.Name,
		})
	}
}

func (c *Controller) enqueueTunnelEndpointImport(obj interface{}) {
	tei, err := checkTunnelEndpointImport(obj)
	if err != nil {
		return
	}
	c.queue.Add(key{
		kind: "TunnelEndpointImport",
		name: tei.Name,
	})
}

// Run will create defaultWorkers workers (go routines) which will process the TunnelEndpoint events from the
// workqueue.
func (c *Controller) Run(stopCh <-chan struct{}) {
	defer c.queue.ShutDown()

	klog.Infof("Starting %s", controllerName)
	defer klog.Infof("Shutting down %s", controllerName)
	cacheSyncs := []cache.InformerSynced{c.teListerSynced, c.teImportListerSynced}
	if !cache.WaitForNamedCacheSync(controllerName, stopCh, cacheSyncs...) {
		return
	}

	if err := c.reconcile(); err != nil {
		klog.ErrorS(err, "Error during reconciliation", "controller", controllerName)
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

	if k, ok := obj.(key); !ok {
		c.queue.Forget(obj)
		klog.Errorf("Expected type 'key' in work queue but got %#v", obj)
		return true
	} else if err := c.syncMCRouteOnGeneralNode(k); err == nil {
		c.queue.Forget(k)
	} else {
		// Put the item back on the workqueue to handle any transient errors.
		c.queue.AddRateLimited(k)
		klog.Errorf("Error syncing %s, requeuing. Error: %v", k, err)
	}
	return true
}

func (c *Controller) syncMCRouteOnGeneralNode(k key) error {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished syncing Node Route for Multicluster %s. (%v)", k.name, time.Since(startTime))
	}()
	switch k.kind {
	case "TunnelEndpoint":
		klog.InfoS("#processing for TunnelEndpoint", "tunnelendpoint", k.name)
		te, err := c.teLister.TunnelEndpoints(c.namespace).Get(k.name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				if err = c.deleteMCNodeRoute(k, k.name, nil); err != nil {
					return err
				}
				return nil
			}
			return err
		}
		if err := c.ofClient.InstallMCL2Forwarding(getHostnameWithPrefix(te.Name), config.DefaultTunOFPort); err != nil {
			return err
		}
		return c.addMCNodeRoute(te, nil)
	case "TunnelEndpointImport":
		klog.InfoS("#processing for TunnelEndpointImport", "tunnelendpointimport", k.name)
		te, err := getValidTunnelEndpoint(c.teLister, c.namespace)
		if err != nil {
			// No need to handle not found error since multi-cluster flows will be added
			// when a leader role of TunnelEndpoint is created.
			klog.InfoS("No valid TunnelEndpoint is found", "err", err)
			return nil
		}
		if te.Name == c.nodeConfig.Name {
			// Do nothing if this Node is a Gateway Node.
			return nil
		}
		tei, err := c.teImportLister.TunnelEndpointImports(c.namespace).Get(k.name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				cachedTEI, isExist, _ := c.installedTEImport.GetByKey(k.name)
				if isExist {
					cachedata := cachedTEI.(*teImportInfo)
					return c.deleteMCNodeRoute(k, te.Spec.Hostname, &cachedata.teImport)
				}
			}
			return err
		}
		klog.InfoS("Add Flows for TunnelEndpointImport", "tunnelendpointimport", tei.Name)
		return c.addMCNodeRoute(te, tei)
	}
	return nil
}

func (c *Controller) deleteMCNodeRoute(k key, teName string, tei *mcv1alpha1.TunnelEndpointImport) error {
	klog.InfoS("Deleting Node Route for Multicluster traffic", "kind", k.kind, "name", k.name)
	if tei != nil {
		klog.InfoS("Deleting node flow", "key", getHostnameWithPrefix(teName+tei.Spec.Hostname))
		if err := c.ofClient.UninstallMulticlusterNodeFlows(getHostnameWithPrefix(teName + tei.Spec.Hostname)); err != nil {
			return fmt.Errorf("failed to uninstall multicluster flows to remote Gateway Node %s: %v", tei.Spec.Hostname, err)
		}
		return nil
	}

	if k.kind == "TunnelEndpoint" {
		cachedTEIs := c.installedTEImport.List()
		for _, tei := range cachedTEIs {
			cached := tei.(*teImportInfo)
			klog.InfoS("Deleting node flow", "key", getHostnameWithPrefix(teName+cached.teImport.Name))
			if err := c.ofClient.UninstallMulticlusterNodeFlows(getHostnameWithPrefix(teName + cached.teImport.Name)); err != nil {
				return fmt.Errorf("failed to uninstall multicluster flows to remote Gateway Node %s from cluster %s: %v", cached.teImport.Name, cached.teImport.Spec.ClusterID, err)
			}
		}
	}
	return nil
}

func (c *Controller) addMCNodeRoute(te *mcv1alpha1.TunnelEndpoint, tei *mcv1alpha1.TunnelEndpointImport) error {
	peerNodeIP := getPeerNodeIP(te.Spec)
	if peerNodeIP == "" {
		klog.InfoS("No valid peer Node IP for the Gateway Node, skip updating openflow rules", "tunnelendpoint", klog.KObj(te), "node", c.nodeConfig.Name)
		return nil
	}

	var allTEImports []*mcv1alpha1.TunnelEndpointImport
	var err error
	if tei == nil {
		cachedTE, installed, _ := c.installedTE.GetByKey(te.Name)
		if installed && cachedTE.(*teInfo).peerIP == peerNodeIP {
			return nil
		}
		allTEImports, err = c.teImportLister.TunnelEndpointImports(c.namespace).List(labels.Everything())
		if err != nil {
			return fmt.Errorf("failed to get TunnelEndpointImport list, err: %v", err)
		}
	} else {
		allTEImports = append(allTEImports, tei)
	}

	if len(allTEImports) == 0 {
		// If no valid subnet is configured, return immediately.
		klog.InfoS("No valid remote subnets configured, nothing is done", "tunnelendpoint", klog.KObj(te), "node", c.nodeConfig.Name)
		return nil
	}

	klog.InfoS("Adding Node Route for Multicluster", "tunnelendpoint", klog.KObj(te), "node", c.nodeConfig.Name)
	for _, teImport := range allTEImports {
		peerConfigs := make(map[*net.IPNet]net.IP, len(teImport.Spec.Subnets))
		for _, subnet := range teImport.Spec.Subnets {
			peerCIDRAddr, peerCIDR, err := net.ParseCIDR(subnet)
			if err != nil {
				klog.Errorf("Failed to parse subnet %s from remote Gateway Node %s for TunnelEndpoint %s", subnet, teImport.Spec.Hostname, te.Name)
				return err
			}
			peerGatewayIP := ip.NextIP(peerCIDRAddr)
			peerConfigs[peerCIDR] = peerGatewayIP
		}

		klog.InfoS("Adding flows to Gateway Node for Multicluster traffic", "gateway", te.Spec.Hostname, "subnets", teImport.Spec.Subnets)
		if err := c.ofClient.InstallMulticlusterNodeFlows(
			getHostnameWithPrefix(te.Spec.Hostname+teImport.Spec.Hostname),
			peerConfigs,
			0,
			net.ParseIP(peerNodeIP)); err != nil {
			return fmt.Errorf("failed to install flows to Node %s: %v", te.Spec.Hostname, err)
		}
		c.installedTEImport.Add(&teImportInfo{
			localTEName: te.Name,
			teImport:    *teImport,
		})
	}
	c.installedTE.Add(&teInfo{
		name:   te.Name,
		peerIP: peerNodeIP,
	})
	return nil
}

func (c *Controller) reconcile() error {
	klog.Infof("Reconciliation for %s", controllerName)
	klog.InfoS("Remove stale Multicluster route")
	return nil
}
