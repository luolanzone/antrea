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

type featureMulticluster struct {
	cookieAllocator cookie.Allocator
	gwNodeFlowCache *flowCategoryCache
	category        cookie.Category
	ipProtocols     []binding.Protocol
}

func (c *featureMulticluster) getFeatureName() featureName {
	return Multicluster
}

func newFeatureMulticluster(cookieAllocator cookie.Allocator, ipProtocols []binding.Protocol) *featureMulticluster {
	return &featureMulticluster{
		cookieAllocator: cookieAllocator,
		gwNodeFlowCache: newFlowCategoryCache(),
		category:        cookie.Multicluster,
		ipProtocols:     ipProtocols,
	}
}

func (c *featureMulticluster) initFlows(category cookie.Category) []binding.Flow {
	return []binding.Flow{}
}

func (c *featureMulticluster) replayFlows() []binding.Flow {
	return []binding.Flow{}
}
