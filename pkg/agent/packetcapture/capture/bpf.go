// Copyright 2024 Antrea Authors.
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

package capture

import (
	"encoding/binary"
	"net"
	"strings"

	"golang.org/x/net/bpf"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/utils/ptr"

	crdv1alpha1 "antrea.io/antrea/pkg/apis/crd/v1alpha1"
)

const (
	lengthByte    int    = 1
	lengthHalf    int    = 2
	lengthWord    int    = 4
	bitsPerWord   int    = 32
	etherTypeIPv4 uint32 = 0x0800

	jumpMask           uint32 = 0x1fff
	ip4SourcePort      uint32 = 14
	ip4DestinationPort uint32 = 16
	ip4HeaderSize      uint32 = 14
	ip4HeaderFlags     uint32 = 20
)

var (
	returnDrop                 = bpf.RetConstant{Val: 0}
	returnKeep                 = bpf.RetConstant{Val: 0x40000}
	loadIPv4SourcePort         = bpf.LoadIndirect{Off: ip4SourcePort, Size: lengthHalf}
	loadIPv4DestinationPort    = bpf.LoadIndirect{Off: ip4DestinationPort, Size: lengthHalf}
	loadEtherKind              = bpf.LoadAbsolute{Off: 12, Size: lengthHalf}
	loadIPv4SourceAddress      = bpf.LoadAbsolute{Off: 26, Size: lengthWord}
	loadIPv4DestinationAddress = bpf.LoadAbsolute{Off: 30, Size: lengthWord}
	loadIPv4Protocol           = bpf.LoadAbsolute{Off: 23, Size: lengthByte}
	loadIPv4TCPFlags           = bpf.LoadIndirect{Off: 27, Size: lengthByte}
	loadIPv4ICMPType           = bpf.LoadIndirect{Off: 14, Size: lengthByte}
	loadIPv4ICMPCode           = bpf.LoadIndirect{Off: 15, Size: lengthByte}
)

var ProtocolMap = map[string]uint32{
	"UDP":  17,
	"TCP":  6,
	"ICMP": 1,
}

var ICMPMsgTypeMap = map[crdv1alpha1.ICMPMsgType]uint32{
	crdv1alpha1.ICMPMsgTypeEcho:       8,
	crdv1alpha1.ICMPMsgTypeEchoReply:  0,
	crdv1alpha1.ICMPMsgTypeDstUnreach: 3,
	crdv1alpha1.ICMPMsgTypeTimexceed:  11,
}

type tcpFlagsFilter struct {
	flag uint32
	mask uint32
}

type icmpFilter struct {
	icmpType uint32
	icmpCode *uint32
}

type transportFilters struct {
	srcPort  uint16
	dstPort  uint16
	tcpFlags []tcpFlagsFilter
	icmp     []icmpFilter
}

func loadIPv4HeaderOffset(skipTrue uint8) []bpf.Instruction {
	return []bpf.Instruction{
		bpf.LoadAbsolute{Off: ip4HeaderFlags, Size: lengthHalf},              // flags+fragment offset, since we need to calc where the src/dst port is
		bpf.JumpIf{Cond: bpf.JumpBitsSet, Val: jumpMask, SkipTrue: skipTrue}, // check if there is a L4 header
		bpf.LoadMemShift{Off: ip4HeaderSize},                                 // calculate the size of IP header
	}
}

func compareProtocolIP4(skipTrue, skipFalse uint8) bpf.Instruction {
	return bpf.JumpIf{Cond: bpf.JumpEqual, Val: etherTypeIPv4, SkipTrue: skipTrue, SkipFalse: skipFalse}
}

func compareProtocol(protocol uint32, skipTrue, skipFalse uint8) bpf.Instruction {
	return bpf.JumpIf{Cond: bpf.JumpEqual, Val: protocol, SkipTrue: skipTrue, SkipFalse: skipFalse}
}

func calculateSkipFalse(transport *transportFilters) uint8 {
	var count uint8
	// load dstIP and compare
	count += 2

	if transport.srcPort > 0 || transport.dstPort > 0 || len(transport.tcpFlags) > 0 || len(transport.icmp) > 0 {
		// load fragment offset
		count += 3

		if transport.srcPort > 0 {
			count += 2
		}
		if transport.dstPort > 0 {
			count += 2
		}
		if len(transport.tcpFlags) > 0 {
			count += uint8(len(transport.tcpFlags) * 3)
		}
		if len(transport.icmp) > 0 {
			count += 1
			for _, m := range transport.icmp {
				count += 1
				if m.icmpCode != nil {
					count += 2
				}
			}
		}
	}
	// ret keep
	count += 1

	return count
}

// Generates IP address and port matching instructions
func compileIPAndTransportFilters(srcAddrVal, dstAddrVal uint32, size, curLen uint8, transport *transportFilters, needsOtherTrafficDirectionCheck bool) []bpf.Instruction {
	inst := []bpf.Instruction{}

	// from here we need to check the inst length to calculate skipFalse. If no protocol is set, there will be no related bpf instructions.

	// calculate skip size to jump to the final instruction (NO MATCH)
	skipToEnd := func() uint8 {
		return size - curLen - uint8(len(inst)) - 2
	}

	// needsOtherTrafficDirectionCheck indicates if we need to check whether the packet belongs to the return traffic flow when source IP from the
	// packet spec and packet header don't match and we are capturing packets in both direction. If true, we calculate skipFalse to jump to the
	// instruction that compares the destination IP from the packet spec with the loaded source IP from the packet header.
	if needsOtherTrafficDirectionCheck {
		inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: srcAddrVal, SkipTrue: 0, SkipFalse: calculateSkipFalse(transport)})
	} else {
		inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: srcAddrVal, SkipTrue: 0, SkipFalse: skipToEnd()})
	}

	// dst ip
	inst = append(inst, loadIPv4DestinationAddress)
	inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: dstAddrVal, SkipTrue: 0, SkipFalse: skipToEnd()})

	if transport.srcPort > 0 || transport.dstPort > 0 || len(transport.tcpFlags) > 0 || len(transport.icmp) > 0 {
		skipTrue := skipToEnd() - 1
		inst = append(inst, loadIPv4HeaderOffset(skipTrue)...)
		if transport.srcPort > 0 {
			inst = append(inst, loadIPv4SourcePort)
			inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: uint32(transport.srcPort), SkipTrue: 0, SkipFalse: skipToEnd()})
		}
		if transport.dstPort > 0 {
			inst = append(inst, loadIPv4DestinationPort)
			inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: uint32(transport.dstPort), SkipTrue: 0, SkipFalse: skipToEnd()})
		}

		// tcp flags
		if len(transport.tcpFlags) > 0 {
			for i, f := range transport.tcpFlags {
				inst = append(inst, loadIPv4TCPFlags)
				inst = append(inst, bpf.ALUOpConstant{Op: bpf.ALUOpAnd, Val: f.mask})
				if i == len(transport.tcpFlags)-1 { // last flag match condition
					inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: f.flag, SkipTrue: 0, SkipFalse: skipToEnd()})
				} else {
					inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: f.flag, SkipTrue: skipToEnd() - 1, SkipFalse: 0})
				}
			}
		}

		// icmp messages
		if len(transport.icmp) > 0 {
			inst = append(inst, loadIPv4ICMPType)
			for i, f := range transport.icmp {
				var skipTrue, skipFalse uint8
				if f.icmpCode != nil {
					if i != len(transport.icmp)-1 {
						skipFalse = 2
					} else {
						skipFalse = skipToEnd()
					}
				} else {
					if i != len(transport.icmp)-1 {
						skipTrue, skipFalse = skipToEnd()-1, 0
					} else {
						skipTrue, skipFalse = 0, skipToEnd()
					}
				}
				inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: f.icmpType, SkipTrue: skipTrue, SkipFalse: skipFalse})
				if f.icmpCode != nil {
					inst = append(inst, loadIPv4ICMPCode)
					inst = append(inst, bpf.JumpIf{Cond: bpf.JumpEqual, Val: *f.icmpCode, SkipTrue: skipToEnd() - 1, SkipFalse: skipToEnd()})
				}
			}
		}
	}

	// return (accept)
	inst = append(inst, returnKeep)

	return inst
}

// compilePacketFilter compiles the CRD spec to bpf instructions. For now, we only focus on
// ipv4 traffic. Compared to the raw BPF filter supported by libpcap, we only need to support
// limited use cases, so an expression parser is not needed.
func compilePacketFilter(packetSpec *crdv1alpha1.Packet, srcIP, dstIP net.IP, direction crdv1alpha1.CaptureDirection) []bpf.Instruction {
	size := uint8(calculateInstructionsSize(packetSpec, direction))

	// ipv4 check
	inst := []bpf.Instruction{loadEtherKind}
	// skip means how many instructions we need to skip if the compare fails.
	// for example, for now we have 2 instructions, and the total size is 17, if ipv4
	// check failed, we need to jump to the end (ret #0), skip 17-3=14 instructions.
	// if check succeed, skipTrue means we jump to the next instruction. Here 3 means we
	// have 3 instructions so far.
	inst = append(inst, compareProtocolIP4(0, size-3))

	if packetSpec != nil {
		if packetSpec.Protocol != nil {
			var proto uint32
			if packetSpec.Protocol.Type == intstr.Int {
				proto = uint32(packetSpec.Protocol.IntVal)
			} else {
				proto = ProtocolMap[strings.ToUpper(packetSpec.Protocol.StrVal)]
			}

			inst = append(inst, loadIPv4Protocol)
			inst = append(inst, compareProtocol(proto, 0, size-5))
		}
	}

	srcAddrVal := binary.BigEndian.Uint32(srcIP[len(srcIP)-4:])
	dstAddrVal := binary.BigEndian.Uint32(dstIP[len(dstIP)-4:])

	// ports, TCP flags and ICMP messages
	var transport transportFilters
	if packetSpec.TransportHeader.TCP != nil {
		if packetSpec.TransportHeader.TCP.SrcPort != nil {
			transport.srcPort = uint16(*packetSpec.TransportHeader.TCP.SrcPort)
		}
		if packetSpec.TransportHeader.TCP.DstPort != nil {
			transport.dstPort = uint16(*packetSpec.TransportHeader.TCP.DstPort)
		}
		if packetSpec.TransportHeader.TCP.Flags != nil {
			for _, f := range packetSpec.TransportHeader.TCP.Flags {
				m := f.Value // default to flag if not specified
				if f.Mask != nil {
					m = *f.Mask
				}
				transport.tcpFlags = append(transport.tcpFlags, tcpFlagsFilter{
					flag: uint32(f.Value),
					mask: uint32(m),
				})
			}
		}
	} else if packetSpec.TransportHeader.UDP != nil {
		if packetSpec.TransportHeader.UDP.SrcPort != nil {
			transport.srcPort = uint16(*packetSpec.TransportHeader.UDP.SrcPort)
		}
		if packetSpec.TransportHeader.UDP.DstPort != nil {
			transport.dstPort = uint16(*packetSpec.TransportHeader.UDP.DstPort)
		}
	} else if packetSpec.TransportHeader.ICMP != nil {
		for _, f := range packetSpec.TransportHeader.ICMP.Messages {
			var typeValue uint32
			var codeValue *uint32
			if f.Type.Type == intstr.Int {
				typeValue = uint32(f.Type.IntVal)
			} else {
				typeValue = ICMPMsgTypeMap[crdv1alpha1.ICMPMsgType(strings.ToLower(f.Type.StrVal))]
			}
			if f.Code != nil {
				codeValue = ptr.To(uint32(*f.Code))
			}

			transport.icmp = append(transport.icmp, icmpFilter{
				icmpType: typeValue,
				icmpCode: codeValue,
			})
		}
	}

	inst = append(inst, loadIPv4SourceAddress)

	switch direction {
	case crdv1alpha1.CaptureDirectionSourceToDestination:
		inst = append(inst, compileIPAndTransportFilters(srcAddrVal, dstAddrVal, size, uint8(len(inst)), &transport, false)...)
	case crdv1alpha1.CaptureDirectionDestinationToSource:
		transport.srcPort, transport.dstPort = transport.dstPort, transport.srcPort
		inst = append(inst, compileIPAndTransportFilters(dstAddrVal, srcAddrVal, size, uint8(len(inst)), &transport, false)...)
	default:
		inst = append(inst, compileIPAndTransportFilters(srcAddrVal, dstAddrVal, size, uint8(len(inst)), &transport, true)...)
		transport.srcPort, transport.dstPort = transport.dstPort, transport.srcPort
		inst = append(inst, compileIPAndTransportFilters(dstAddrVal, srcAddrVal, size, uint8(len(inst)), &transport, false)...)
	}

	// return (drop)
	inst = append(inst, returnDrop)

	return inst

}

// We need to figure out how long the instruction list will be first. It will be used in the instructions' jump case.
// For example, If you provide all the filters supported by `PacketCapture`, it will end with the following BPF filter string:
// 'ip proto 6 and src host 127.0.0.1 and dst host 127.0.0.1 and src port 123 and dst port 124'
// And using `tcpdump -i <device> '<filter>' -d` will generate the following BPF instructions:
// (000) ldh      [12]                                     # Load 2B at 12 (Ethertype)
// (001) jeq      #0x800           jt 2	jf 16              # Ethertype: If IPv4, goto #2, else #16
// (002) ldb      [23]                                     # Load 1B at 23 (IPv4 Protocol)
// (003) jeq      #0x6             jt 4	jf 16              # IPv4 Protocol: If TCP, goto #4, #16
// (004) ld       [26]                                     # Load 4B at 26 (source address)
// (005) jeq      #0x7f000001      jt 6	jf 16              # If bytes match(127.0.0.1), goto #6, else #16
// (006) ld       [30]                                     # Load 4B at 30 (dest address)
// (007) jeq      #0x7f000001      jt 8	jf 16              # If bytes match(127.0.0.1), goto #8, else #16
// (008) ldh      [20]                                     # Load 2B at 20 (13b Fragment Offset)
// (009) jset     #0x1fff          jt 16	jf 10          # Use 0x1fff as a mask for fragment offset; If fragment offset != 0, #10, else #16
// (010) ldxb     4*([14]&0xf)                             # x = IP header length
// (011) ldh      [x + 14]                                 # Load 2B at x+14 (TCP Source Port)
// (012) jeq      #0x7b            jt 13	jf 16		   # TCP Source Port: If 123, goto #13, else #16
// (013) ldh      [x + 16]                                 # Load 2B at x+16 (TCP dst port)
// (014) jeq      #0x7c            jt 15	jf 16		   # TCP dst port: If 123, goto #15, else #16
// (015) ret      #262144                                  # MATCH
// (016) ret      #0                                       # NOMATCH

// When capturing return traffic also (i.e., both src -> dst and dst -> src), the filter might look like this:
// 'ip proto 6 and ((src host 10.244.1.2 and dst host 10.244.1.3 and src port 123 and dst port 124) or (src host 10.244.1.3 and dst host 10.244.1.2 and src port 124 and dst port 123))'
// And using `tcpdump -i <device> '<filter>' -d` will generate the following BPF instructions:
// Ethertype, IPv4 protocol...
// (004) ld       [26]									   # Load 4B at 26 (source address)
// (005) jeq      #0xaf40102       jt 6	jf 15			   # If bytes match(10.244.1.2), goto #6, else #15
// (006) ld       [30]									   # Load 4B at 30 (dest address)
// (007) jeq      #0xaf40103       jt 8	jf 26			   # If bytes match(10.244.1.3), goto #8, else #26
// Check fragment offset and calculate IP header length...
// (011) ldh      [x + 14]								   # Load 2B at x+14 (TCP Source Port)
// (012) jeq      #0x7b            jt 13	jf 26		   # TCP Source Port: If 123, goto #13, else #26
// (013) ldh      [x + 16]								   # Load 2B at x+16 (TCP dst port)
// (014) jeq      #0x7c            jt 25	jf 26		   # TCP dst port: If 123, goto #25, else #26
// (015) jeq      #0xaf40103       jt 16	jf 26		   # If bytes match(10.244.1.3), goto #16, else #26
// (016) ld       [30]									   # Load 4B at 30 (return traffic dest address)
// (017) jeq      #0xaf40102       jt 18	jf 26		   # If bytes match(10.244.1.2), goto #18, else #26
// Check fragment offset and calculate IP header length...
// (021) ldh      [x + 14]								   # Load 2B at x+14 (TCP Source Port)
// (022) jeq      #0x7c            jt 23	jf 26		   # TCP Source Port: If 124, goto #23, else #26
// (023) ldh      [x + 16]								   # Load 2B at x+16 (TCP dst port)
// (024) jeq      #0x7b            jt 25	jf 26		   # TCP dst port: If 123, goto #25, else #26
// (025) ret      #262144								   # MATCH
// (026) ret      #0									   # NOMATCH

// For simpler code generation in 'Both' direction, an extra instruction to accept the packet is added after instruction 014.
// The final instruction set looks like this:
// Ethertype, IPv4 protocol...
// Source IP, Destination IP, Source port, Destination port...
// (015) ret      #262144								   # MATCH
// Source IP, Destination IP, Source port, Destination port for return traffic...
// (026) ret      #262144								   # MATCH
// (027) ret      #0									   # NOMATCH

// To capture all TCP packets from 10.0.0.4 to 10.0.0.5 with either SYN or ACK flags set, the filter would be:
// 'ip proto 6 and src host 10.0.0.4 and dst host 10.0.0.5 and ((tcp[tcpflags] & tcp-syn) == tcp-syn) or ((tcp[tcpflags] & tcp-ack) == tcp-ack))'
// And using `tcpdump -i <device> '<filter>' -d` will generate the following BPF instructions:
// Ethertype, IPv4 protocol...
// Source and Destination IP...
// Check fragment offset and calculate IP header length...
// (011) ldh      [x + 27]                                 # Load 1B at x+27 (TCP Flags)
// (012) and	  0x2			            			   # Apply a bitwise AND with 0x2 (SYN flag)
// (013) jeq      #0x2             jt 17    jf 14          # If SYN is set, goto #17, else #14
// (014) ldh      [x + 27]                                 # Again load 1B at x+27 (TCP Flags)
// (015) and	  0x10			            			   # Apply a bitwise AND with 0x10 (ACK flag)
// (016) jeq      #0x10            jt 17    jf 18          # If ACK is set, goto #17, else #18
// (017) ret      #262144                                  # MATCH
// (018) ret      #0                                       # NOMATCH

// To capture ICMP destination unreachable (host unreachable) packets from 10.0.0.1 to 10.0.0.2, the tcpdump filter would be:
// 'ip proto 1 and src host 10.0.0.1 and dst host 10.0.0.2 and icmp[0]=3 and icmp[1]=1'
// And using `tcpdump -i <device> '<filter>' -d` will generate the following BPF instructions:
// Ethertype, IPv4 protocol...
// Source and Destination IP...
// Check fragment offset and calculate IP header length...
// (011) ldb      [x + 14]								   # Load 1B at x+14 (ICMP Type)
// (012) jeq      #0x3             jt 13   jf 16		   # ICMP Type: If 3, goto #13, else #16
// (013) ldb      [x + 15]								   # Load 1B at x+15 (ICMP Code)
// (014) jeq      #0x1             jt 15   jf 16		   # ICMP Code: If 1, goto #15, else #16
// (015) ret      #262144								   # MATCH
// (016) ret      #0									   # NOMATCH

func calculateInstructionsSize(packet *crdv1alpha1.Packet, direction crdv1alpha1.CaptureDirection) int {
	count := 0
	// load ethertype
	count++
	// ip check
	count++

	// src and dst ip
	count += 4

	if packet != nil {
		// protocol check
		if packet.Protocol != nil {
			count += 2
		}
		transport := packet.TransportHeader
		portFiltersSize := func() int {
			count := 0
			if transport.TCP != nil {
				// load Fragment Offset
				count += 3
				if transport.TCP.SrcPort != nil {
					count += 2
				}
				if transport.TCP.DstPort != nil {
					count += 2
				}
				if transport.TCP.Flags != nil {
					// every TCP Flag match condition will have 3 instructions - load, bitwise AND, compare
					count += len(transport.TCP.Flags) * 3
				}

			} else if transport.UDP != nil {
				count += 3
				if transport.UDP.SrcPort != nil {
					count += 2
				}
				if transport.UDP.DstPort != nil {
					count += 2
				}
			} else if transport.ICMP != nil {
				count += 3
				count += 1 // load icmp type
				for _, m := range transport.ICMP.Messages {
					count += 1 // compare icmp type
					if m.Code != nil {
						count += 2 // load + compare icmp code
					}
				}
			}
			return count
		}()

		count += portFiltersSize

		if direction == crdv1alpha1.CaptureDirectionBoth {

			// extra returnKeep
			count++

			// src and dst ip (return traffic)
			count += 3

			count += portFiltersSize

		}
	}

	// ret command
	count += 2
	return count

}
