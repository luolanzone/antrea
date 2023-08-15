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

package member

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/pkg/apis/crd/v1beta1"
	k8smcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
)

func TestCleanUpMCServiceAndServiceImport(t *testing.T) {
	existingSVCs := &corev1.ServiceList{
		Items: []corev1.Service{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "svc-a",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "antrea-mc-svc-b",
				},
			},
		},
	}
	existingSVCImports := &k8smcsv1alpha1.ServiceImportList{
		Items: []k8smcsv1alpha1.ServiceImport{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "svc-b",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithLists(existingSVCImports, existingSVCs).Build()
	ctx := context.Background()
	err := cleanUpMCServiceAndServiceImport(ctx, fakeClient)
	require.NoError(t, err)
	actualSvcList := &corev1.ServiceList{}
	err = fakeClient.List(ctx, actualSvcList)
	require.NoError(t, err)
	assert.Equal(t, 1, len(actualSvcList.Items))

	actualSvcImpList := &k8smcsv1alpha1.ServiceImportList{}
	err = fakeClient.List(ctx, actualSvcImpList)
	require.NoError(t, err)
	assert.Equal(t, 0, len(actualSvcImpList.Items))
}

func TestCleanUpReplicatedACNP(t *testing.T) {
	acnpList := &v1beta1.ClusterNetworkPolicyList{
		Items: []v1beta1.ClusterNetworkPolicy{
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "acnp-1",
					Annotations: map[string]string{
						common.AntreaMCACNPAnnotation: "true",
					},
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Name: "acnp-2",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithLists(acnpList).Build()
	ctx := context.Background()
	err := cleanUpReplicatedACNP(ctx, fakeClient)
	require.NoError(t, err)

	actualACNPList := &v1beta1.ClusterNetworkPolicyList{}
	err = fakeClient.List(ctx, actualACNPList, &client.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, 1, len(actualACNPList.Items))
}

func TestCleanuUpLabelIdentities(t *testing.T) {
	labelIdentityList := &mcv1alpha1.LabelIdentityList{
		Items: []mcv1alpha1.LabelIdentity{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "labelidt-1",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "labelidt-2",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithLists(labelIdentityList).Build()
	ctx := context.Background()
	err := cleanUpLabelIdentities(ctx, fakeClient)
	require.NoError(t, err)
	actualIdtList := &mcv1alpha1.LabelIdentityList{}
	err = fakeClient.List(ctx, actualIdtList, &client.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, len(actualIdtList.Items))
}

func TestCleanUpClusterInfoImport(t *testing.T) {
	ciImpList := &mcv1alpha1.ClusterInfoImportList{
		Items: []mcv1alpha1.ClusterInfoImport{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "cluster-1-import",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "cluster-2-import",
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithLists(ciImpList).Build()
	ctx := context.Background()
	err := cleanUpClusterInfoImport(ctx, fakeClient)
	require.NoError(t, err)
	actualCIImpList := &mcv1alpha1.ClusterInfoImportList{}
	err = fakeClient.List(ctx, actualCIImpList, &client.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, len(actualCIImpList.Items))
}
