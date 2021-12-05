//go:build !windows
// +build !windows

// package openflow is needed by antctl which is compiled for macOS too.

// Copyright 2021 Antrea Authors
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

// TODO: refactor
func (c *client) snatMarkFlows(snatIP net.IP, mark uint32) []binding.Flow {
	return []binding.Flow{c.snatIPFromTunnelFlow(snatIP, mark)}
}

// Feature: PodConnectivity
// Stage: ClassifierStage
// Tables: ClassifierTable
// Stage: RoutingStage
// Tables: L2ForwardingCalcTable
// Refactored from:
//   - func (c *client) hostBridgeUplinkFlows(localSubnet net.IPNet, category cookie.Category) (flows []binding.Flow)
// hostBridgeUplinkFlows generates the flows that forward traffic between the bridge local port and the uplink port to
// support the host traffic.
// TODO(gran): sync latest changes from pipeline_windows.go
func (c *featurePodConnectivity) hostBridgeUplinkFlows(category cookie.Category, localSubnet net.IPNet) (flows []binding.Flow) {
	flows = c.hostBridgeLocalFlows(category)
	flows = append(flows,
		// This generates the flow to forward ARP from uplink port in normal way.
		ARPSpoofGuardTable.ofTable.BuildFlow(priorityHigh).
			Cookie(c.cookieAllocator.Request(category).Raw()).
			MatchInPort(config.UplinkOFPort).
			MatchProtocol(binding.ProtocolARP).
			Action().Normal().
			Done(),
		// This generates the flow to forward ARP from bridge local port in normal way.
		ARPSpoofGuardTable.ofTable.BuildFlow(priorityHigh).
			Cookie(c.cookieAllocator.Request(category).Raw()).
			MatchInPort(config.BridgeOFPort).
			MatchProtocol(binding.ProtocolARP).
			Action().Normal().
			Done(),
		// Handle packet to Node.
		// Must use a separate flow to Output(config.BridgeOFPort), otherwise OVS will drop the packet:
		//   output:NXM_NX_REG1[]
		//   >> output port 4294967294 is out of range
		//   Datapath actions: drop
		// TODO(gran): support Traceflow
		L2ForwardingCalcTable.ofTable.BuildFlow(priorityNormal).
			Cookie(c.cookieAllocator.Request(category).Raw()).
			MatchDstMAC(c.nodeConfig.UplinkNetConfig.MAC).
			Action().LoadToRegField(TargetOFPortField, config.BridgeOFPort).
			Action().LoadRegMark(OFPortFoundRegMark).
			Action().GotoStage(binding.ConntrackStage).
			Done(),
		L2ForwardingOutTable.ofTable.BuildFlow(priorityHigh).
			Cookie(c.cookieAllocator.Request(category).Raw()).
			MatchProtocol(binding.ProtocolIP).
			MatchRegMark(ToBridgeRegMark).
			MatchRegMark(OFPortFoundRegMark).
			Action().Output(config.BridgeOFPort).
			Done(),
		// Handle outgoing packet from AntreaFlexibleIPAM Pods. Broadcast is not supported.
		L2ForwardingCalcTable.ofTable.BuildFlow(priorityLow).
			Cookie(c.cookieAllocator.Request(category).Raw()).
			MatchRegMark(AntreaFlexibleIPAMRegMark).
			Action().LoadToRegField(TargetOFPortField, config.UplinkOFPort).
			Action().LoadRegMark(OFPortFoundRegMark).
			Action().GotoStage(binding.ConntrackStage).
			Done())
	return flows
}

// Feature: PodConnectivity
// Stage: RoutingStage
// Tables: L3ForwardingTable
// Refactored from:
//   - `func (c *client) l3FwdFlowToRemoteViaRouting(localGatewayMAC net.HardwareAddr, remoteGatewayMAC net.HardwareAddr,
//	    category cookie.Category, peerIP net.IP, peerPodCIDR *net.IPNet) []binding.Flow`
func (c *featurePodConnectivity) l3FwdFlowToRemoteViaRouting(
	category cookie.Category,
	localGatewayMAC net.HardwareAddr,
	remoteGatewayMAC net.HardwareAddr,
	peerIP net.IP,
	peerPodCIDR *net.IPNet) []binding.Flow {
	return []binding.Flow{c.l3FwdFlowToRemoteViaGW(category, localGatewayMAC, *peerPodCIDR)}
}
