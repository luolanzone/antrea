/*
Copyright 2021 Antrea AuthorsvcExport.

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
	"reflect"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	"sigs.k8s.io/controller-runtime/pkg/source"
	k8smcsv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"
	k8smcsversioned "sigs.k8s.io/mcs-api/pkg/client/clientset/versioned"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	antreamcsversioned "antrea.io/antrea/multicluster/pkg/client/clientset/versioned"
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
		Client          client.Client
		Scheme          *runtime.Scheme
		K8sClient       kubernetes.Interface
		LeaderK8sClient kubernetes.Interface
		K8smcsCrdClient k8smcsversioned.Interface
		//TODO when leader HA is enabled, need a way to refresh AntreamcsCrdClient
		AntreamcsCrdClient antreamcsversioned.Interface
		installedSvcs      cache.Indexer
		installedEps       cache.Indexer
	}
)

const (
	// cached indexer
	svcIndexerByType                     = "service.by.type"
	epIndexerByLabel                     = "endpoints.by.label"
	nodeIndexerByServiceName             = "node.by.serviceName"
	resExportIndexerByKind               = "resourceexport.by.kind"
	resExportIndexerByNameSpacedNameKind = "resourceexport.by.namespacedNameKind"
)

func NewServiceExportReconciler(
	Client client.Client,
	Scheme *runtime.Scheme,
	k8sClient kubernetes.Interface,
	k8smcsCrdClient k8smcsversioned.Interface,
	antreamcsCrdClient antreamcsversioned.Interface) *ServiceExportReconciler {
	reconciler := &ServiceExportReconciler{
		Client:             Client,
		Scheme:             Scheme,
		K8sClient:          k8sClient,
		K8smcsCrdClient:    k8smcsCrdClient,
		AntreamcsCrdClient: antreamcsCrdClient,
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
	return svc.namespace + svc.name, nil
}

func svcIndexerByTypeFunc(obj interface{}) ([]string, error) {
	return []string{obj.(*svcInfo).svcType}, nil
}

func epInfoKeyFunc(obj interface{}) (string, error) {
	ep := obj.(*epInfo)
	return ep.namespace + ep.name, nil
}

func epIndexerByLabelFunc(obj interface{}) ([]string, error) {
	var info []string
	ep := obj.(*epInfo)
	for k, v := range ep.labels {
		info = append(info, k+v)
	}
	return info, nil
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/finalizers,verbs=update
//+kubebuilder:rbac:groups="multicluster.x-k8s.io",resources=serviceexports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;update
//+kubebuilder:rbac:groups="",resources=endpoints,verbs=get;list;watch;update
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch

func (r *ServiceExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.Infof("request namespace %s, name %s ", req.NamespacedName, req.Name)
	var svcExport k8smcsv1alpha1.ServiceExport
	clusterID := LocalClusterID

	key := getKey(req)
	svcObj, svcInstalled, _ := r.installedSvcs.GetByKey(key)
	epObj, epInstalled, _ := r.installedEps.GetByKey(key)
	resExportBaseName := clusterID + "-" + req.Namespace + "-" + req.Name
	svcResExportName := getResourceExportName(resExportBaseName, ServiceKind)
	epResExportName := getResourceExportName(resExportBaseName, EndpointsKind)

	if err := r.Client.Get(ctx, req.NamespacedName, &svcExport); err != nil {
		klog.Infof("unable to fetch ServiceExport %s/%s, err: %v", req.Namespace, req.Name, err)
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// clean up Service/Endpoints kind of ResourceExport in leader cluster
		klog.Infof("deleting ResourceExport %s/%s in Leader Cluster", LeaderNameSpace, svcResExportName)
		err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).Delete(ctx, svcResExportName, metav1.DeleteOptions{})
		if err != nil {
			klog.Errorf("fail to delete ResourceExport %s/%s, err: %v", LeaderNameSpace, svcResExportName, err)
		}
		klog.Infof("deleting ResourceExport %s/%s in Leader Cluster", LeaderNameSpace, epResExportName)
		err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).Delete(ctx, epResExportName, metav1.DeleteOptions{})
		if err != nil {
			klog.Errorf("fail to delete ResourceExport %s/%s, err: %v", req.Namespace, epResExportName, err)
		}
		if svcInstalled {
			_ = r.installedSvcs.Delete(svcObj)
		}
		if epInstalled {
			_ = r.installedEps.Delete(epObj)
		}
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// check if corresponding service exists or not, if it's deleted
	// then need to clean up ServiceExport
	svc, err := r.K8sClient.CoreV1().Services(req.Namespace).Get(ctx, req.Name, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			err = r.K8smcsCrdClient.MulticlusterV1alpha1().ServiceExports(req.Namespace).Delete(ctx, req.Name, metav1.DeleteOptions{})
			if err != nil {
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
		} else {
			klog.Errorf("fail to get Service %s/%s, err: %v", req.Namespace, req.Name, err)
			return ctrl.Result{}, err
		}
	}

	// we also watch Service and Endpoints events via events mapping function
	// need to check cache and compare with cache if there is any change for Service or Endpoints
	var svcNoChange, epNoChange bool
	if svcInstalled {
		installedSvc := svcObj.(*svcInfo)
		if reflect.DeepEqual(svc.Spec.Ports, installedSvc.ports) &&
			reflect.DeepEqual(svc.Spec.ClusterIPs, installedSvc.clusterIPs) {
			klog.Infof("Service %s/%s has been converted into ResourceExport %s/%s and no change, skip it", svc.Namespace, svc.Name, LeaderNameSpace, svcResExportName)
			svcNoChange = true
		}
	}

	ep := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Name:      svc.Name,
			Namespace: svc.Namespace,
			Labels: map[string]string{
				"SourceServiceType": string(corev1.ServiceTypeNodePort),
			},
		},
	}

	if svc.Spec.Type == corev1.ServiceTypeNodePort {
		// Build up Endpoint with Node ip and nodePort for NodePort service.
		nodes, err := r.K8sClient.CoreV1().Nodes().List(ctx, metav1.ListOptions{FieldSelector: "spec.unschedulable=false"})
		if err != nil {
			klog.Error("fail to get nodes list")
			return ctrl.Result{}, err
		}
		var addresses []corev1.EndpointAddress
		for _, n := range nodes.Items {
			for _, a := range n.Status.Addresses {
				// prefer to use external IP?
				if a.Type == corev1.NodeExternalIP {
					addresses = append(addresses, corev1.EndpointAddress{IP: a.Address})
					break
				}
				if a.Type == corev1.NodeInternalIP {
					addresses = append(addresses, corev1.EndpointAddress{IP: a.Address})
					break
				}
			}
		}

		var ports []corev1.EndpointPort
		for _, p := range svc.Spec.Ports {
			ports = append(ports, corev1.EndpointPort{
				Port:     p.NodePort,
				Protocol: p.Protocol,
			})
		}
		ep.Subsets = []corev1.EndpointSubset{{Addresses: addresses, Ports: ports}}
	} else {
		ep, err = r.K8sClient.CoreV1().Endpoints(svcExport.Namespace).Get(ctx, svcExport.Name, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("fail to get Endpoints %s/%s, err: %v", svcExport.Namespace, svcExport.Name, err)
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
	}

	if epInstalled {
		installedEp := epObj.(*epInfo)
		if reflect.DeepEqual(getEndPointsPorts(ep), installedEp.ports) &&
			reflect.DeepEqual(getEndPointsAddress(ep), installedEp.addressIPs) {
			klog.Infof("Endpoints %s/%s has been converted into ResourceExport %s/%s and no change, skip it", ep.Namespace, ep.Name, LeaderNameSpace, epResExportName)
			epNoChange = true
		}
	}

	if epNoChange && svcNoChange {
		return ctrl.Result{}, nil
	}

	re := mcsv1alpha1.ResourceExport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: LeaderNameSpace,
			Labels: map[string]string{
				"sourceName":      req.Name,
				"sourceNamespace": req.Namespace,
				"sourceClusterID": clusterID,
			},
		},
		Spec: mcsv1alpha1.ResourceExportSpec{
			ClusterID: clusterID,
			Name:      req.Name,
			Namespace: req.Namespace,
		},
	}

	if !svcNoChange {
		klog.Infof("Service %s/%s has new changes, apply into ResourceExport %s/%s", svc.Namespace, svc.Name, LeaderNameSpace, svcResExportName)
		err := r.serviceHandler(ctx, req, svc, svcResExportName, re)
		if err != nil {
			klog.Infof("fail to handle service change %s/%s, err: %v", svc.Namespace, svc.Name, err)
			return ctrl.Result{}, err
		}
	}

	if !epNoChange {
		klog.Infof("Endpoints %s/%s have new change, apply into ResourceExport %s/%s", ep.Namespace, ep.Name, LeaderNameSpace, epResExportName)
		err := r.endpointsHandler(ctx, req, ep, epResExportName, re)
		if err != nil {
			klog.Infof("fail to handle service change %s/%s, err: %v", svc.Namespace, svc.Name, err)
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ServiceExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&k8smcsv1alpha1.ServiceExport{}).
		Watches(&source.Kind{Type: &corev1.Service{}}, handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
			if _, ok := a.(*corev1.Service).Labels[antreaMultiClusterServiceLabel]; ok {
				// events mapping from Service to ServiceExport
				return []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Name:      a.GetName(),
							Namespace: a.GetNamespace(),
						},
					},
				}
			}
			return nil
		})).
		Watches(&source.Kind{Type: &corev1.Endpoints{}}, handler.EnqueueRequestsFromMapFunc(func(a client.Object) []reconcile.Request {
			if _, ok := a.(*corev1.Endpoints).Labels[antreaMultiClusterServiceLabel]; ok {
				// events mapping from Endpoints to ServiceExport
				return []reconcile.Request{
					{
						NamespacedName: types.NamespacedName{
							Name:      a.GetName(),
							Namespace: a.GetNamespace(),
						},
					},
				}
			}
			return nil
		})).Complete(r)
}

// serviceHandler handle service related change
// - ClusterIP: update corresponding ResourceExport only when ClusterIP or Ports change.
// - NodePort: update corresponding ResourceExport only when ClusterIP or Ports change.
// ...
func (r *ServiceExportReconciler) serviceHandler(
	ctx context.Context,
	req ctrl.Request,
	svc *corev1.Service,
	resName string,
	re mcsv1alpha1.ResourceExport) error {
	kind := ServiceKind
	sinfo := &svcInfo{
		name:       svc.Name,
		namespace:  svc.Namespace,
		clusterIPs: svc.Spec.ClusterIPs,
		ports:      svc.Spec.Ports,
		svcType:    string(svc.Spec.Type),
	}
	r.refreshResourceExport(resName, kind, svc, nil, &re)
	existResExport, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).Get(ctx, resName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("fail to get ResourceExport %s/%s, err: %v", LeaderNameSpace, resName, err)
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err = r.createResourceExport(resName, ctx, req, &re); err != nil {
			return err
		}
		// update Service's label with `antrea.io/multi-cluster` so we can watch Service's events via event mapping function.
		labels := svc.DeepCopy().Labels
		labels[antreaMultiClusterServiceLabel] = "true"
		svc.Labels = labels
		if _, err := r.K8sClient.CoreV1().Services(svc.Namespace).Update(ctx, svc, metav1.UpdateOptions{}); err != nil {
			klog.Infof("fail to update Service %s/%s's labels, err: %v", svc.Namespace, svc.Name, err)
		}
		r.installedSvcs.Add(sinfo)
		return nil
	}
	if err := r.updateResourceExport(resName, ctx, req, &re, existResExport); err != nil {
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
	re mcsv1alpha1.ResourceExport) error {
	kind := EndpointsKind
	epInfo := &epInfo{
		name:       ep.Name,
		namespace:  ep.Namespace,
		addressIPs: getEndPointsAddress(ep),
		ports:      getEndPointsPorts(ep),
		labels:     ep.Labels,
	}
	r.refreshResourceExport(resName, kind, nil, ep, &re)
	existResExport, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).Get(ctx, resName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("fail to get ResourceExport %s/%s, err: %v", LeaderNameSpace, resName, err)
		if !apierrors.IsNotFound(err) {
			return err
		}
		if err := r.createResourceExport(resName, ctx, req, &re); err != nil {
			return err
		}
		r.installedEps.Add(epInfo)
		return nil
	}
	if err := r.updateResourceExport(resName, ctx, req, &re, existResExport); err != nil {
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
	case ServiceKind:
		re.ObjectMeta.Name = resName
		re.Spec.Service = &mcsv1alpha1.ServiceExport{
			ServiceSpec: svc.Spec,
		}
	case EndpointsKind:
		re.ObjectMeta.Name = resName
		re.Spec.Endpoints = &mcsv1alpha1.EndpointsExport{
			Subsets: ep.Subsets,
		}
	}
	return *re
}

func (r *ServiceExportReconciler) updateResourceExport(resName string,
	ctx context.Context,
	req ctrl.Request,
	newResExport *mcsv1alpha1.ResourceExport,
	existResExport *mcsv1alpha1.ResourceExport) error {
	newResExport.ObjectMeta.ResourceVersion = existResExport.ObjectMeta.ResourceVersion
	_, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).Update(ctx, newResExport, metav1.UpdateOptions{})
	if err != nil {
		klog.Infof("fail to update ResourceExport %s/%s, err:%v", LeaderNameSpace, resName, err)
		return err
	}
	// ignore status update failure?
	latestResExport, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).Get(ctx, resName, metav1.GetOptions{})
	if err != nil {
		klog.Infof("fail to get ResourceExport %v", err)
		return nil
	}
	latestResExport.Status.Conditions = []mcsv1alpha1.ResourceExportCondition{{
		Type:               mcsv1alpha1.ResourceExportSucceeded,
		Status:             corev1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(time.Now()),
		Message:            "update is successful",
	}}
	_, err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).UpdateStatus(ctx, latestResExport, metav1.UpdateOptions{})
	if err != nil {
		klog.Infof("fail to update ResourceExport's Status %s/%s,err:%v", LeaderNameSpace, resName, err)
	}
	return nil
}

func (r *ServiceExportReconciler) createResourceExport(resName string,
	ctx context.Context,
	req ctrl.Request,
	re *mcsv1alpha1.ResourceExport) error {
	klog.Infof("creating ResourceExport %s/%s", LeaderNameSpace, resName)
	_, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).Create(ctx, re, metav1.CreateOptions{})
	if err != nil {
		klog.Errorf("fail to create ResourceExport %s/%s in leader cluster,err: %v", LeaderNameSpace, resName, err)
		return err
	}
	// ignore status update failure?
	re.Status.Conditions = []mcsv1alpha1.ResourceExportCondition{{
		Type:               mcsv1alpha1.ResourceExportSucceeded,
		Status:             corev1.ConditionTrue,
		LastTransitionTime: metav1.NewTime(time.Now()),
		Message:            "creation is successful",
	}}
	new, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).Get(ctx, resName, metav1.GetOptions{})
	if err != nil {
		return nil
	}
	re.ObjectMeta.ResourceVersion = new.ObjectMeta.ResourceVersion
	_, err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(LeaderNameSpace).UpdateStatus(ctx, re, metav1.UpdateOptions{})
	if err != nil {
		klog.Infof("fail to update ResourceExport's Status %s/%s,err:%v", LeaderNameSpace, resName, err)
	}
	return nil
}

func getKey(req ctrl.Request) string {
	return req.Namespace + req.Name
}

func getResourceExportName(resName, kind string) string {
	return resName + "-" + strings.ToLower(kind)
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
