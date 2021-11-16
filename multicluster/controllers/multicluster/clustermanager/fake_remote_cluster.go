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

package clustermanager

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	"antrea.io/antrea/multicluster/controllers/multicluster/common"
)

// fakeRemoteCluster is a fake RemoteCluster for unit test purpose
type fakeRemoteCluster struct {
	client.Client
	ClusterID common.ClusterID
	Namespace string
}

func (c *fakeRemoteCluster) GetClusterID() common.ClusterID {
	return c.ClusterID
}

func (c *fakeRemoteCluster) GetNamespace() string {
	return c.Namespace
}

func (c *fakeRemoteCluster) Start() (context.CancelFunc, error) {
	_, stopFunc := context.WithCancel(context.Background())
	return stopFunc, nil
}

func (c *fakeRemoteCluster) Stop() error {
	return nil
}

func (c *fakeRemoteCluster) IsConnected() bool {
	return true
}

func (c *fakeRemoteCluster) StartMonitoring() error {
	return nil
}

// NewFakeRemoteCluster creates a new fakeRemoteCluster for unit test purpose only
func NewFakeRemoteCluster(scheme *runtime.Scheme,
	remoteClusterManager *RemoteClusterManager,
	fakeClient client.Client, clusterID string, namespace string) RemoteCluster {
	fakeRemoteCluster := &fakeRemoteCluster{
		Client:    fakeClient,
		ClusterID: common.ClusterID(clusterID),
		Namespace: namespace,
	}
	(*remoteClusterManager).AddRemoteCluster(fakeRemoteCluster)
	return fakeRemoteCluster
}
