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
	"antrea.io/antrea/pkg/agent/openflow/cookie"
	binding "antrea.io/antrea/pkg/ovs/openflow"
)

type featureMulticast struct {
	cookieAllocator cookie.Allocator
	ipProtocols     []binding.Protocol
	mcastFlowCache  *flowCategoryCache
}

func (c *featureMulticast) getFeatureID() featureID {
	return Multicast
}

func newFeatureMulticast(cookieAllocator cookie.Allocator, ipProtocols []binding.Protocol) *featureMulticast {
	return &featureMulticast{
		cookieAllocator: cookieAllocator,
		ipProtocols:     ipProtocols,
		mcastFlowCache:  newFlowCategoryCache(),
	}
}

func multicastPipelineClassifyFlow(cookieID uint64, pipeline binding.Pipeline) binding.Flow {
	targetTable := pipeline.GetFirstTable()
	return MulticastClassifierTable.ofTable.BuildFlow(priorityHigh).
		Cookie(cookieID).
		MatchProtocol(binding.ProtocolIP).
		MatchDstIPNet(*mcastCIDR).
		Action().ResubmitToTables(targetTable.GetID()).
		Done()
}