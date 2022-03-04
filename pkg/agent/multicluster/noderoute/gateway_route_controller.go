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

	"github.com/containernetworking/plugins/pkg/ip"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	mcclientset "antrea.io/antrea/multicluster/pkg/client/clientset/versioned"
	mcinformers "antrea.io/antrea/multicluster/pkg/client/informers/externalversions/multicluster/v1alpha1"
	mclisters "antrea.io/antrea/multicluster/pkg/client/listers/multicluster/v1alpha1"
	"antrea.io/antrea/pkg/agent/config"
	"antrea.io/antrea/pkg/agent/interfacestore"
	"antrea.io/antrea/pkg/agent/openflow"
	"antrea.io/antrea/pkg/agent/route"
	"antrea.io/antrea/pkg/ovs/ovsconfig"
)

const (
	gwcontrollerName = "AntreaAgentMCGatewayNodeRouteController"
	// Default name of the default Multi-cluster tunnel interface on the OVS bridge.
	defaultMCTunInterfaceName = "antrea-mc-tun0"
)

// GatewayController watches TunnelEndpoint and TunnelEndpointImport events.
// It is responsible for setting up necessary tunnel, IP routes and
// Openflow entries for multi-cluster traffic in a Gateway Node.
type GatewayController struct {
	mcClient             mcclientset.Interface
	ovsBridgeClient      ovsconfig.OVSBridgeClient
	ofClient             openflow.Client
	routeClient          route.Interface
	interfaceStore       interfacestore.InterfaceStore
	networkConfig        *config.NetworkConfig
	nodeConfig           *config.NodeConfig
	multiclusterConfig   *config.MulticlusterConfig
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
	initialized          bool
}

func NewMCGatewayAgentRouteController(
	mcClient mcclientset.Interface,
	teInformer mcinformers.TunnelEndpointInformer,
	teImportInformer mcinformers.TunnelEndpointImportInformer,
	client openflow.Client,
	ovsBridgeClient ovsconfig.OVSBridgeClient,
	routeClient route.Interface,
	interfaceStore interfacestore.InterfaceStore,
	networkConfig *config.NetworkConfig,
	multiclusterConfig *config.MulticlusterConfig,
	nodeConfig *config.NodeConfig,
	namespace string,
) *GatewayController {
	controller := &GatewayController{
		mcClient:             mcClient,
		ovsBridgeClient:      ovsBridgeClient,
		ofClient:             client,
		routeClient:          routeClient,
		interfaceStore:       interfaceStore,
		networkConfig:        networkConfig,
		nodeConfig:           nodeConfig,
		multiclusterConfig:   multiclusterConfig,
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
				klog.InfoS("TunnelEndpoint Add events")
				controller.enqueueTunnelEndpoint(cur)
			},
			UpdateFunc: func(old, cur interface{}) {
				klog.InfoS("TunnelEndpoint Update events")
				oldTE := old.(*mcv1alpha1.TunnelEndpoint)
				curTE := cur.(*mcv1alpha1.TunnelEndpoint)
				if !elementsMatch(oldTE.Spec.Subnets, curTE.Spec.Subnets) {
					controller.enqueueTunnelEndpoint(cur)
				}
			},
			DeleteFunc: func(old interface{}) {
				klog.InfoS("TunnelEndpoint Delete events")
				controller.enqueueTunnelEndpoint(old)
			},
		},
		resyncPeriod,
	)
	controller.teImportInformer.Informer().AddEventHandlerWithResyncPeriod(
		cache.ResourceEventHandlerFuncs{
			AddFunc: func(cur interface{}) {
				klog.InfoS("TunnelEndpointImport Add events")
				controller.enqueueTunnelEndpointImport(cur)
			},
			UpdateFunc: func(old, cur interface{}) {
				klog.InfoS("TunnelEndpointImport Update events")
				controller.enqueueTunnelEndpointImport(cur)
			},
			DeleteFunc: func(old interface{}) {
				klog.InfoS("TunnelEndpointImport Delete events")
				controller.enqueueTunnelEndpointImport(old)
			},
		},
		resyncPeriod,
	)
	return controller
}

func (c *GatewayController) enqueueTunnelEndpoint(obj interface{}) {
	te, err := checkTunnelEndpoint(obj)
	if err != nil && te == nil {
		return
	}
	if te.Name == c.nodeConfig.Name && te.Spec.Role == "leader" {
		c.queue.Add(key{
			kind: "TunnelEndpoint",
			name: te.Name,
		})
	}
}

func (c *GatewayController) enqueueTunnelEndpointImport(obj interface{}) {
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
func (c *GatewayController) Run(stopCh <-chan struct{}) {
	defer c.queue.ShutDown()

	klog.Infof("Starting %s", gwcontrollerName)
	defer klog.Infof("Shutting down %s", gwcontrollerName)
	cacheSyncs := []cache.InformerSynced{c.teListerSynced, c.teImportListerSynced}
	if !cache.WaitForNamedCacheSync(gwcontrollerName, stopCh, cacheSyncs...) {
		return
	}

	if err := c.reconcile(); err != nil {
		klog.ErrorS(err, "Error during reconciliation", "controller", gwcontrollerName)
	}

	for i := 0; i < defaultWorkers; i++ {
		go wait.Until(c.worker, time.Second, stopCh)
	}
	<-stopCh
}

// worker is a long-running function that will continually call the processNextWorkItem function in
// order to read and process a message on the workqueue.
func (c *GatewayController) worker() {
	for c.processNextWorkItem() {
	}
}

func (c *GatewayController) processNextWorkItem() bool {
	obj, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(obj)

	if k, ok := obj.(key); !ok {
		c.queue.Forget(obj)
		klog.Errorf("Expected type 'key' in work queue but got %#v", obj)
		return true
	} else if err := c.syncMCRouteOnGatewayNode(k); err == nil {
		c.queue.Forget(k)
	} else {
		// Put the item back on the workqueue to handle any transient errors.
		c.queue.AddRateLimited(k)
		klog.Errorf("Error syncing %s, requeuing. Error: %v", k, err)
	}
	return true
}

func (c *GatewayController) syncMCRouteOnGatewayNode(k key) error {
	startTime := time.Now()
	defer func() {
		klog.V(4).Infof("Finished syncing Gateway Node route for Multi-cluster %s. (%v)", k.name, time.Since(startTime))
	}()
	switch k.kind {
	case "TunnelEndpoint":
		klog.InfoS("processing for TunnelEndpoint", "tunnelendpoint", k.name)
		klog.InfoS("Initialization value", "initialized", c.initialized)
		te, err := c.teLister.TunnelEndpoints(c.namespace).Get(k.name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				if err = c.deleteMCNodeRoute(k, te, nil); err != nil {
					return err
				}
				if err = c.deleteMCTunnelInterface(); err != nil {
					return err
				}
				c.initialized = false
				return nil
			}
			return err
		}
		klog.InfoS("new initialized value", "initialized", c.initialized)
		if !c.initialized {
			klog.InfoS("Initialization for a TunnelEndpoint", "tunnelendpoint", k.name)
			// Create the antrea-mc-tun0 tunnel interface for traffic cross multiple clusters.
			if err := c.createMCTunnelInterface(); err != nil {
				return err
			}
			c.initialized = true
		}
		if err := c.ofClient.InstallMCL2Forwarding(te.Name, config.DefaultMCTunOFPort); err != nil {
			return err
		}
		if err = c.addMCClassifierForLocalTraffic(te); err != nil {
			return err
		}
		if err = c.addMCNodeRoute(te, nil); err != nil {
			return err
		}
		return nil
	case "TunnelEndpointImport":
		klog.InfoS("processing for TunnelEndpointImport", "tunnelendpointimport", k.name)
		te, err := getValidTunnelEndpoint(c.teLister, c.namespace)
		if err != nil {
			// No need to handle not found error since multi-cluster flows will be added
			// when a leader role of TunnelEndpoint is created.
			klog.InfoS("No valid TunnelEndpoint is found", "err", err)
			return nil
		}
		if te.Name != c.nodeConfig.Name {
			// Do nothing if this Node is a non-Gateway Node.
			return nil
		}
		tei, err := c.teImportLister.TunnelEndpointImports(c.namespace).Get(k.name)
		if err != nil {
			if apierrors.IsNotFound(err) {
				cachedTEI, isExist, _ := c.installedTEImport.GetByKey(k.name)
				if isExist {
					cachedata := cachedTEI.(*teImportInfo)
					return c.deleteMCNodeRoute(k, te, &cachedata.teImport)
				}
				klog.InfoS("No cached TunnelEndpointImport found, do nothing")
				return nil
			}
			return err
		}
		klog.InfoS("Add Flows for TunnelEndpointImport", "tunnelendpointimport", tei.Name)
		if err = c.addMCClassifierForRemoteTraffic(tei); err != nil {
			return err
		}
		if err = c.addMCNodeRoute(te, tei); err != nil {
			return err
		}
	}
	return nil
}

func (c *GatewayController) deleteMCNodeRoute(k key, te *mcv1alpha1.TunnelEndpoint, tei *mcv1alpha1.TunnelEndpointImport) error {
	klog.InfoS("Deleting remote Node route for Multi-cluster traffic", "kind", k.kind, "name", k.name)
	if tei != nil {
		if err := c.ofClient.UninstallMulticlusterNodeFlows(getHostnameWithPrefix(tei.Spec.Hostname)); err != nil {
			return fmt.Errorf("failed to uninstall multicluster flows to remote Gateway Node %s: %v", tei.Spec.Hostname, err)
		}
		c.installedTEImport.Delete(&teImportInfo{teImport: *tei})
		return nil
	}

	if k.kind == "TunnelEndpoint" {
		cachedTEIs := c.installedTEImport.List()
		for _, tei := range cachedTEIs {
			cached := tei.(*teImportInfo)
			if err := c.ofClient.UninstallMulticlusterNodeFlows(getHostnameWithPrefix(cached.teImport.Name)); err != nil {
				return fmt.Errorf("failed to uninstall multicluster flows to remote Gateway Node %s from cluster %s: %v", cached.teImport.Name, cached.teImport.Spec.ClusterID, err)
			}
			c.installedTEImport.Delete(tei)
		}
	}
	return nil
}

func (c *GatewayController) addMCNodeRoute(te *mcv1alpha1.TunnelEndpoint, tei *mcv1alpha1.TunnelEndpointImport) error {
	var allTEImports []*mcv1alpha1.TunnelEndpointImport
	var err error
	if tei == nil {
		allTEImports, err = c.teImportLister.TunnelEndpointImports(c.namespace).List(labels.Everything())
		if err != nil {
			return fmt.Errorf("failed to get TunnelEndpointImport list, err: %v", err)
		}
	} else {
		allTEImports = append(allTEImports, tei)
	}

	if len(allTEImports) == 0 {
		klog.InfoS("No valid remote Gateway Node found, do nothing", "tunnelendpoint", klog.KObj(te))
		return nil
	}
	var peerNodeIP string
	for _, item := range allTEImports {
		peerNodeIP = getPeerNodeIP(item.Spec)
		if peerNodeIP == "" {
			klog.InfoS("No valid peer Node IP for the remote Gateway Node, skip updating openflow rules", "tunnelendpointimport", klog.KObj(item), "node", c.nodeConfig.Name, "peer", peerNodeIP)
			continue
		}
		TEImport, exist, _ := c.installedTEImport.GetByKey(item.Name)
		if exist {
			cachedTEI := TEImport.(*teImportInfo)
			peerIP := getPeerNodeIP(cachedTEI.teImport.Spec)
			if peerIP == peerNodeIP && elementsMatch(cachedTEI.teImport.Spec.Subnets, item.Spec.Subnets) {
				klog.InfoS("No change from remote Gateway Node impacts the Route for Multi-cluster, do nothing", "tunnelendpointimport", klog.KObj(item), "node", c.nodeConfig.Name, "peer", peerNodeIP)
				continue
			}
		}
		klog.InfoS("Adding remote Gateway Node Route for Multi-cluster", "tunnelendpoint", klog.KObj(te), "node", c.nodeConfig.Name, "peer", peerNodeIP)
		peerConfigs := make(map[*net.IPNet]net.IP, len(item.Spec.Subnets))
		for _, subnet := range item.Spec.Subnets {
			peerCIDRAddr, peerCIDR, err := net.ParseCIDR(subnet)
			if err != nil {
				klog.Errorf("Failed to parse subnet %s", subnet)
				return err
			}
			peerGatewayIP := ip.NextIP(peerCIDRAddr)
			peerConfigs[peerCIDR] = peerGatewayIP
		}

		klog.InfoS("Adding flows to remote Gateway Node for Multi-cluster traffic", "gateway", item.Spec.Hostname, "subnets", item.Spec.Subnets)
		if err := c.ofClient.InstallMulticlusterNodeFlows(
			getHostnameWithPrefix(item.Spec.Hostname),
			peerConfigs,
			config.DefaultMCTunOFPort,
			net.ParseIP(peerNodeIP)); err != nil {
			return fmt.Errorf("failed to install flows to remote Gateway Node %s: %v", item.Spec.Hostname, err)
		}
		c.installedTEImport.Add(&teImportInfo{
			localTEName: te.Name,
			teImport:    *item,
		})
	}
	return nil
}

func (c *GatewayController) createMCTunnelInterface() error {
	tunnelPortName := defaultMCTunInterfaceName
	tunnelIface, portExists := c.interfaceStore.GetInterface(tunnelPortName)
	// Enabling UDP checksum can greatly improve the performance for Geneve and
	// VXLAN tunnels by triggering GRO on the receiver.
	shouldEnableCsum := c.multiclusterConfig.TunnelType == ovsconfig.VXLANTunnel || c.multiclusterConfig.TunnelType == ovsconfig.GeneveTunnel

	// Check the default Multi-cluster tunnel port.
	if portExists {
		if err := c.ovsBridgeClient.DeletePort(tunnelIface.PortUUID); err != nil {
			klog.Errorf("Failed to remove Multi-cluster tunnel port %s: %v", tunnelPortName, err)
		} else {
			klog.Infof("Removed Multi-cluster tunnel port %s with tunnel type: %s", tunnelPortName, tunnelIface.TunnelInterfaceConfig.Type)
			c.interfaceStore.DeleteInterface(tunnelIface)
		}
	}

	tunnelType := ovsconfig.TunnelType(c.multiclusterConfig.TunnelType)
	//tunnelType := ovsconfig.TunnelType("geneve")
	externalIDs := map[string]interface{}{
		interfacestore.AntreaInterfaceTypeKey: interfacestore.AntreaMulticlusterTunnel,
	}
	tunnelPortUUID, err := c.ovsBridgeClient.CreateTunnelPortExt(tunnelPortName, tunnelType, config.DefaultMCTunOFPort, shouldEnableCsum, "", "", "", externalIDs)
	if err != nil {
		klog.Errorf("Failed to create Multi-cluster tunnel port %s type %s on OVS bridge: %v", tunnelPortName, tunnelType, err)
		return err
	}
	tunnelIface = interfacestore.NewTunnelInterface(tunnelPortName, tunnelType, nil, shouldEnableCsum)
	tunnelIface.OVSPortConfig = &interfacestore.OVSPortConfig{PortUUID: tunnelPortUUID, OFPort: config.DefaultMCTunOFPort}
	c.interfaceStore.AddInterface(tunnelIface)
	return nil
}

func (c *GatewayController) deleteMCTunnelInterface() error {
	klog.InfoS("Remove Multi-cluster tunnel interface in the Gateway Node", "interface", defaultMCTunInterfaceName)
	tunnelPortName := defaultMCTunInterfaceName
	tunnelIface, portExists := c.interfaceStore.GetInterface(tunnelPortName)
	// Check the default Multi-cluster tunnel port.
	if portExists {
		if err := c.ovsBridgeClient.DeletePort(tunnelIface.PortUUID); err != nil {
			klog.Errorf("Failed to remove Multi-cluster tunnel port %s: %v", tunnelPortName, err)
		} else {
			klog.Infof("Removed Multi-cluster tunnel port %s with tunnel type: %s", tunnelPortName, tunnelIface.TunnelInterfaceConfig.Type)
			c.interfaceStore.DeleteInterface(tunnelIface)
		}
	}
	c.initialized = false
	return nil
}

func (c *GatewayController) reconcile() error {
	klog.Infof("Reconciliation for %s", gwcontrollerName)
	klog.InfoS("Remove stale Multi-cluster route")
	return nil
}

func (c *GatewayController) addMCClassifierForLocalTraffic(te *mcv1alpha1.TunnelEndpoint) error {
	if len(te.Spec.Subnets) == 0 {
		klog.InfoS("Do nothing when subnets are empty", "tunnelendpoint", klog.KObj(te))
	}
	cidrs, err := parseSubnets(te.Spec.Subnets)
	if err != nil {
		return err
	}
	if err := c.ofClient.InstallMulticlusterLocalClassifier(getHostnameWithPrefix(te.Spec.Hostname), config.DefaultMCTunOFPort, cidrs); err != nil {
		return err
	}
	return nil
}

func (c *GatewayController) addMCClassifierForRemoteTraffic(tei *mcv1alpha1.TunnelEndpointImport) error {
	if len(tei.Spec.Subnets) == 0 {
		klog.InfoS("Do nothing when subnets are empty", "tunnelendpointimport", klog.KObj(tei))
	}
	cidrs, err := parseSubnets(tei.Spec.Subnets)
	if err != nil {
		return err
	}
	if err := c.ofClient.InstallMulticlusterRemoteClassifier(tei.Spec.Hostname, config.DefaultTunOFPort, cidrs); err != nil {
		return err
	}
	return nil
}

func parseSubnets(subnets []string) ([]net.IPNet, error) {
	var cidrs []net.IPNet
	for _, subnet := range subnets {
		_, peerCIDR, err := net.ParseCIDR(subnet)
		if err != nil {
			klog.Errorf("Failed to parse subnet %s", subnet)
			return nil, err
		}
		cidrs = append(cidrs, *peerCIDR)
	}
	return cidrs, nil
}

// ParseMCTunnelInterfaceConfig initializes and returns an InterfaceConfig struct
// for a Multi-cluster tunnel interface.
func ParseMCTunnelInterfaceConfig(
	portData *ovsconfig.OVSPortData,
	portConfig *interfacestore.OVSPortConfig) *interfacestore.InterfaceConfig {
	if portData.Options == nil {
		klog.V(2).Infof("OVS port %s has no options", portData.Name)
		return nil
	}
	_, localIP, _, csum := ovsconfig.ParseTunnelInterfaceOptions(portData)

	var interfaceConfig = interfacestore.NewTunnelInterface(portData.Name, ovsconfig.TunnelType(portData.IFType), localIP, csum)
	interfaceConfig.OVSPortConfig = portConfig
	return interfaceConfig
}
