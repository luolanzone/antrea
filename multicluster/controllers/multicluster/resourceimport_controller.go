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
	"context"
	"errors"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	multiclusterv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/internal"
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
	return NamespacedName(ri.Namespace, ri.Name), nil
}

// ResourceImportReconciler reconciles a ResourceImport object
type ResourceImportReconciler struct {
	client.Client
	Scheme               *runtime.Scheme
	remoteClusterManager *internal.RemoteClusterManager
	installedSvcs        cache.Indexer
	installedResImports  cache.Indexer
}

func NewResourceImportReconciler(client client.Client, scheme *runtime.Scheme, remoteClusterMgr *internal.RemoteClusterManager) *ResourceImportReconciler {
	return &ResourceImportReconciler{
		Client:               client,
		Scheme:               scheme,
		remoteClusterManager: remoteClusterMgr,
		installedSvcs: cache.NewIndexer(svcInfoKeyFunc, cache.Indexers{
			svcIndexerByType: svcIndexerByTypeFunc,
		}),
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
	klog.V(2).Infof("Reconciling ResourceImport %s", req.NamespacedName)
	// TODO: Must check whether this ResourceImport must be reconciled by this member cluster. Check `spec.clusters` field.
	remoteClusterMgr := *r.remoteClusterManager
	if remoteClusterMgr == nil {
		return ctrl.Result{}, errors.New("clusterset has not been initialized properly, no remote cluster manager")
	}
	remoteCluster, err := r.getRemoteCluster()
	if err != nil {
		return ctrl.Result{}, err
	}
	var resImp multiclusterv1alpha1.ResourceImport
	err = remoteCluster.Get(ctx, req.NamespacedName, &resImp)
	if err != nil && !apierrors.IsNotFound(err) {
		klog.Infof("unable to fetch ResourceImport %s/%s, err: %v", req.Namespace, req.Name, err)
		return ctrl.Result{}, err
	}
	if apierrors.IsNotFound(err) {
		resImpObj, exist, err := r.installedResImports.GetByKey(req.NamespacedName.String())
		if exist {
			resImp = resImpObj.(multiclusterv1alpha1.ResourceImport)
		} else {
			klog.Infof("no cached data for ResourceImport %s/%s, err: %v", req.Namespace, req.Name, err)
			return ctrl.Result{}, nil
		}
	}
	// TODO: need a background process to clean up service and endpoints when installedResImports
	// cache is gone when controller restart.
	switch resImp.Spec.Kind {
	case ServiceImportKind:
		if apierrors.IsNotFound(err) {
			klog.Infof("unable to find ResourceImport %s/%s, err: %v", req.Namespace, req.Name, err)
			return r.handleResImpDeleteForService(ctx, &resImp)
		}
		// TODO: skip status update
		klog.Infof("start to update service according to ResourceImport %s/%s", req.Namespace, req.Name)
		return r.handleResImpUpdateForService(ctx, &resImp)
	case EndpointsKind:
		if apierrors.IsNotFound(err) {
			return r.handleResImpDeleteForEndpoints(ctx, &resImp)
		}
		// TODO: skip status update
		return r.handleResImpUpdateForEndpoints(ctx, &resImp)
	}
	// TODO: handle for other ResImport Kinds
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpUpdateForService(ctx context.Context, resImp *multiclusterv1alpha1.ResourceImport) (ctrl.Result, error) {
	klog.Infof("Updating Service corresponding to ResourceImport %s/%s", resImp.Namespace, resImp.Name)
	svcName := GetNamespacedName(resImp.Spec.Namespace, resImp.Spec.Name)
	svc := &corev1.Service{}
	err := r.Client.Get(ctx, svcName, svc)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	mcsObj := getMCSForServiceImport(resImp)
	if apierrors.IsNotFound(err) {
		// Create MCS
		err := r.Client.Create(ctx, mcsObj, &client.CreateOptions{})
		if err != nil {
			klog.Error("fail to create MCS Service %s/%s,err: %v", mcsObj.GetNamespace(), mcsObj.GetName(), err)
			return ctrl.Result{}, err
		}
		r.installedResImports.Add(*resImp)
		return ctrl.Result{}, nil
	}
	// Update MCS logic here
	if _, ok := svc.Labels[antreaMcsAutoGenLabel]; !ok {
		// return immediately if Service is without label "antrea.io/mult-cluster-autogenerated"
		return ctrl.Result{}, nil
	}
	// TODO: check label difference ?
	if !apiequality.Semantic.DeepEqual(svc.Spec.Ports, mcsObj.Spec.Ports) {
		svc.Spec.Ports = mcsObj.Spec.Ports
		err = r.Client.Update(ctx, svc, &client.UpdateOptions{})
		if err != nil {
			klog.Error("fail to update MCS Service %s/%s,err: %v", resImp.Spec.Namespace, resImp.Spec.Name, err)
			return ctrl.Result{}, err
		}
		r.installedResImports.Update(*resImp)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpDeleteForService(ctx context.Context, resImp *multiclusterv1alpha1.ResourceImport) (ctrl.Result, error) {
	klog.Infof("Deleting Service corresponding to ResourceImport %s/%s", resImp.Namespace, resImp.Name)
	svcName := GetNamespacedName(resImp.Spec.Namespace, resImp.Spec.Name)
	svc := &corev1.Service{}
	err := r.Client.Get(ctx, svcName, svc)
	if err != nil {
		// TODO add some cleanup logic here
		if apierrors.IsNotFound(err) {
			klog.Infof("Service %s is already deleted. Nothing to do.", svcName)
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, err
		}
	}
	err = r.Client.Delete(ctx, svc, &client.DeleteOptions{})
	if err != nil {
		klog.Errorf("failed to delete Multi-Cluster Service %s, err: %v", svcName.String(), err)
		// TODO add some cleanup logic here
		if apierrors.IsNotFound(err) {
			klog.Infof("Service %s is already deleted. Nothing to do.", svcName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpUpdateForEndpoints(ctx context.Context, resImp *multiclusterv1alpha1.ResourceImport) (ctrl.Result, error) {
	klog.Infof("Updating Endpoints corresponding to ResourceImport %s/%s", resImp.Namespace, resImp.Name)
	epName := GetNamespacedName(resImp.Spec.Namespace, resImp.Spec.Name)
	ep := &corev1.Endpoints{}
	err := r.Client.Get(ctx, epName, ep)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}
	mcsEpObj := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resImp.Spec.Name,
			Namespace: resImp.Spec.Namespace,
			Labels:    map[string]string{antreaMcsAutoGenLabel: ""},
		},
		Subsets: resImp.Spec.Endpoints.Subsets,
	}
	if apierrors.IsNotFound(err) {
		// Create MCS Endpoints
		err := r.Client.Create(ctx, mcsEpObj, &client.CreateOptions{})
		if err != nil {
			klog.Errorf("fail to create MCS Endpoints %s/%s,err: %v", mcsEpObj.GetNamespace(), mcsEpObj.GetName(), err)
			return ctrl.Result{}, err
		}
		r.installedResImports.Add(*resImp)
		return ctrl.Result{}, nil
	}
	if _, ok := ep.Labels[antreaMcsAutoGenLabel]; !ok {
		// return immediately if Service is without label "antrea.io/mult-cluster-autogenerated"
		return ctrl.Result{}, nil
	}
	// Update MCS Endpoints logic here
	// TODO: check label difference ?
	if !apiequality.Semantic.DeepEqual(resImp.Spec.Endpoints.Subsets, ep.Subsets) {
		ep.Subsets = resImp.Spec.Endpoints.Subsets
		err = r.Client.Update(ctx, ep, &client.UpdateOptions{})
		if err != nil {
			klog.Error("fail to update MCS Endpoints %s/%s,err: %v", resImp.Spec.Namespace, resImp.Spec.Name, err)
			return ctrl.Result{}, err
		}
		r.installedResImports.Update(*resImp)
	}
	return ctrl.Result{}, nil
}

func (r *ResourceImportReconciler) handleResImpDeleteForEndpoints(ctx context.Context, resImp *multiclusterv1alpha1.ResourceImport) (ctrl.Result, error) {
	klog.Infof("Deleting Endpoints corresponding to ResourceImport %s/%s", resImp.Namespace, resImp.Name)
	epName := GetNamespacedName(resImp.Spec.Namespace, resImp.Spec.Name)
	ep := &corev1.Endpoints{}
	err := r.Client.Get(ctx, epName, ep)
	if err != nil {
		// TODO add some cleanup logic here
		if apierrors.IsNotFound(err) {
			klog.Infof("Endpoints %s is already deleted. Nothing to do.", epName)
			return ctrl.Result{}, nil
		} else {
			return ctrl.Result{}, err
		}
	}
	err = r.Client.Delete(ctx, ep, &client.DeleteOptions{})
	if err != nil {
		klog.Errorf("failed to delete Multi-Cluster Endpoints %s, err: %v", epName.String(), err)
		// TODO add some cleanup logic here
		if apierrors.IsNotFound(err) {
			klog.Infof("Endpoints %s is already deleted. Nothing to do.", epName)
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

// TODO: Demo purpose only. Remove this function.
func (r *ResourceImportReconciler) getRemoteCluster() (internal.RemoteCluster, error) {
	var remoteCluster internal.RemoteCluster
	remoteClusters := (*r.remoteClusterManager).GetRemoteClusters()
	if len(remoteClusters) <= 0 {
		return remoteCluster, errors.New("clusterset has not been initialized properly, no remote clusters found")
	}
	for _, c := range remoteClusters {
		return c, nil
	}
	return remoteCluster, nil
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
			// Add necessary labels and annotations
			Labels: map[string]string{antreaMcsAutoGenLabel: ""},
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
		Complete(r)
}
