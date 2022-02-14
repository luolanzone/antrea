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
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	binding "antrea.io/antrea/pkg/ovs/openflow"
)

type ipStack int

const (
	ipv4Only ipStack = iota
	ipv6Only
	dualStack
)

func TestBuildPipeline(t *testing.T) {
	ipStackMap := map[ipStack][]binding.Protocol{
		ipv4Only:  {binding.ProtocolIP},
		ipv6Only:  {binding.ProtocolIPv6},
		dualStack: {binding.ProtocolIP, binding.ProtocolIPv6},
	}
	for _, tc := range []struct {
		ipStack        ipStack
		features       []feature
		expectedTables map[pipeline][]*FeatureTable
	}{
		{
			ipStack: dualStack,
			features: []feature{
				&featurePodConnectivity{ipProtocols: ipStackMap[dualStack]},
				&featureNetworkPolicy{enableAntreaPolicy: true},
				&featureService{enableProxy: true, proxyAll: true},
				&featureEgress{},
			},
			expectedTables: map[pipeline][]*FeatureTable{
				pipelineIP: {
					ClassifierTable,
					SpoofGuardTable,
					IPv6Table,
					SNATConntrackTable,
					ConntrackTable,
					ConntrackStateTable,
					PreRoutingClassifierTable,
					NodePortProbeTable,
					SessionAffinityTable,
					ServiceLBTable,
					EndpointDNATTable,
					AntreaPolicyEgressRuleTable,
					EgressRuleTable,
					EgressDefaultTable,
					EgressMetricTable,
					L3ForwardingTable,
					ServiceHairpinMarkTable,
					L3DecTTLTable,
					SNATTable,
					SNATConntrackCommitTable,
					L2ForwardingCalcTable,
					AntreaPolicyIngressRuleTable,
					IngressRuleTable,
					IngressDefaultTable,
					IngressMetricTable,
					ConntrackCommitTable,
					L2ForwardingOutTable,
				},
				pipelineARP: {
					ARPSpoofGuardTable,
					ARPResponderTable,
				},
			},
		},
		{
			ipStack: ipv6Only,
			features: []feature{
				&featurePodConnectivity{ipProtocols: ipStackMap[ipv6Only]},
				&featureNetworkPolicy{enableAntreaPolicy: true},
				&featureService{enableProxy: true, proxyAll: true},
				&featureEgress{},
			},
			expectedTables: map[pipeline][]*FeatureTable{
				pipelineIP: {
					ClassifierTable,
					SpoofGuardTable,
					IPv6Table,
					SNATConntrackTable,
					ConntrackTable,
					ConntrackStateTable,
					PreRoutingClassifierTable,
					NodePortProbeTable,
					SessionAffinityTable,
					ServiceLBTable,
					EndpointDNATTable,
					AntreaPolicyEgressRuleTable,
					EgressRuleTable,
					EgressDefaultTable,
					EgressMetricTable,
					L3ForwardingTable,
					ServiceHairpinMarkTable,
					L3DecTTLTable,
					SNATTable,
					SNATConntrackCommitTable,
					L2ForwardingCalcTable,
					AntreaPolicyIngressRuleTable,
					IngressRuleTable,
					IngressDefaultTable,
					IngressMetricTable,
					ConntrackCommitTable,
					L2ForwardingOutTable,
				},
			},
		},
		{
			ipStack: ipv4Only,
			features: []feature{
				&featurePodConnectivity{ipProtocols: ipStackMap[ipv4Only]},
				&featureNetworkPolicy{enableAntreaPolicy: true},
				&featureService{enableProxy: false},
				&featureEgress{},
			},
			expectedTables: map[pipeline][]*FeatureTable{
				pipelineIP: {
					ClassifierTable,
					SpoofGuardTable,
					ConntrackTable,
					ConntrackStateTable,
					DNATTable,
					AntreaPolicyEgressRuleTable,
					EgressRuleTable,
					EgressDefaultTable,
					EgressMetricTable,
					L3ForwardingTable,
					L3DecTTLTable,
					SNATTable,
					L2ForwardingCalcTable,
					AntreaPolicyIngressRuleTable,
					IngressRuleTable,
					IngressDefaultTable,
					IngressMetricTable,
					ConntrackCommitTable,
					L2ForwardingOutTable,
				},
				pipelineARP: {
					ARPSpoofGuardTable,
					ARPResponderTable,
				},
			},
		},
		{
			ipStack: ipv4Only,
			features: []feature{
				&featurePodConnectivity{ipProtocols: ipStackMap[ipv4Only]},
				&featureNetworkPolicy{enableAntreaPolicy: true},
				&featureService{enableProxy: true, proxyAll: false},
				&featureEgress{},
			},
			expectedTables: map[pipeline][]*FeatureTable{
				pipelineIP: {
					ClassifierTable,
					SpoofGuardTable,
					SNATConntrackTable,
					ConntrackTable,
					ConntrackStateTable,
					PreRoutingClassifierTable,
					SessionAffinityTable,
					ServiceLBTable,
					EndpointDNATTable,
					AntreaPolicyEgressRuleTable,
					EgressRuleTable,
					EgressDefaultTable,
					EgressMetricTable,
					L3ForwardingTable,
					ServiceHairpinMarkTable,
					L3DecTTLTable,
					SNATTable,
					SNATConntrackCommitTable,
					L2ForwardingCalcTable,
					AntreaPolicyIngressRuleTable,
					IngressRuleTable,
					IngressDefaultTable,
					IngressMetricTable,
					ConntrackCommitTable,
					L2ForwardingOutTable,
				},
				pipelineARP: {
					ARPSpoofGuardTable,
					ARPResponderTable,
				},
			},
		},
	} {
		templatesMap := make(map[pipeline][]*featureTemplate)
		for _, f := range tc.features {
			templatesMap[pipelineIP] = append(templatesMap[pipelineIP], f.getTemplate(pipelineIP))
			if tc.ipStack != ipv6Only {
				template := f.getTemplate(pipelineARP)
				if template != nil {
					templatesMap[pipelineARP] = append(templatesMap[pipelineARP], template)
				}
			}
		}

		for proto, templates := range templatesMap {
			generatePipeline(templates)
			tables := tc.expectedTables[proto]

			for i := 0; i < len(tables)-1; i++ {
				require.NotNil(t, tables[i].ofTable, "table %q should be initialized", tables[i].name)
				require.Less(t, tables[i].GetID(), tables[i+1].GetID(), fmt.Sprintf("id of table %q should less than that of table %q", tables[i].GetName(), tables[i+1].GetName()))
			}
			require.NotNil(t, tables[len(tables)-1].ofTable, "table %q should be initialized", tables[len(tables)-1].name)

			reset(tables)
		}
	}
}

func reset(tables []*FeatureTable) {
	PipelineClassifierTable.ofTable = nil
	for _, table := range tables {
		table.ofTable = nil
	}
	binding.ResetTableID()
}
