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
	"antrea.io/antrea/pkg/agent/config"
	binding "antrea.io/antrea/pkg/ovs/openflow"
)

// Fields using reg.
var (
	// reg0 (NXM_NX_REG0)
	// reg0[0..3]: Field to store the packet source. Marks in this field include,
	//   - 0: from tunnel interface.
	//   - 1: from Antrea gateway interface.
	//   - 2: from local Pods.
	//   - 3: from uplink interface.
	//   - 4: from bridge local interface.
	PktSourceField     = binding.NewRegField(0, 0, 3, "PacketSource")
	FromTunnelRegMark  = binding.NewRegMark(PktSourceField, 0)
	FromGatewayRegMark = binding.NewRegMark(PktSourceField, 1)
	FromLocalRegMark   = binding.NewRegMark(PktSourceField, 2)
	FromUplinkRegMark  = binding.NewRegMark(PktSourceField, 3)
	FromBridgeRegMark  = binding.NewRegMark(PktSourceField, 4)
	// reg0[4..7]: Field to store the packet destination. Marks in this field include,
	//   - 0: to tunnel interface.
	//   - 1: to local Antrea gateway interface.
	//   - 2: to local Pods.
	//   - 3: to uplink interface.
	//   - 4: to local Node.
	//     - via Antrea gateway interface for per-Node IPAM Pods.
	//     - via bridge local interface for Antrea IPAM Pods.
	//   - 5: to external network.
	//     - via Antrea gateway interface for per-Node IPAM Pods.
	//     - via bridge local interface for Antrea IPAM Pods.
	PktDestinationField = binding.NewRegField(0, 4, 7, "PacketDestination")
	ToTunnelRegMark     = binding.NewRegMark(PktDestinationField, 0)
	ToGatewayRegMark    = binding.NewRegMark(PktDestinationField, 1)
	ToLocalRegMark      = binding.NewRegMark(PktDestinationField, 2)
	ToUplinkRegMark     = binding.NewRegMark(PktDestinationField, 3)
	ToNodeRegMark       = binding.NewRegMark(PktDestinationField, 4)
	ToExternalRegMark   = binding.NewRegMark(PktDestinationField, 5)
	// reg0[0...7]: Union field of the packet source and destination. It is used to mark the hairpin packet. Marks in
	// this field include,
	//  - 0x41: the packet sourced from Antrea gateway interface, and destined for local Node via Antrea gateway interface.
	//  - 0x51: the packet sourced from Antrea gateway interface, and destined for external network via Antrea gateway interface.
	PktUnionField                 = binding.NewRegField(0, 0, 7, "PacketUnion")
	GatewayNodeHairpinRegMark     = binding.NewRegMark(PktUnionField, (ToNodeRegMark.GetValue()<<ToNodeRegMark.GetField().GetRange().Offset())|FromGatewayRegMark.GetValue())
	GatewayExternalHairpinRegMark = binding.NewRegMark(PktUnionField, (ToExternalRegMark.GetValue()<<ToExternalRegMark.GetField().GetRange().Offset())|FromGatewayRegMark.GetValue())
	// reg0[8]: Mark to indicate the ofPort number of an interface is found.
	OFPortFoundRegMark = binding.NewOneBitRegMark(0, 8, "OFPortFound")
	// reg0[9]: Mark to indicate the packet is hairpin, and the packet will be output to the OVS port where it enters OVS
	// in L2ForwardingOutTable.
	HairpinRegMark = binding.NewOneBitRegMark(0, 9, "Hairpin")
	// reg0[10]: Field to indicate which IP should be used to perform SNAT for the hairpin packet.
	SNATWithGatewayIP        = binding.NewOneBitRegMark(0, 10, "SNATWithGatewayIP")
	SNATWithVirtualIP        = binding.NewOneBitZeroRegMark(0, 10, "SNATWithVirtualIP")
	HairpinSNATUnionField    = binding.NewRegField(0, 9, 10, "HairpinSNATUnion")
	HairpinSNATWithVirtualIP = binding.NewRegMark(HairpinSNATUnionField, 1)
	HairpinSNATWithGatewayIP = binding.NewRegMark(HairpinSNATUnionField, 3)
	// reg0[11]: Field to indicate whether the packet's source / destination MAC address needs to be rewritten.
	RewriteMACRegMark    = binding.NewOneBitRegMark(0, 11, "RewriteMAC")
	NotRewriteMACRegMark = binding.NewOneBitZeroRegMark(0, 11, "NotRewriteMAC")
	// reg0[12]: Mark to indicate the packet is denied(Drop/Reject).
	CnpDenyRegMark = binding.NewOneBitRegMark(0, 12, "CNPDeny")
	// reg0[13..14]: Field to indicate disposition of Antrea Policy. It could have more bits to support more disposition
	// that Antrea Policy support in the future. Marks in this field include,
	//   - 0b00: allow
	//   - 0b01: drop
	//   - 0b11: pass
	APDispositionField      = binding.NewRegField(0, 13, 14, "APDisposition")
	DispositionAllowRegMark = binding.NewRegMark(APDispositionField, DispositionAllow)
	DispositionDropRegMark  = binding.NewRegMark(APDispositionField, DispositionDrop)
	DispositionPassRegMark  = binding.NewRegMark(APDispositionField, DispositionPass)
	// reg0[15..18]: Field to indicate the reasons of sending packet to the controller. Marks in this field include,
	//   - 0b00001: logging
	//   - 0b00010: reject
	//   - 0b00100: deny (used by Flow Exporter)
	//   - 0b01000: DNS packet (used by FQDN)
	//   - 0b10000: IGMP packet (used by Multicast)
	CustomReasonField          = binding.NewRegField(0, 15, 19, "PacketInReason")
	CustomReasonLoggingRegMark = binding.NewRegMark(CustomReasonField, CustomReasonLogging)
	CustomReasonRejectRegMark  = binding.NewRegMark(CustomReasonField, CustomReasonReject)
	CustomReasonDenyRegMark    = binding.NewRegMark(CustomReasonField, CustomReasonDeny)
	CustomReasonDNSRegMark     = binding.NewRegMark(CustomReasonField, CustomReasonDNS)
	CustomReasonIGMPRegMark    = binding.NewRegMark(CustomReasonField, CustomReasonIGMP)

	// reg1(NXM_NX_REG1)
	// Field to cache the ofPort of the OVS interface where to output packet.
	TargetOFPortField = binding.NewRegField(1, 0, 31, "TargetOFPort")
	// ToBridgeRegMark marks that the output interface is OVS bridge.
	ToBridgeRegMark = binding.NewRegMark(TargetOFPortField, config.BridgeOFPort)

	// reg2(NXM_NX_REG2)
	// Field to help swap values in two different flow fields in the OpenFlow actions. This field is only used in func
	// `arpResponderStaticFlow`.
	SwapField = binding.NewRegField(2, 0, 31, "SwapValue")

	// reg3(NXM_NX_REG3)
	// Field to store the selected Service Endpoint IP
	EndpointIPField = binding.NewRegField(3, 0, 31, "EndpointIP")
	// Field to store the conjunction ID which is for "deny" rule in CNP. It shares the same register with EndpointIPField,
	// since the service selection will finish when a packet hitting NetworkPolicy related rules.
	CNPDenyConjIDField = binding.NewRegField(3, 0, 31, "CNPDenyConjunctionID")

	// reg4(NXM_NX_REG4)
	// reg4[0..15]: Field to store the selected Service Endpoint port.
	EndpointPortField = binding.NewRegField(4, 0, 15, "EndpointPort")
	// reg4[16..18]: Field to store the state of a packet accessing a Service. Marks in this field include,
	//	- 0b001: packet need to do service selection.
	//	- 0b010: packet has done service selection.
	//	- 0b011: packet has done service selection and the selection result needs to be cached.
	ServiceEPStateField = binding.NewRegField(4, 16, 18, "EndpointState")
	EpToSelectRegMark   = binding.NewRegMark(ServiceEPStateField, 0b001)
	EpSelectedRegMark   = binding.NewRegMark(ServiceEPStateField, 0b010)
	EpToLearnRegMark    = binding.NewRegMark(ServiceEPStateField, 0b011)
	// reg4[0..18]: Field to store the union value of Endpoint port and Endpoint status. It is used as a single match
	// when needed.
	EpUnionField = binding.NewRegField(4, 0, 18, "EndpointUnion")
	// reg4[19]: Mark to indicate the Service type is NodePort.
	ToNodePortAddressRegMark = binding.NewOneBitRegMark(4, 19, "NodePortAddress")
	// reg4[16..19]: Field to store the union value of Endpoint state and the mark of whether Service type is NodePort.
	NodePortUnionField = binding.NewRegField(4, 16, 19, "NodePortUnion")
	// reg4[20]: Field to indicate whether the packet is from local Antrea IPAM Pod. NotAntreaFlexibleIPAMRegMark will
	// be used with RewriteMACRegMark, thus the reg id must not be same due to the limitation of ofnet library.
	AntreaFlexibleIPAMRegMark    = binding.NewOneBitRegMark(4, 20, "AntreaFlexibleIPAM")
	NotAntreaFlexibleIPAMRegMark = binding.NewOneBitZeroRegMark(4, 20, "AntreaFlexibleIPAM")
	// reg4[21..22]: Field to store the state of the first packet of Service NodePort/LoadBalancer connection from Antrea
	// gateway.
	//  - 0b00: the packet doesn't require SNAT.
	//  - 0b01: the packet requires SNAT and has not been marked with CT mark.
	//  - 0b11: the packet requires SNAT and has been marked with CT mark.
	ServiceSNATStateField = binding.NewRegField(4, 21, 22, "ServiceSNAT")
	NotRequireSNATRegMark = binding.NewRegMark(ServiceSNATStateField, 0b00)
	RequireSNATRegMark    = binding.NewRegMark(ServiceSNATStateField, 0b01)
	CTMarkedSNATRegMark   = binding.NewRegMark(ServiceSNATStateField, 0b11)
	// reg4[23]: Mark to indicate the packet is Egress packet.
	EgressRegMark = binding.NewOneBitRegMark(4, 23, "Egress")

	// reg5(NXM_NX_REG5)
	// Field to cache the Egress conjunction ID hit by TraceFlow packet.
	TFEgressConjIDField = binding.NewRegField(5, 0, 31, "TFEgressConjunctionID")

	// reg6(NXM_NX_REG6)
	// Field to store the Ingress conjunction ID hit by TraceFlow packet.
	TFIngressConjIDField = binding.NewRegField(6, 0, 31, "TFIngressConjunctionID")

	// reg7(NXM_NX_REG7)
	// Field to store the GroupID corresponding to the Service.
	ServiceGroupIDField = binding.NewRegField(7, 0, 31, "ServiceGroupID")
)

// Fields using xxreg.
var (
	// xxreg3(NXM_NX_XXREG3)
	// xxreg3: Field to cache Endpoint IPv6 address. It occupies reg12-reg15 in the meanwhile.
	EndpointIP6Field = binding.NewXXRegField(3, 0, 127)
)

// Marks using CT.
var (
	//TODO: There is a bug in libOpenflow when CT_MARK range is from 0 to 0, and a wrong mask will be got,
	// so bit 0 of CT_MARK is not used for now.

	// Mark to indicate the connection is initiated through the host gateway interface
	// (i.e. for which the first packet of the connection was received through the gateway).
	// This CT mark is only used in CtZone / CtZoneV6.
	FromGatewayCTMark = binding.NewCTMark(0b1, 1, 1)
	// Mark to indicate DNAT is performed on the connection for Service.
	// This CT mark is both used in CtZone / CtZoneV6 and SNATCtZone / SNATCtZoneV6.
	ServiceCTMark = binding.NewCTMark(0b1, 2, 2)
	// Mark to indicate the connection is non-Service.
	// This CT mark is used in CtZone / CtZoneV6.
	NotServiceCTMark = binding.NewCTMark(0b0, 2, 2)
	// Mark to indicate the connection is initiated through the host bridge interface
	// (i.e. for which the first packet of the connection was received through the bridge).
	// This CT mark is only used in CtZone / CtZoneV6.
	FromBridgeCTMark = binding.NewCTMark(0b1, 3, 3)
	// Mark to indicate SNAT should be performed on the connection for Service.
	// This CT mark is only used in CtZone / CtZoneV6.
	ServiceSNATCTMark = binding.NewCTMark(0b1, 4, 4)
	// Mark to indicate the connection is hairpin.
	// This CT mark is only used in CtZone / CtZoneV6.
	HairpinCTMark = binding.NewCTMark(0b1, 5, 5)
)

// Fields using CT label.
var (
	// Field to store the ingress rule ID.
	IngressRuleCTLabel = binding.NewCTLabel(0, 31, "ingressRuleCTLabel")

	// Field to store the egress rule ID.
	EgressRuleCTLabel = binding.NewCTLabel(32, 63, "egressRuleCTLabel")
)
