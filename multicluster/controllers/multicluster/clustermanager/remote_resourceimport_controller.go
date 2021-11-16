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
	"errors"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	multiclusterv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
)

const (
	// cached indexer
	resImportIndexerByNameKind = "resourceimport.by.namekind"
)

func resImportIndexerByNameKindFunc(obj interface{}) ([]string, error) {
	ri := obj.(multiclusterv1alpha1.ResourceImport)
	return []string{ri.Spec.Namespace + ri.Spec.Name + ri.Spec.Kind}, nil
}

func resImportIndexerKeyFunc(obj interface{}) (string, error) {
	ri := obj.(multiclusterv1alpha1.ResourceImport)
	return common.NamespacedName(ri.Namespace, ri.Name), nil
}

// ResourceImportReconciler reconciles a ResourceImport object
type ResourceImportReconciler struct {
	client.Client
	Scheme              *runtime.Scheme
	localClusterManager *LocalClusterManager
	remoteCluster       RemoteCluster
	installedResImports cache.Indexer
}

func NewResourceImportReconciler(client client.Client, scheme *runtime.Scheme, localClusterMgr *LocalClusterManager, remoteCluster RemoteCluster) *ResourceImportReconciler {
	return &ResourceImportReconciler{
		Client:              client,
		Scheme:              scheme,
		localClusterManager: localClusterMgr,
		remoteCluster:       remoteCluster,
		installedResImports: cache.NewIndexer(resImportIndexerKeyFunc, cache.Indexers{
			resImportIndexerByNameKind: resImportIndexerByNameKindFunc,
		}),
	}
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceimports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceimports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceimports/finalizers,verbs=update
//+kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch;update;create;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update;create;patch;delete

// Reconcile will attempt to ensure that the imported Resource is installed in local cluster as per the
// ResourceImport object.
func (r *ResourceImportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(2).InfoS("reconciling ResourceImport", "resourceimport", req.NamespacedName)
	// TODO: Must check whether this ResourceImport must be reconciled by this member cluster. Check `spec.clusters` field.
	if r.localClusterManager == nil {
		return ctrl.Result{}, errors.New("localClusterMgr has not been initialized properly, no local cluster manager")
	}

	if r.remoteCluster == nil {
		return ctrl.Result{}, errors.New("remoteCluster has not been initialized properly, no remote cluster manager")
	}

	var resImp multiclusterv1alpha1.ResourceImport
	err := r.remoteCluster.Get(ctx, req.NamespacedName, &resImp)
	var isDeleted bool
	if err != nil {
		isDeleted = apierrors.IsNotFound(err)
		if !isDeleted {
			klog.InfoS("unable to fetch ResourceImport", "resourceimport", req.NamespacedName.String(), "err", err)
			return ctrl.Result{}, err
		}
	}

	if isDeleted {
		resImpObj, exist, err := r.installedResImports.GetByKey(req.NamespacedName.String())
		if exist {
			resImp = resImpObj.(multiclusterv1alpha1.ResourceImport)
		} else {
			klog.InfoS("no cached data for ResourceImport", "resourceimport", req.NamespacedName.String(), "err", err)
			return ctrl.Result{}, nil
		}
	}

	switch resImp.Spec.Kind {
	case common.ServiceImportKind:
		if isDeleted {
			return r.handleResImpDeleteForService(ctx, &resImp)
		}
		return r.handleResImpUpdateForService(ctx, &resImp)
	case common.EndpointsKind:
		if isDeleted {
			return r.handleResImpDeleteForEndpoints(ctx, &resImp)
		}
		return r.handleResImpUpdateForEndpoints(ctx, &resImp)
	}
	// TODO: handle for other ResImport Kinds
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpUpdateForService(ctx context.Context, resImp *multiclusterv1alpha1.ResourceImport) (ctrl.Result, error) {
	svcName := types.NamespacedName{Namespace: resImp.Spec.Namespace, Name: resImp.Spec.Name}
	klog.InfoS("Updating Service corresponding to ResourceImport",
		"service", svcName.String(),
		"resourceimport", common.NamespacedName(resImp.Namespace, resImp.Name))

	svc := &corev1.Service{}
	err := (*r.localClusterManager).Get(ctx, svcName, svc)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	mcsObj := getMCSForServiceImport(resImp)
	if apierrors.IsNotFound(err) {
		err := (*r.localClusterManager).Create(ctx, mcsObj, &client.CreateOptions{})
		if err != nil {
			klog.ErrorS(err, "fail to import Service", "service", klog.KObj(mcsObj))
			return ctrl.Result{}, err
		}
		r.installedResImports.Add(*resImp)
		return ctrl.Result{}, nil
	}
	// Update MCS logic here
	if _, ok := svc.Labels[common.AntreaMCSAutoGenAnnotation]; !ok {
		klog.InfoS("Service has no desired label 'antrea.io/multi-cluster-autogenerated', skip update", "service", svcName.String())
		return ctrl.Result{}, nil
	}
	// TODO: check label difference ?
	if !apiequality.Semantic.DeepEqual(svc.Spec.Ports, mcsObj.Spec.Ports) {
		svc.Spec.Ports = mcsObj.Spec.Ports
		err = (*r.localClusterManager).Update(ctx, svc, &client.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "fail to update imported Service", "service", svcName.String())
			return ctrl.Result{}, err
		}
		r.installedResImports.Update(*resImp)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpDeleteForService(ctx context.Context, resImp *multiclusterv1alpha1.ResourceImport) (ctrl.Result, error) {
	svcName := types.NamespacedName{Namespace: resImp.Spec.Namespace, Name: resImp.Spec.Name}
	klog.InfoS("Deleting Service corresponding to ResourceImport", "service", svcName.String(), "resourceimport", common.NamespacedName(resImp.Namespace, resImp.Name))
	svc := &corev1.Service{}
	err := (*r.localClusterManager).Get(ctx, svcName, svc)
	if err != nil {
		klog.InfoS("unable to fetch imported Service", "service", svcName.String(), "err", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	err = (*r.localClusterManager).Delete(ctx, svc, &client.DeleteOptions{})
	if err != nil {
		klog.InfoS("failed to delete imported Service", "service", svcName.String(), "err", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpUpdateForEndpoints(ctx context.Context, resImp *multiclusterv1alpha1.ResourceImport) (ctrl.Result, error) {
	klog.InfoS("Updating Endpoints corresponding to ResourceImport", resImp.Namespace, resImp.Name)
	epName := types.NamespacedName{Namespace: resImp.Spec.Namespace, Name: resImp.Spec.Name}
	ep := &corev1.Endpoints{}
	err := (*r.localClusterManager).Get(ctx, epName, ep)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	mcsEpObj := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resImp.Spec.Name,
			Namespace: resImp.Spec.Namespace,
			Labels:    map[string]string{common.AntreaMCSAutoGenAnnotation: "true"},
		},
		Subsets: resImp.Spec.Endpoints.Subsets,
	}
	if apierrors.IsNotFound(err) {
		err := (*r.localClusterManager).Create(ctx, mcsEpObj, &client.CreateOptions{})
		if err != nil {
			klog.Errorf("fail to create MCS Endpoints %s/%s,err: %v", mcsEpObj.GetNamespace(), mcsEpObj.GetName(), err)
			return ctrl.Result{}, err
		}
		r.installedResImports.Add(*resImp)
		return ctrl.Result{}, nil
	}
	if _, ok := ep.Labels[common.AntreaMCSAutoGenAnnotation]; !ok {
		klog.Infof("Endpoints %s has no desired label 'antrea.io/multi-cluster-autogenerated', skip update", epName.String())
		return ctrl.Result{}, nil
	}
	// TODO: check label difference ?
	if !apiequality.Semantic.DeepEqual(resImp.Spec.Endpoints.Subsets, ep.Subsets) {
		ep.Subsets = resImp.Spec.Endpoints.Subsets
		err = (*r.localClusterManager).Update(ctx, ep, &client.UpdateOptions{})
		if err != nil {
			klog.Error("fail to update MCS Endpoints %s/%s,err: %v", resImp.Spec.Namespace, resImp.Spec.Name, err)
			return ctrl.Result{}, err
		}
		r.installedResImports.Update(*resImp)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpDeleteForEndpoints(ctx context.Context, resImp *multiclusterv1alpha1.ResourceImport) (ctrl.Result, error) {
	klog.Infof("Deleting Endpoints corresponding to ResourceImport", "resourceimport", common.NamespacedName(resImp.Namespace, resImp.Name))
	epName := types.NamespacedName{Namespace: resImp.Spec.Namespace, Name: resImp.Spec.Name}
	ep := &corev1.Endpoints{}
	err := (*r.localClusterManager).Get(ctx, epName, ep)
	if err != nil {
		klog.InfoS("unable to fetch imported Endpoints", "endpoints", epName.String(), "err", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	err = (*r.localClusterManager).Delete(ctx, ep, &client.DeleteOptions{})
	if err != nil {
		klog.InfoS("fail to delete imported Endpoints", "endpoints", epName.String(), "err", err)
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	return ctrl.Result{}, nil
}

func getMCSForServiceImport(resImp *multiclusterv1alpha1.ResourceImport) *corev1.Service {
	svcPort := int32(0)
	protocol := corev1.ProtocolTCP
	for _, p := range resImp.Spec.ServiceImport.Spec.Ports {
		if p.Port > 0 {
			svcPort = p.Port
			protocol = p.Protocol
			break
		}
	}
	mcs := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resImp.Spec.Name,
			Namespace: resImp.Spec.Namespace,
			Labels: map[string]string{
				common.AntreaMCSAutoGenAnnotation: "true",
				common.SourceImportAnnotation:     resImp.Name,
			},
		},
		TypeMeta: metav1.TypeMeta{
			Kind: "Service",
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeClusterIP,
			Ports: []corev1.ServicePort{
				{
					Protocol: protocol,
					Port:     svcPort,
				},
			},
		},
	}
	return mcs
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceImportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&multiclusterv1alpha1.ResourceImport{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.DefaultWorkerCount,
		}).
		Complete(r)
}
