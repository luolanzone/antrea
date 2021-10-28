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
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

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
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	k8smcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/multicluster/controllers/multicluster/internal"
)

type (
	svcInfo struct {
		name       string
		namespace  string
		clusterIPs []string
		ports      []corev1.ServicePort
		svcType    string
	}

	epInfo struct {
		name       string
		namespace  string
		addressIPs []string
		ports      []corev1.EndpointPort
		labels     map[string]string
	}

	// ServiceExportReconciler reconciles a ServiceExport object
	ServiceExportReconciler struct {
		Client               client.Client
		Scheme               *runtime.Scheme
		remoteClusterManager *internal.RemoteClusterManager
		installedSvcs        cache.Indexer
		installedEps         cache.Indexer
	}
)

const (
	// cached indexer
	svcIndexerByType = "service.by.type"
	epIndexerByLabel = "endpoints.by.label"
)

var (
	leaderNamespace string
	leaderClusterID string
	localClusterID  string
)

func NewServiceExportReconciler(
	Client client.Client,
	Scheme *runtime.Scheme,
	remoteClusterManager *internal.RemoteClusterManager) *ServiceExportReconciler {
	reconciler := &ServiceExportReconciler{
		Client:               Client,
		Scheme:               Scheme,
		remoteClusterManager: remoteClusterManager,
		installedSvcs: cache.NewIndexer(svcInfoKeyFunc, cache.Indexers{
			svcIndexerByType: svcIndexerByTypeFunc,
		}),
		installedEps: cache.NewIndexer(epInfoKeyFunc, cache.Indexers{
			epIndexerByLabel: epIndexerByLabelFunc,
		}),
	}
	return reconciler
}

func svcInfoKeyFunc(obj interface{}) (string, error) {
	svc := obj.(*svcInfo)
	return common.NamespacedName(svc.namespace, svc.name), nil
}

func svcIndexerByTypeFunc(obj interface{}) ([]string, error) {
	return []string{obj.(*svcInfo).svcType}, nil
}

func epInfoKeyFunc(obj interface{}) (string, error) {
	ep := obj.(*epInfo)
	return common.NamespacedName(ep.namespace, ep.name), nil
}

func epIndexerByLabelFunc(obj interface{}) ([]string, error) {
	var info []string
	ep := obj.(*epInfo)
	keys := make([]string, len(ep.labels))
	i := 0
	for k := range ep.labels {
		keys[i] = k
		i++
	}
	sort.Strings(keys)
	for _, k := range keys {
		info = append(info, k+ep.labels[k])
	}
	return info, nil
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/finalizers,verbs=update
//+kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=serviceexports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=serviceexports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update
//+kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch;update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// For ServiceExport controller, it watches events of ServiceExport resource, and also
// Endpoints/Services resource if they has the label 'antrea.io/multi-cluster'.
// It will create/update/remove ResourceExport resource in leader cluster
// for corresponding ServiceExport in member cluster.
func (r *ServiceExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(2).Infof("reconciling %s", req.NamespacedName)
	if *r.remoteClusterManager == nil {
		return ctrl.Result{}, errors.New("clusterset has not been initialized properly, no remote cluster manager")
	}
	localClusterID = string((*r.remoteClusterManager).GetLocalClusterID())
	if len(localClusterID) == 0 {
		return ctrl.Result{}, errors.New("localClusterID is not initialized, skip reconcile")
	}

	var svcExport k8smcsv1alpha1.ServiceExport
	svcObj, svcInstalled, _ := r.installedSvcs.GetByKey(req.String())
	epObj, epInstalled, _ := r.installedEps.GetByKey(req.String())
	svcResExportName := getResourceExportName(localClusterID, req, "service")
	epResExportName := getResourceExportName(localClusterID, req, "endpoints")

	remoteCluster, err := getRemoteCluster(r.remoteClusterManager)
	if err != nil {
		return ctrl.Result{}, err
	}

	leaderNamespace = remoteCluster.GetNamespace()
	leaderClusterID = string(remoteCluster.GetClusterID())

	svc := &corev1.Service{}
	if err := r.Client.Get(ctx, req.NamespacedName, &svcExport); err != nil {
		klog.V(2).InfoS("unable to fetch ServiceExport", "serviceexport", req.String(), "err", err)
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// cleanup multi-cluster labels for the Service so we don't watch it anymore
		r.cleanUpLabels(ctx, svc, req)
		err = r.handleDeleteEvent(ctx, req, remoteCluster)
		if err != nil {
			return ctrl.Result{}, err
		}
		// clean up cache
		if svcInstalled {
			r.installedSvcs.Delete(svcObj)
		}
		if epInstalled {
			r.installedEps.Delete(epObj)
		}
		return ctrl.Result{}, nil
	}

	// if corresponding service doesn't exist, update ServiceExport's status reason to not_found_service.
	err = r.Client.Get(ctx, req.NamespacedName, svc)
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = r.updateSvcExportStatus(ctx, req, "not-found")
			if err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		} else {
			klog.ErrorS(err, "fail to get Service ", req.String())
			return ctrl.Result{}, err
		}
	}

	// skip if ServiceExport is trying to export MCS Service/Endpoints
	if !svcInstalled || !epInstalled {
		if _, ok := svc.Labels[common.AntreaMcsAutoGenLabel]; ok {
			klog.InfoS("it's not allowed to export the multi-cluster controller auto-generated Service", "service", req.String())
			err = r.updateSvcExportStatus(ctx, req, "imported-service")
			if err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
	}

	// we also watch Service and Endpoints events via events mapping function
	// need to check cache and compare with cache if there is any change for Service or Endpoints
	var svcNoChange, epNoChange bool
	svcExportNSName := common.NamespacedName(leaderNamespace, svcResExportName)
	epExportNSName := common.NamespacedName(leaderNamespace, epResExportName)
	if svcInstalled {
		installedSvc := svcObj.(*svcInfo)
		if apiequality.Semantic.DeepEqual(svc.Spec.Ports, installedSvc.ports) &&
			apiequality.Semantic.DeepEqual(svc.Spec.ClusterIPs, installedSvc.clusterIPs) {
			klog.InfoS("Service has been converted into ResourceExport and no change, skip it", "service",
				req.String(), "resourceexport", svcExportNSName)
			svcNoChange = true
		}
	}

	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}

	err = r.Client.Get(ctx, req.NamespacedName, ep)
	if err != nil {
		klog.ErrorS(err, "fail to get Endpoints", "endpoints", req.String())
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	if epInstalled {
		installedEp := epObj.(*epInfo)
		if apiequality.Semantic.DeepEqual(getEndPointsPorts(ep), installedEp.ports) &&
			apiequality.Semantic.DeepEqual(getEndPointsAddress(ep), installedEp.addressIPs) {
			klog.InfoS("Endpoints has been converted into ResourceExport and no change, skip it", "endpoints",
				req.String(), "resourceexport", epExportNSName)
			epNoChange = true
		}
	}

	if epNoChange && svcNoChange {
		return ctrl.Result{}, nil
	}

	re := mcsv1alpha1.ResourceExport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: leaderNamespace,
			Labels: map[string]string{
				"sourceName":      req.Name,
				"sourceNamespace": req.Namespace,
				"sourceClusterID": localClusterID,
			},
		},
		Spec: mcsv1alpha1.ResourceExportSpec{
			ClusterID: localClusterID,
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}

	if !svcNoChange {
		klog.InfoS("Service has new changes, apply into ResourceExport", "service", req.String(),
			"resourceexport", svcExportNSName)
		err := r.serviceHandler(ctx, req, svc, svcResExportName, re, remoteCluster)
		if err != nil {
			klog.Infof("fail to handle service change %s/%s, err: %v", svc.Namespace, svc.Name, err)
			return ctrl.Result{}, err
		}
	}

	if !epNoChange {
		klog.InfoS("Endpoints have new change, apply into ResourceExport", "endpoints",
			req.String(), "resourceexport", epExportNSName)
		err := r.endpointsHandler(ctx, req, ep, epResExportName, re, remoteCluster)
		if err != nil {
			klog.ErrorS(err, "fail to handle service change", "service", req.String())
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *ServiceExportReconciler) cleanUpLabels(ctx context.Context, svc *corev1.Service, req ctrl.Request) {
	err := r.Client.Get(ctx, req.NamespacedName, svc)
	if err == nil {
		delete(svc.Labels, common.AntreaMcsLabel)
		// ignore error since the service event will be triggered again
		// then here we can delete in another time.
		_ = r.Client.Update(ctx, svc)
	} else if apierrors.IsNotFound(err) {
		klog.V(2).InfoS("Service not found, no need to clean up multi-cluster labels", "service", req.String())
	} else {
		klog.V(2).ErrorS(err, "fail to get Service, unable to clean up multi-cluster labels", "service", req.String())
	}
}

func (r *ServiceExportReconciler) handleDeleteEvent(ctx context.Context, req ctrl.Request, remoteCluster internal.RemoteCluster) error {
	svcResExportName := getResourceExportName(localClusterID, req, "service")
	epResExportName := getResourceExportName(localClusterID, req, "endpoints")
	svcResExport := &mcsv1alpha1.ResourceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svcResExportName,
			Namespace: leaderNamespace,
		},
	}
	epResExport := &mcsv1alpha1.ResourceExport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      epResExportName,
			Namespace: leaderNamespace,
		},
	}

	// clean up Service/Endpoints kind of ResourceExport in remote leader cluster
	svcResExportNamespaced := common.NamespacedName(leaderNamespace, svcResExportName)
	epResExportNamespaced := common.NamespacedName(leaderNamespace, epResExportName)
	err := remoteCluster.Delete(ctx, svcResExport, &client.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		klog.InfoS("fail to delete ResourceExport in remote cluster", "resourceexport",
			svcResExportNamespaced, "clusterID", leaderClusterID, "err", err)
		return err
	}

	err = remoteCluster.Delete(ctx, epResExport, &client.DeleteOptions{})
	if err != nil && !apierrors.IsNotFound(err) {
		klog.InfoS("fail to delete ResourceExport in remote cluster", "resourceexport",
			epResExportNamespaced, "clusterID", leaderClusterID, "err", err)
		return err
	}

	klog.V(2).InfoS("clean up ResourceExports in remote cluster", "service", svcResExportNamespaced, "endpoints", epResExportNamespaced)
	return nil
}

func (r *ServiceExportReconciler) updateSvcExportStatus(ctx context.Context, req ctrl.Request, cause string) error {
	svcExport := &k8smcsv1alpha1.ServiceExport{}
	err := r.Client.Get(ctx, req.NamespacedName, svcExport)
	if err != nil {
		return client.IgnoreNotFound(err)
	}
	now := metav1.Now()
	var reason, message *string
	switch cause {
	case "not-found":
		reason = common.GetPointer("not_found_service")
		message = common.GetPointer("corresponding Service does not exist")
	case "imported-service":
		reason = common.GetPointer("imported_service")
		message = common.GetPointer("corresponding Service is imported service, it's not allowed to export")
	default:
		reason = common.GetPointer("invalid_service")
		message = common.GetPointer("it's not allowed to export")
	}

	svcExportConditions := svcExport.Status.DeepCopy().Conditions
	invalidCondition := k8smcsv1alpha1.ServiceExportCondition{
		Type:               k8smcsv1alpha1.ServiceExportValid,
		Status:             corev1.ConditionFalse,
		LastTransitionTime: &now,
		Reason:             reason,
		Message:            message,
	}

	matchedCondition := false
	for n, c := range svcExportConditions {
		if c.Type == k8smcsv1alpha1.ServiceExportValid {
			matchedCondition = true
			svcExportConditions[n] = invalidCondition
			break
		}
	}
	if !matchedCondition {
		svcExportConditions = append(svcExportConditions, invalidCondition)

	}
	svcExport.Status = k8smcsv1alpha1.ServiceExportStatus{
		Conditions: svcExportConditions,
	}
	err = r.Client.Status().Update(ctx, svcExport)
	if err != nil {
		klog.ErrorS(err, "fail to update ServiceExport", "serviceexport", req.String())
		return client.IgnoreNotFound(err)
	}
	return nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k8smcsv1alpha1.ServiceExport{}).
		Watches(&source.Kind{Type: &corev1.Service{}}, handler.EnqueueRequestsFromMapFunc(serviceMapFunc)).
		Watches(&source.Kind{Type: &corev1.Endpoints{}}, handler.EnqueueRequestsFromMapFunc(endpointsMapFunc)).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.DefaultWorkerCount,
		}).
		Complete(r)
}

func serviceMapFunc(a client.Object) []reconcile.Request {
	if _, ok := a.(*corev1.Service).Labels[common.AntreaMcsLabel]; ok {
		// events mapping from Service to ServiceExport
		return objToRequest(a)
	}
	return nil
}

func endpointsMapFunc(a client.Object) []reconcile.Request {
	if _, ok := a.(*corev1.Endpoints).Labels[common.AntreaMcsLabel]; ok {
		// events mapping from Endpoints to ServiceExport
		return objToRequest(a)
	}
	return nil
}

func objToRequest(a client.Object) []reconcile.Request {
	return []reconcile.Request{
		{
			NamespacedName: types.NamespacedName{
				Name:      a.GetName(),
				Namespace: a.GetNamespace(),
			},
		},
	}
}

// serviceHandler handle service related change
// ClusterIP type: update corresponding ResourceExport only when ClusterIP or Ports change.
func (r *ServiceExportReconciler) serviceHandler(
	ctx context.Context,
	req ctrl.Request,
	svc *corev1.Service,
	resName string,
	re mcsv1alpha1.ResourceExport,
	rc internal.RemoteCluster) error {
	kind := common.ServiceKind
	sinfo := &svcInfo{
		name:       svc.Name,
		namespace:  svc.Namespace,
		clusterIPs: svc.Spec.ClusterIPs,
		ports:      svc.Spec.Ports,
		svcType:    string(svc.Spec.Type),
	}
	r.refreshResourceExport(resName, kind, svc, nil, &re)
	existResExport := &mcsv1alpha1.ResourceExport{}
	resNamespaced := types.NamespacedName{Namespace: leaderNamespace, Name: resName}
	err := rc.Get(ctx, resNamespaced, existResExport)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.ErrorS(err, "fail to get ResourceExport", "resourceexport", resNamespaced.String())
			return err
		}
	}
	if err = r.updateOrCreateResourceExport(resName, ctx, req, &re, existResExport, rc); err != nil {
		return err
	}
	if svc.Labels != nil {
		svc.Labels[common.AntreaMcsLabel] = "true"
	} else {
		svc.Labels = map[string]string{common.AntreaMcsLabel: "true"}
	}
	// update Service's label with `antrea.io/multi-cluster` so we can watch Service's events via event mapping function.
	if err := r.Client.Update(ctx, svc); err != nil {
		klog.ErrorS(err, "fail to update Service labels", "service", req.String())
		return err
	}
	r.installedSvcs.Add(sinfo)
	return nil
}

// endpointsHandler handle Endpoints related change
// - update corresponding ResourceExport only when Ports or Addresses IP change.
func (r *ServiceExportReconciler) endpointsHandler(
	ctx context.Context,
	req ctrl.Request,
	ep *corev1.Endpoints,
	resName string,
	re mcsv1alpha1.ResourceExport,
	rc internal.RemoteCluster) error {
	kind := common.EndpointsKind
	epInfo := &epInfo{
		name:       ep.Name,
		namespace:  ep.Namespace,
		addressIPs: getEndPointsAddress(ep),
		ports:      getEndPointsPorts(ep),
		labels:     ep.Labels,
	}
	r.refreshResourceExport(resName, kind, nil, ep, &re)
	existResExport := &mcsv1alpha1.ResourceExport{}
	resNamespaced := types.NamespacedName{Namespace: leaderNamespace, Name: resName}
	err := rc.Get(ctx, resNamespaced, existResExport)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.ErrorS(err, "fail to get ResourceExport", "resourceexport", resNamespaced.String())
			return err
		}
	}
	if err = r.updateOrCreateResourceExport(resName, ctx, req, &re, existResExport, rc); err != nil {
		return err
	}
	r.installedEps.Add(epInfo)
	return nil
}

func (r *ServiceExportReconciler) refreshResourceExport(resName, kind string,
	svc *corev1.Service,
	ep *corev1.Endpoints,
	re *mcsv1alpha1.ResourceExport) mcsv1alpha1.ResourceExport {
	re.Spec.Kind = kind
	switch kind {
	case common.ServiceKind:
		re.ObjectMeta.Name = resName
		newSvcSpec := svc.Spec.DeepCopy()
		var renamedPorts []corev1.ServicePort
		for _, p := range svc.Spec.Ports {
			p.Name = strings.ToLower(string(p.Protocol)) + strconv.Itoa(int(p.Port))
			renamedPorts = append(renamedPorts, p)
		}
		newSvcSpec.Ports = renamedPorts
		re.Spec.Service = &mcsv1alpha1.ServiceExport{
			ServiceSpec: *newSvcSpec,
		}
		re.Labels["sourceKind"] = common.ServiceKind
	case common.EndpointsKind:
		re.ObjectMeta.Name = resName
		re.Spec.Endpoints = &mcsv1alpha1.EndpointsExport{
			Subsets: ep.Subsets,
		}
		re.Labels["sourceKind"] = common.EndpointsKind
	}
	return *re
}

func (r *ServiceExportReconciler) updateOrCreateResourceExport(resName string,
	ctx context.Context,
	req ctrl.Request,
	newResExport *mcsv1alpha1.ResourceExport,
	existResExport *mcsv1alpha1.ResourceExport,
	rc internal.RemoteCluster) error {
	createResExport := reflect.DeepEqual(*existResExport, mcsv1alpha1.ResourceExport{})
	resNamespaced := types.NamespacedName{Namespace: leaderNamespace, Name: resName}
	if createResExport {
		newResExport.Finalizers = []string{common.ResourceExportFinalizer}
		klog.InfoS("creating ResourceExport", "resourceexport", resNamespaced.String())
		err := rc.Create(ctx, newResExport, &client.CreateOptions{})
		if err != nil {
			klog.ErrorS(err, "fail to create ResourceExport in leader cluster", "resourceexport", resNamespaced.String())
			return err
		}
	} else {
		newResExport.ObjectMeta.ResourceVersion = existResExport.ObjectMeta.ResourceVersion
		newResExport.Finalizers = existResExport.Finalizers
		err := rc.Update(ctx, newResExport, &client.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "fail to update ResourceExport", "resourceexport", resNamespaced.String())
			return err
		}
	}

	var message string
	if createResExport {
		message = "creation is successful"
	} else {
		message = "update is successful"
	}

	// ignore status update failure?
	latestResExport := &mcsv1alpha1.ResourceExport{}
	err := rc.Get(ctx, resNamespaced, latestResExport)
	if err != nil {
		klog.InfoS("fail to get ResourceExport", "resourceexport", resNamespaced.String(), "err", err)
		return nil
	}
	latestResExport.Status.Conditions = []mcsv1alpha1.ResourceExportCondition{{
		Type:               mcsv1alpha1.ResourceExportSucceeded,
		Status:             corev1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(time.Now()),
		Message:            message,
	}}
	err = rc.Status().Update(ctx, latestResExport, &client.UpdateOptions{})
	if err != nil {
		klog.InfoS("fail to update ResourceExport Status", "resourceexport", resNamespaced.String(), "err", err)
	}
	return nil
}

func getEndPointsAddress(ep *corev1.Endpoints) []string {
	var epAddrs []string
	for _, s := range ep.Subsets {
		for _, a := range s.Addresses {
			epAddrs = append(epAddrs, a.IP)
		}
	}
	return epAddrs
}

func getEndPointsPorts(ep *corev1.Endpoints) []corev1.EndpointPort {
	var epPorts []corev1.EndpointPort
	for _, s := range ep.Subsets {
		epPorts = append(epPorts, s.Ports...)
	}
	return epPorts
}

func getResourceExportName(clusterID string, req ctrl.Request, kind string) string {
	return clusterID + "-" + req.Namespace + "-" + req.Name + "-" + kind
}
