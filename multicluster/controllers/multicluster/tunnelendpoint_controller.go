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
	"context"
	"encoding/json"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/multicluster/controllers/multicluster/commonarea"
)

type (
	// TunnelEndpointReconciler is for member cluster only.
	TunnelEndpointReconciler struct {
		client.Client
		Scheme                  *runtime.Scheme
		remoteCommonAreaManager *commonarea.RemoteCommonAreaManager
		namespace               string
		localClusterID          string
		installedTE             cache.Indexer
		leaderNamespace         string
	}
)

// TunnelEndpointReconciler will watch TunnelEndpoint events and create a Raw ResourceExport
// in the leader cluster.
func NewTunnelEndpointReconciler(
	Client client.Client,
	Scheme *runtime.Scheme,
	namespace string,
	remoteCommonAreaManager *commonarea.RemoteCommonAreaManager) *TunnelEndpointReconciler {
	reconciler := &TunnelEndpointReconciler{
		Client:                  Client,
		Scheme:                  Scheme,
		namespace:               namespace,
		remoteCommonAreaManager: remoteCommonAreaManager,
		installedTE: cache.NewIndexer(tunnelEndpointKeyFunc, cache.Indexers{
			teIndexerBySubnets: tunnelEndpointIndexerBySubnetsFunc,
		}),
	}
	return reconciler
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints/finalizers,verbs=update

func (r *TunnelEndpointReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	if *r.remoteCommonAreaManager == nil {
		klog.InfoS("clusterset has not been initialized properly, no remote cluster manager")
		return ctrl.Result{Requeue: true}, nil
	}
	r.localClusterID = string((*r.remoteCommonAreaManager).GetLocalClusterID())
	if len(r.localClusterID) == 0 {
		klog.InfoS("localClusterID is not initialized, skip reconcile")
		return ctrl.Result{Requeue: true}, nil
	}

	remoteCluster, err := getRemoteCommonArea(r.remoteCommonAreaManager)
	if err != nil {
		return ctrl.Result{}, err
	}

	r.leaderNamespace = remoteCluster.GetNamespace()

	te := &mcsv1alpha1.TunnelEndpoint{}
	if err := r.Client.Get(ctx, req.NamespacedName, te); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	resExportName := r.localClusterID + "-" + r.namespace + "-" + te.Name + "-" + "tunnelendpoint"
	resExport := &mcsv1alpha1.ResourceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resExportName,
			Namespace: r.leaderNamespace,
		},
	}
	if !te.DeletionTimestamp.IsZero() {
		if common.StringExistsInSlice(te.Finalizers, common.TunnelEndpointFinalizer) {
			if err := remoteCluster.Delete(ctx, resExport, &client.DeleteOptions{}); err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			te.SetFinalizers(common.RemoveStringFromSlice(te.Finalizers, common.TunnelEndpointFinalizer))
			if err := r.Client.Update(ctx, te, &client.UpdateOptions{}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}
	existResExport := &mcsv1alpha1.ResourceExport{}
	if err := remoteCluster.Get(ctx, types.NamespacedName{Namespace: r.leaderNamespace, Name: resExportName}, existResExport); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		if err = r.createorUpdateResourceExport(ctx, remoteCluster, existResExport, te, true); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	if err = r.createorUpdateResourceExport(ctx, remoteCluster, existResExport, te, false); err != nil {
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *TunnelEndpointReconciler) createorUpdateResourceExport(ctx context.Context,
	remoteCluster commonarea.RemoteCommonArea, existResExport *mcsv1alpha1.ResourceExport, te *mcsv1alpha1.TunnelEndpoint, creation bool) error {
	data, err := json.Marshal(te.Spec)
	if err != nil {
		return err
	}
	resExportSpec := mcsv1alpha1.ResourceExportSpec{
		Kind:      "TunnelEndpoint",
		ClusterID: r.localClusterID,
		Name:      te.Name,
		Namespace: te.Namespace,
		Raw: mcsv1alpha1.RawResourceExport{
			Data: data,
		},
	}

	if creation {
		resExport := &mcsv1alpha1.ResourceExport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: r.leaderNamespace,
				Name:      r.localClusterID + "-" + r.namespace + "-" + te.Name + "-" + "tunnelendpoint",
			},
			Spec: resExportSpec,
		}
		resExport.Finalizers = []string{common.ResourceExportFinalizer}
		if err = remoteCluster.Create(ctx, resExport, &client.CreateOptions{}); err != nil {
			return err
		}
		return nil
	}
	existResExport.Spec = resExportSpec
	if err = remoteCluster.Update(ctx, existResExport, &client.UpdateOptions{}); err != nil {
		return err
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *TunnelEndpointReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mcsv1alpha1.TunnelEndpoint{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
