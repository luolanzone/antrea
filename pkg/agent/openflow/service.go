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

package openflow

import (
	"net"
	"sync"

	"k8s.io/klog/v2"

	"antrea.io/antrea/pkg/agent/config"
	"antrea.io/antrea/pkg/agent/openflow/cookie"
	binding "antrea.io/antrea/pkg/ovs/openflow"
)

type featureService struct {
	cookieAllocator cookie.Allocator
	ipProtocols     []binding.Protocol
	bridge          binding.Bridge

	serviceFlowCache    *flowCategoryCache
	defaultServiceFlows []binding.Flow
	groupCache          sync.Map

	gatewayIPs  map[binding.Protocol]net.IP
	virtualIPs  map[binding.Protocol]net.IP
	dnatCtZones map[binding.Protocol]int
	snatCtZones map[binding.Protocol]int
	gatewayMAC  net.HardwareAddr

	enableProxy           bool
	proxyAll              bool
	connectUplinkToBridge bool
}

func (c *featureService) getFeatureName() featureName {
	return Service
}

func newFeatureService(
	cookieAllocator cookie.Allocator,
	ipProtocols []binding.Protocol,
	nodeConfig *config.NodeConfig,
	bridge binding.Bridge,
	enableProxy,
	proxyAll,
	connectUplinkToBridge bool) *featureService {
	gatewayIPs := make(map[binding.Protocol]net.IP)
	virtualIPs := make(map[binding.Protocol]net.IP)
	dnatCtZones := make(map[binding.Protocol]int)
	snatCtZones := make(map[binding.Protocol]int)
	for _, ipProtocol := range ipProtocols {
		if ipProtocol == binding.ProtocolIP {
			gatewayIPs[ipProtocol] = nodeConfig.GatewayConfig.IPv4
			virtualIPs[ipProtocol] = config.VirtualServiceIPv4
			dnatCtZones[ipProtocol] = CtZone
			snatCtZones[ipProtocol] = SNATCtZone
		} else if ipProtocol == binding.ProtocolIPv6 {
			gatewayIPs[ipProtocol] = nodeConfig.GatewayConfig.IPv6
			virtualIPs[ipProtocol] = config.VirtualServiceIPv6
			dnatCtZones[ipProtocol] = CtZoneV6
			snatCtZones[ipProtocol] = SNATCtZoneV6
		}
	}

	return &featureService{
		cookieAllocator:       cookieAllocator,
		ipProtocols:           ipProtocols,
		bridge:                bridge,
		serviceFlowCache:      newFlowCategoryCache(),
		groupCache:            sync.Map{},
		gatewayIPs:            gatewayIPs,
		virtualIPs:            virtualIPs,
		dnatCtZones:           dnatCtZones,
		snatCtZones:           snatCtZones,
		gatewayMAC:            nodeConfig.GatewayConfig.MAC,
		enableProxy:           enableProxy,
		proxyAll:              proxyAll,
		connectUplinkToBridge: connectUplinkToBridge,
	}
}

func (c *featureService) initFlows(category cookie.Category) []binding.Flow {
	var flows []binding.Flow
	if c.enableProxy {
		flows = append(flows, c.conntrackFlows(category)...)
		flows = append(flows, c.preRoutingClassifierFlows(category)...)
		flows = append(flows, c.snatConntrackFlows(category)...)
		flows = append(flows, c.l3FwdFlowsToExternal(category)...)
		flows = append(flows, c.hairpinBypassFlows(category)...)
		flows = append(flows, c.gatewayHairpinFlows(category)...)
	}
	return flows
}

func (c *featureService) replayFlows() []binding.Flow {
	var flows []binding.Flow

	// Get fixed flows.
	for _, flow := range c.defaultServiceFlows {
		flow.Reset()
		flows = append(flows, flow)
	}

	// Get cached flows.
	rangeFunc := func(key, value interface{}) bool {
		fCache := value.(flowCache)
		for _, flow := range fCache {
			flow.Reset()
			flows = append(flows, flow)
		}
		return true
	}
	c.serviceFlowCache.Range(rangeFunc)

	return flows
}

func (c *featureService) replayGroups() {
	c.groupCache.Range(func(id, value interface{}) bool {
		group := value.(binding.Group)
		group.Reset()
		if err := group.Add(); err != nil {
			klog.Errorf("Error when replaying cached group %d: %v", id, err)
		}
		return true
	})
}
