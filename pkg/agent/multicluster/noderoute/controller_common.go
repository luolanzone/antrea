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
	"time"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	mclisters "antrea.io/antrea/multicluster/pkg/client/listers/multicluster/v1alpha1"
	"github.com/stretchr/testify/assert"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
)

const (
	// Interval of reprocessing every TunnelEndpoint or TunnelEndpointImport.
	resyncPeriod = 30 * time.Second
	// How long to wait before retrying the processing of a resource change
	minRetryDelay = 2 * time.Second
	maxRetryDelay = 120 * time.Second

	// Default number of workers processing a resource change
	defaultWorkers = 1

	tunnelEndpointInfoSubnetsIndexName  = "teSubnets"
	tunnelEndpointImportPeerIPIndexName = "teiPeerIP"
)

func checkTunnelEndpoint(obj interface{}) (*mcv1alpha1.TunnelEndpoint, error) {
	te, isTE := obj.(*mcv1alpha1.TunnelEndpoint)
	if !isTE {
		deletedState, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return nil, fmt.Errorf("received unexpected object: %v", obj)
		}
		te, ok = deletedState.Obj.(*mcv1alpha1.TunnelEndpoint)
		if !ok {
			return nil, fmt.Errorf("DeletedFinalStateUnknown contains non-TunnelEndpoint object: %v", deletedState.Obj)
		}
	}

	if !(te.Spec.PrivateIP != "" || te.Spec.PublicIP != "") {
		return te, fmt.Errorf("no valid Gateway Node IP is found in TunnelEndpoint %s", te.Namespace+"/"+te.Name)
	}
	return te, nil
}

func checkTunnelEndpointImport(obj interface{}) (*mcv1alpha1.TunnelEndpointImport, error) {
	tei, isTEI := obj.(*mcv1alpha1.TunnelEndpointImport)
	if !isTEI {
		deletedState, ok := obj.(cache.DeletedFinalStateUnknown)
		if !ok {
			return nil, fmt.Errorf("received unexpected object: %v", obj)
		}
		tei, ok = deletedState.Obj.(*mcv1alpha1.TunnelEndpointImport)
		if !ok {
			return nil, fmt.Errorf("DeletedFinalStateUnknown contains non-TunnelEndpointImport object: %v", deletedState.Obj)
		}
	}
	return tei, nil
}

func getPeerNodeIP(spec mcv1alpha1.TunnelEndpointSpec) string {
	var peerNodeIP string
	if spec.PrivateIP != "" {
		peerNodeIP = spec.PrivateIP
		return peerNodeIP
	}
	if spec.PublicIP != "" {
		peerNodeIP = spec.PublicIP
		return peerNodeIP
	}
	return ""
}

func getHostnameWithPrefix(hostname string) string {
	return "mc-" + hostname
}

func getValidTunnelEndpoint(teLister mclisters.TunnelEndpointLister, namespace string) (*mcv1alpha1.TunnelEndpoint, error) {
	telist, err := teLister.TunnelEndpoints(namespace).List(labels.Everything())
	if err != nil {
		klog.ErrorS(err, "Failed to get TunnelEndpoint list", "namespace", namespace)
		return nil, err
	}
	if len(telist) == 0 {
		return nil, fmt.Errorf("no TunnelEndpoint found in %s namespace", namespace)
	}
	var te *mcv1alpha1.TunnelEndpoint
	for _, t := range telist {
		if t.Spec.Role == "leader" {
			te = t
			break
		}
	}
	if te == nil {
		return nil, fmt.Errorf("no leader role of TunnelEndpoint found in %s namespace", namespace)
	}
	return te, nil
}

type dummyT struct{}

func (t dummyT) Errorf(string, ...interface{}) {}

// elementsMatch compares array ignoring the order of elements.
func elementsMatch(listA, listB interface{}) bool {
	return assert.ElementsMatch(dummyT{}, listA, listB)
}
