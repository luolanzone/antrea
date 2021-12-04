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

	"antrea.io/antrea/pkg/agent/config"
	"antrea.io/antrea/pkg/agent/openflow/cookie"
	binding "antrea.io/antrea/pkg/ovs/openflow"
)

type featurePodConnectivity struct {
	cookieAllocator cookie.Allocator
	ipProtocols     []binding.Protocol

	nodeFlowCache       *flowCategoryCache
	podFlowCache        *flowCategoryCache
	gatewayFlows        []binding.Flow
	defaultTunnelFlows  []binding.Flow
	hostNetworkingFlows []binding.Flow

	gatewayIPs    map[binding.Protocol]net.IP
	ctZones       map[binding.Protocol]int
	localCIDRs    map[binding.Protocol]net.IPNet
	nodeIPs       map[binding.Protocol]net.IP
	nodeConfig    *config.NodeConfig
	networkConfig *config.NetworkConfig

	connectUplinkToBridge bool
	enableMulticast       bool
}

func (c *featurePodConnectivity) getFeatureID() featureID {
	return PodConnectivity
}

func newFeaturePodConnectivity(
	cookieAllocator cookie.Allocator,
	ipProtocols []binding.Protocol,
	nodeConfig *config.NodeConfig,
	networkConfig *config.NetworkConfig,
	connectUplinkToBridge bool,
	enableMulticast bool) *featurePodConnectivity {
	ctZones := make(map[binding.Protocol]int)
	gatewayIPs := make(map[binding.Protocol]net.IP)
	localCIDRs := make(map[binding.Protocol]net.IPNet)
	nodeIPs := make(map[binding.Protocol]net.IP)
	for _, ipProtocol := range ipProtocols {
		if ipProtocol == binding.ProtocolIP {
			ctZones[ipProtocol] = CtZone
			gatewayIPs[ipProtocol] = nodeConfig.GatewayConfig.IPv4
			localCIDRs[ipProtocol] = *nodeConfig.PodIPv4CIDR
			nodeIPs[ipProtocol] = nodeConfig.NodeIPv4Addr.IP
		} else if ipProtocol == binding.ProtocolIPv6 {
			ctZones[ipProtocol] = CtZoneV6
			gatewayIPs[ipProtocol] = nodeConfig.GatewayConfig.IPv6
			localCIDRs[ipProtocol] = *nodeConfig.PodIPv6CIDR
			nodeIPs[ipProtocol] = nodeConfig.NodeIPv6Addr.IP
		}
	}

	return &featurePodConnectivity{
		cookieAllocator:       cookieAllocator,
		ipProtocols:           ipProtocols,
		nodeFlowCache:         newFlowCategoryCache(),
		podFlowCache:          newFlowCategoryCache(),
		gatewayIPs:            gatewayIPs,
		ctZones:               ctZones,
		localCIDRs:            localCIDRs,
		nodeIPs:               nodeIPs,
		nodeConfig:            nodeConfig,
		networkConfig:         networkConfig,
		connectUplinkToBridge: connectUplinkToBridge,
		enableMulticast:       enableMulticast,
	}
}

func (c *featurePodConnectivity) initialize(category cookie.Category) []binding.Flow {
	var flows []binding.Flow

	for _, ipProtocol := range c.ipProtocols {
		if ipProtocol == binding.ProtocolIPv6 {
			flows = append(flows, c.ipv6Flows(category)...)
		} else if ipProtocol == binding.ProtocolIP {
			flows = append(flows, c.arpNormalFlow(category))
			flows = append(flows, c.arpResponderFlow(category, c.nodeConfig.GatewayConfig.IPv4, c.nodeConfig.GatewayConfig.MAC))
		}
	}
	flows = append(flows, c.defaultDropFlows(category)...)
	flows = append(flows, c.l3FwdFlowToLocalCIDR(category)...)
	flows = append(flows, c.l3FwdFlowToNode(category)...)
	flows = append(flows, c.l3FwdFlowToExternal(category))
	flows = append(flows, c.decTTLFlows(category)...)
	flows = append(flows, c.conntrackFlows(category)...)
	flows = append(flows, c.l2ForwardOutputFlow(category))
	if c.networkConfig.TrafficEncapMode.IsNetworkPolicyOnly() {
		flows = append(flows, c.l3FwdFlowRouteToGW(category)...)
		// If IPv6 is enabled, this flow will never get hit.
		// Replies any ARP request with the same global virtual MAC.
		flows = append(flows, c.arpResponderStaticFlow(category))
	}
	return flows
}
