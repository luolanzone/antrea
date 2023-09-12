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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
)

func cleanUpResourceExports(ctx context.Context, mgrClient client.Client) error {
	resExports := mcv1alpha1.ResourceExportList{}
	err := mgrClient.List(ctx, &resExports, &client.ListOptions{})
	if err != nil {
		return err
	}
	for _, resExport := range resExports.Items {
		resExpTmp := resExport
		err := mgrClient.Delete(ctx, &resExpTmp, &client.DeleteOptions{})
		if err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}
