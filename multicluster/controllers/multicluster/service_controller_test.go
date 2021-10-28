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
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/multicluster/controllers/multicluster/internal"
)

func TestServiceReconciler_Reconcile(t *testing.T) {
	localClusterID = "cluster-a"
	localNamespace := "default"
	remoteMgr := internal.NewRemoteClusterManager("test-clusterset", Log, common.ClusterID(localClusterID))
	remoteMgr.Start()

	newSvcNginx := svcNginx.DeepCopy()
	newSvcNginx.Labels = map[string]string{common.SourceImportLabel: "default-nginx-service"}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(newSvcNginx).Build()
	fakeRemoteClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	_ = internal.NewFakeRemoteCluster(scheme, &remoteMgr, fakeRemoteClient, "leader-cluster", "default")
	svcNamespaced1 := types.NamespacedName{
		Namespace: localNamespace,
		Name:      "nginx-one",
	}
	svcNamespaced2 := types.NamespacedName{
		Namespace: localNamespace,
		Name:      "nginx",
	}
	tests := []struct {
		name       string
		req        ctrl.Request
		namespaced types.NamespacedName
		wantErr    bool
	}{
		{
			name:       "service not found",
			namespaced: svcNamespaced1,
			req:        ctrl.Request{NamespacedName: svcNamespaced1},
			wantErr:    false,
		},
		{
			name:       "remove existing Service because no corresponding ResourceImport",
			namespaced: svcNamespaced2,
			req:        ctrl.Request{NamespacedName: svcNamespaced2},
			wantErr:    false,
		},
	}
	r := NewServiceReconciler(fakeClient, scheme, &remoteMgr)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.Reconcile(ctx, tt.req); err != nil {
				t.Errorf("Service Reconciler should handle delete event successfully but got error = %v", err)
			} else {
				svc := &corev1.Service{}
				if err = fakeClient.Get(ctx, tt.req.NamespacedName, svc); !apierrors.IsNotFound(err) {
					t.Errorf("expected not found error but got error = %v", err)
				}
			}
		})
	}
}
