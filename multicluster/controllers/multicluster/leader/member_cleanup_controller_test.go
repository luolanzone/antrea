/*
Copyright 2023 Antrea Authors.

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

package leader

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/commonarea"
)

func TestCleanUpStaleResourceExports(t *testing.T) {
	now := time.Now().Format(time.RFC3339)
	oldTime := time.Now().Add(-1).Format(time.RFC3339)
	mcaList := &mcv1alpha1.MemberClusterAnnounceList{
		Items: []mcv1alpha1.MemberClusterAnnounce{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "member-announce-from-cluster-a",
					Namespace: "default",
					Annotations: map[string]string{
						commonarea.TimestampAnnotationKey: now,
					},
				},
				ClusterID: "cluster-a",
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "member-announce-from-cluster-b",
					Namespace: "default",
					Annotations: map[string]string{
						commonarea.TimestampAnnotationKey: oldTime,
					},
				},
				ClusterID: "cluster-b",
			},
		},
	}

	resExports := &mcv1alpha1.ResourceExportList{
		Items: []mcv1alpha1.ResourceExport{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "cluster-a-svc",
				},
				Spec: mcv1alpha1.ResourceExportSpec{
					ClusterID: "cluster-a",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "cluster-b-svc",
				},
				Spec: mcv1alpha1.ResourceExportSpec{
					ClusterID: "cluster-b",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "cluster-c-svc",
				},
				Spec: mcv1alpha1.ResourceExportSpec{
					ClusterID: "cluster-c",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "leader-acnp",
				},
				Spec: mcv1alpha1.ResourceExportSpec{},
			},
		},
	}

	scheme := runtime.NewScheme()
	mcv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithLists(mcaList, resExports).Build()
	c := NewMemberResourcesCleanupController(fakeClient, scheme, "default")
	ctx := context.Background()
	c.cleanUpStaleResourceExports(ctx)
	latestResExports := &mcv1alpha1.ResourceExportList{}
	err := fakeClient.List(ctx, latestResExports)
	require.NoError(t, err)
	assert.Equal(t, 3, len(latestResExports.Items))
}

func TestReconcile(t *testing.T) {
	resExport1 := mcv1alpha1.ResourceExport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cluster-1-svc",
		},
		Spec: mcv1alpha1.ResourceExportSpec{
			ClusterID: "cluster-1",
		},
	}
	resExport2 := mcv1alpha1.ResourceExport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cluster-1-ep",
		},
		Spec: mcv1alpha1.ResourceExportSpec{
			ClusterID: "cluster-1",
		},
	}
	resExport3 := mcv1alpha1.ResourceExport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "cluster-2-ep",
		},
		Spec: mcv1alpha1.ResourceExportSpec{
			ClusterID: "cluster-2",
		},
	}
	resExportsList := &mcv1alpha1.ResourceExportList{
		Items: []mcv1alpha1.ResourceExport{
			resExport1,
			resExport2,
			resExport3,
		},
	}
	scheme := runtime.NewScheme()
	mcv1alpha1.AddToScheme(scheme)
	memberClusterAnnounce1 := &mcv1alpha1.MemberClusterAnnounce{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "member-announce-from-cluster-1",
			Namespace: "default",
		},
	}

	tests := []struct {
		name                          string
		memberAnnounceName            string
		expectedResExportsSize        int
		expectedErr                   error
		existingMemberAnnounce        *mcv1alpha1.MemberClusterAnnounce
		existingResExports            *mcv1alpha1.ResourceExportList
		getResourceExportsByClusterID func(c *MemberResourcesCleanupController, ctx context.Context, clusterID string) ([]mcv1alpha1.ResourceExport, error)
	}{
		{
			name:                   "MemberClusterAnnounce exists",
			memberAnnounceName:     memberClusterAnnounce1.Name,
			existingMemberAnnounce: memberClusterAnnounce1,
			existingResExports:     resExportsList,
			expectedResExportsSize: 3,
		},
		{
			name:                   "MemberClusterAnnounce deleted",
			memberAnnounceName:     memberClusterAnnounce1.Name,
			existingMemberAnnounce: &mcv1alpha1.MemberClusterAnnounce{},
			existingResExports:     resExportsList,
			getResourceExportsByClusterID: func(c *MemberResourcesCleanupController, ctx context.Context, clusterID string) ([]mcv1alpha1.ResourceExport, error) {
				return []mcv1alpha1.ResourceExport{resExport1, resExport2}, nil
			},
			expectedResExportsSize: 1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			getResourceExportsByClusterIDFunc = tt.getResourceExportsByClusterID
			defer func() {
				getResourceExportsByClusterIDFunc = getResourceExportsByClusterID
			}()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithLists(tt.existingResExports).WithObjects(tt.existingMemberAnnounce).Build()
			c := NewMemberResourcesCleanupController(fakeClient, scheme, "default")
			ctx := context.Background()
			_, err := c.Reconcile(ctx, ctrl.Request{
				NamespacedName: types.NamespacedName{
					Namespace: "default",
					Name:      tt.memberAnnounceName,
				},
			})
			if tt.expectedErr == nil {
				require.NoError(t, err)
			} else {
				assert.Equal(t, tt.expectedErr, err.Error())
			}
			latestResExports := &mcv1alpha1.ResourceExportList{}
			err = fakeClient.List(ctx, latestResExports)
			require.NoError(t, err)
			assert.Equal(t, tt.expectedResExportsSize, len(latestResExports.Items))
		})
	}
}
