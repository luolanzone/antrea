/*
Copyright 2022 Antrea Authors.

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
	"bytes"
	"context"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
)

func (r *ResourceExportReconciler) handleTunnelEndpoint(ctx context.Context, req ctrl.Request, resExport mcsv1alpha1.ResourceExport) (ctrl.Result, error) {
	resImport := &mcsv1alpha1.ResourceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}

	if !resExport.DeletionTimestamp.IsZero() {
		if common.StringExistsInSlice(resExport.Finalizers, common.ResourceExportFinalizer) {
			if err := r.Client.Delete(ctx, resImport, &client.DeleteOptions{}); err != nil {
				return ctrl.Result{}, err
			}
			return r.deleteResourceExport(resExport)
		}
		return ctrl.Result{}, nil
	}

	resImport.Spec = mcsv1alpha1.ResourceImportSpec{
		Kind:      common.TunnelEndpointKind,
		Name:      resExport.Name,
		Namespace: resExport.Namespace,
	}
	resImportName := types.NamespacedName{
		Name:      req.Name,
		Namespace: req.Namespace,
	}

	var err error
	if err = r.Client.Get(ctx, resImportName, resImport); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// Create a new TunnelEndpoint of ResourceImport
		resImport.Spec.Raw = &mcsv1alpha1.RawResourceImport{}
		resImport.Spec.Raw.Data = resExport.Spec.Raw.Data
		if err = r.Client.Create(ctx, resImport, &client.CreateOptions{}); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	// Update an existing TunnelEndpoint of ResourceImport
	if res := bytes.Compare(resImport.Spec.Raw.Data, resExport.Spec.Raw.Data); res == 0 {
		klog.InfoS("no data change from ResourceExport, skip reconciling", "resourceexport", klog.KObj(&resExport))
		return ctrl.Result{}, nil
	}
	resImport.Spec.Raw.Data = resExport.Spec.Raw.Data
	if err = r.Client.Update(ctx, resImport, &client.UpdateOptions{}); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}
