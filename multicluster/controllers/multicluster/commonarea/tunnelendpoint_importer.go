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

package commonarea

import (
	"context"
	"encoding/json"
	"reflect"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
)

func (r *ResourceImportReconciler) handleResImpForTunnelEndpoint(ctx context.Context, req ctrl.Request, resImp *mcsv1alpha1.ResourceImport) (ctrl.Result, error) {
	klog.InfoS("Reconciling TunnelEndpoint of ResourceImport", "resourceimport", req.NamespacedName)
	// if TunnelEndpoint source is local cluster, skip it.
	var teSpec mcsv1alpha1.TunnelEndpointSpec
	var err error
	if err = json.Unmarshal(resImp.Spec.Raw.Data, &teSpec); err != nil {
		return ctrl.Result{}, err
	}
	if teSpec.ClusterID == r.localClusterID {
		klog.InfoS("Skip reconciling TunnelEndpoint kind of ResourceImport since it's from local cluster", "resourceimport", req.NamespacedName)
		return ctrl.Result{}, nil
	}

	// Create or update TunnelEndpointImport
	teImport, teImportNamespaced := getTEImport(req.Name, r.namespace)
	if err = r.localClusterClient.Get(ctx, teImportNamespaced, teImport); err != nil {
		if apierrors.IsNotFound(err) {
			teImport.Spec = teSpec
			if err = r.localClusterClient.Create(ctx, teImport, &client.CreateOptions{}); err != nil {
				return ctrl.Result{}, err
			}
			r.installedResImports.Add(*resImp)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	if reflect.DeepEqual(teImport.Spec, teSpec) {
		klog.InfoS("no change on TunnelEndpoitImport spec, skip reconciling", "tunnelendpointimport", teImportNamespaced.String(),
			"resourceimport", req.NamespacedName.String())
		return ctrl.Result{}, nil
	}
	teImport.Spec = teSpec
	if err = r.localClusterClient.Update(ctx, teImport, &client.UpdateOptions{}); err != nil {
		return ctrl.Result{}, err
	}
	r.installedResImports.Update(*resImp)
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpDeleteForTunnelEndpoint(ctx context.Context, req ctrl.Request, resImp *mcsv1alpha1.ResourceImport) (ctrl.Result, error) {
	teImport, teImportNamespaced := getTEImport(req.Name, r.namespace)
	if err := r.localClusterClient.Delete(ctx, teImport, &client.DeleteOptions{}); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	klog.InfoS("TunnelEndpointImport is deleted", "tunnelendpointimport", teImportNamespaced.String())
	return ctrl.Result{}, nil
}

func getTEImport(name, namespace string) (*mcsv1alpha1.TunnelEndpointImport, types.NamespacedName) {
	teImport := &mcsv1alpha1.TunnelEndpointImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}

	teImportNamespaced := types.NamespacedName{Name: name, Namespace: namespace}
	return teImport, teImportNamespaced
}
