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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
)

func TestCleanUpResourceExports(t *testing.T) {
	resExports := &mcv1alpha1.ResourceExportList{
		Items: []mcv1alpha1.ResourceExport{
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "acnp",
				},
			},
			{
				ObjectMeta: metav1.ObjectMeta{
					Namespace: "default",
					Name:      "svc-1",
				},
			},
		},
	}
	scheme := runtime.NewScheme()
	mcv1alpha1.AddToScheme(scheme)
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithLists(resExports).Build()
	ctx := context.Background()
	err := cleanUpResourceExports(ctx, fakeClient)
	require.NoError(t, err)

	resExportList := &mcv1alpha1.ResourceExportList{}
	err = fakeClient.List(ctx, resExportList, &client.ListOptions{})
	require.NoError(t, err)
	assert.Equal(t, 0, len(resExportList.Items))
}
