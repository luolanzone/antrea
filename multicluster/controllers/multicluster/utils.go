/*
Copyright 2021 Antrea Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package multicluster

import (
	"errors"

	"antrea.io/antrea/multicluster/controllers/multicluster/internal"
)

// we should only have one remote leader cluster for demo purpose
// so check and return the first remote cluster.
func getRemoteCluster(remoteMgr *internal.RemoteClusterManager) (internal.RemoteCluster, error) {
	var remoteCluster internal.RemoteCluster
	remoteClusters := (*remoteMgr).GetRemoteClusters()
	if len(remoteClusters) <= 0 {
		return nil, errors.New("clusterset has not been initialized properly, no remote cluster manager")
	}

	for _, c := range remoteClusters {
		remoteCluster = c
		break
	}
	return remoteCluster, nil
}
