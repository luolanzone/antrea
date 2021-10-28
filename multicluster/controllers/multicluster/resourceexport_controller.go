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
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	mcs "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
)

type (
	// ResourceExportReconciler reconciles a ResourceExport object
	ResourceExportReconciler struct {
		client.Client
		Scheme *runtime.Scheme
	}
)

func NewResourceExportReconciler(
	Client client.Client,
	Scheme *runtime.Scheme) *ResourceExportReconciler {
	reconciler := &ResourceExportReconciler{
		Client: Client,
		Scheme: Scheme,
	}
	return reconciler
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/finalizers,verbs=update

// Reconcile is part of the main kubernetes reconciliation loop which aims to
// move the current state of the cluster closer to the desired state.
// TODO(user): Modify the Reconcile function to compare the state specified by
// the ResourceExport object against the actual cluster state, and then
// perform operations to make the cluster state reflect the state specified by
// the user.
//
// For more details, check Reconcile and its Result here:
// - https://pkg.go.dev/sigs.k8s.io/controller-runtime@v0.8.3/pkg/reconcile
func (r *ResourceExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.InfoS("reconciling ResourceExport", "resourceexport", req.NamespacedName)
	var resExport mcsv1alpha1.ResourceExport
	if err := r.Client.Get(ctx, req.NamespacedName, &resExport); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	resImportName := types.NamespacedName{
		Name:      getResourceImportName(&resExport),
		Namespace: req.Namespace,
	}

	if !resExport.DeletionTimestamp.IsZero() {
		if common.ContainsString(resExport.Finalizers, common.ResourceExportFinalizer) {
			err := r.handleDeleteEvent(ctx, resImportName, resExport)
			if err != nil {
				return ctrl.Result{}, err
			}
			resExport.SetFinalizers(common.RemoveString(resExport.Finalizers, common.ResourceExportFinalizer))
			if err := r.Client.Update(ctx, &resExport, &client.UpdateOptions{}); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, nil
	}

	createResImport, existResImport, err := r.getExistResImport(ctx, resExport, resImportName)
	if err != nil {
		return ctrl.Result{}, err
	}

	var changed bool
	resImport := &mcsv1alpha1.ResourceImport{}
	switch resExport.Spec.Kind {
	case common.ServiceKind:
		resImport, changed, err = r.updateServiceResourceImport(&resExport, existResImport, createResImport)
	case common.EndpointsKind:
		resImport, changed, err = r.updateEndpointsResourceImport(&resExport, existResImport, createResImport)
	}
	if err != nil {
		return ctrl.Result{}, err
	}

	if createResImport {
		if err = r.Client.Create(ctx, resImport, &client.CreateOptions{}); err != nil {
			klog.ErrorS(err, "fail to create ResourceImport", "resourceimport", resImportName.String())
			return ctrl.Result{}, err
		}
	}

	if changed && !createResImport {
		klog.InfoS("update ResourceImport for ResoureExport", "resourceimport", resImportName.String(), "resourceexport", req.NamespacedName)
		if err = r.handleUpdateEvent(ctx, resImportName, resImport, resExport); err != nil {
			klog.ErrorS(err, "fail to update ResourceImport", "resourceimport", resImportName.String())
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

func (r *ResourceExportReconciler) handleUpdateEvent(ctx context.Context, resImpName types.NamespacedName,
	resImport *mcsv1alpha1.ResourceImport, resExport mcsv1alpha1.ResourceExport) error {
	var err error
	if err = r.Client.Update(ctx, resImport, &client.UpdateOptions{}); err != nil {
		klog.ErrorS(err, "fail to update ResourceImport", "resourceimport", resImpName.String())
		return err
	}
	latestResImport := &mcsv1alpha1.ResourceImport{}
	err = r.Client.Get(ctx, resImpName, latestResImport)
	if err != nil {
		klog.ErrorS(err, "fail to get ResourceImport", "resourceimport", resImpName.String())
		return err
	}
	updatedStatus := latestResImport.DeepCopy().Status
	//TODO: should ResourceImport status be updated only by member cluster to tell the leader the service import status?
	updatedStatus.ClusterStatuses = append(updatedStatus.ClusterStatuses, mcsv1alpha1.ResourceImportClusterStatus{
		ClusterID: resExport.Labels["sourceClusterID"],
		Conditions: []mcsv1alpha1.ResourceImportCondition{{
			Type:               mcsv1alpha1.ResourceImportSucceeded,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.NewTime(time.Now()),
			Message:            "update is successful",
		}},
	})
	latestResImport.Status = updatedStatus
	if err := r.Client.Status().Update(ctx, latestResImport, &client.UpdateOptions{}); err != nil {
		klog.ErrorS(err, "fail to update ResourceImport Status", resImpName.String())
		return err
	}
	return nil
}

func (r *ResourceExportReconciler) handleDeleteEvent(ctx context.Context, resImportName types.NamespacedName, resExport mcsv1alpha1.ResourceExport) error {
	reList := &mcsv1alpha1.ResourceExportList{}
	err := r.Client.List(ctx, reList, &client.ListOptions{LabelSelector: getLabelSelector(&resExport)})
	if err != nil {
		return err
	}
	undeleteItems := removeDeletedItems(reList.Items)
	if len(undeleteItems) == 0 {
		err = r.cleanUpResourceImport(ctx, resImportName, resExport)
		if err != nil {
			return err
		}
		return nil
	}
	// should update ResourceImport status when one of ResourceExports is removed?
	if resExport.Spec.Kind == common.ServiceKind {
		return nil
	}
	return r.updateEndpointResourceImport(ctx, resExport, resImportName)
}

func (r *ResourceExportReconciler) cleanUpResourceImport(ctx context.Context,
	resImp types.NamespacedName, re interface{}) error {
	klog.InfoS("cleanup ResourceImport", "resourceimport", resImp.String())
	resImport := &mcsv1alpha1.ResourceImport{ObjectMeta: metav1.ObjectMeta{
		Name:      resImp.Name,
		Namespace: resImp.Namespace,
	}}
	err := r.Client.Delete(ctx, resImport, &client.DeleteOptions{})
	return client.IgnoreNotFound(err)
}

func (r *ResourceExportReconciler) updateEndpointResourceImport(ctx context.Context,
	existRe mcsv1alpha1.ResourceExport, resImpName types.NamespacedName) error {
	resImport := &mcsv1alpha1.ResourceImport{}
	err := r.Client.Get(ctx, resImpName, resImport)
	if err != nil {
		klog.ErrorS(err, "fail to get ResourceImport", "resourceimport", resImpName)
		return client.IgnoreNotFound(err)
	}
	newResImport, changed, err := r.updateEndpointsResourceImport(&existRe, resImport, false)
	if err != nil {
		return err
	}
	if changed {
		err = r.Client.Update(ctx, newResImport, &client.UpdateOptions{})
		if err != nil {
			klog.ErrorS(err, "fail to update ResourceImport", "resourceimport", resImpName)
			return err
		}
	}
	return nil
}

func (r *ResourceExportReconciler) getExistResImport(ctx context.Context,
	resExport mcsv1alpha1.ResourceExport, resImportName types.NamespacedName) (bool, *mcsv1alpha1.ResourceImport, error) {
	importedResNameSpace := resExport.Labels["sourceNamespace"]
	importedResName := resExport.Labels["sourceName"]
	var createResImport bool
	existResImport := &mcsv1alpha1.ResourceImport{}
	err := r.Client.Get(ctx, resImportName, existResImport)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			klog.ErrorS(err, "fail to get ResourceImport", "resourceimport", resImportName.String())
			return createResImport, nil, err
		}
		existResImport = &mcsv1alpha1.ResourceImport{
			ObjectMeta: metav1.ObjectMeta{
				Name:      resImportName.Name,
				Namespace: resImportName.Namespace,
			},
			Spec: mcsv1alpha1.ResourceImportSpec{
				ClusterIDs: []string{},
				Name:       importedResName,
				Namespace:  importedResNameSpace,
			},
		}
		createResImport = true
	}
	return createResImport, existResImport, nil
}

func (r *ResourceExportReconciler) updateServiceResourceImport(
	resExport *mcsv1alpha1.ResourceExport,
	resImport *mcsv1alpha1.ResourceImport,
	createResImport bool) (*mcsv1alpha1.ResourceImport, bool, error) {
	newResImport := resImport.DeepCopy()
	newResImport.Spec.Name = resExport.Spec.Name
	newResImport.Spec.Namespace = resExport.Spec.Namespace
	newResImport.Spec.Kind = common.ServiceImportKind
	if createResImport {
		newResImport.Spec.ServiceImport = &mcs.ServiceImport{
			Spec: mcs.ServiceImportSpec{
				Ports: svcPortsConverter(resExport.Spec.Service.ServiceSpec.Ports),
				Type:  mcs.ClusterSetIP,
			},
		}
		return newResImport, true, nil
	}
	// TODO: check ClusterIPs difference if it is being used in ResrouceImport later
	convertedPorts := svcPortsConverter(resExport.Spec.Service.ServiceSpec.Ports)
	if !apiequality.Semantic.DeepEqual(newResImport.Spec.ServiceImport.Spec.Ports, convertedPorts) {
		undeletedItems, err := r.getUndeletedResourceExportItems(resExport)
		if err != nil {
			klog.Errorf("fail to list ResourceExports %v, retry later", err)
			return newResImport, false, err
		}
		// when there is only one Service ResourceExport, ResourceImport should reflect the change
		// otherwise, it should skip it due to it's impossible to determine which one is desired.
		if len(undeletedItems) == 1 && undeletedItems[0].Name == resExport.Name && undeletedItems[0].Namespace == resExport.Namespace {
			newResImport.Spec.ServiceImport.Spec.Ports = convertedPorts
			return newResImport, true, nil
		} else {
			klog.Infof("new ResourceExport Ports %v don't match existing ResourceImport Ports %v, skip it",
				resExport.Spec.Service.ServiceSpec.Ports, newResImport.Spec.ServiceImport.Spec.Ports)
			// TODO: update ResourceExport status to reflect port collision?
		}
	}
	return newResImport, false, nil
}

func (r *ResourceExportReconciler) updateEndpointsResourceImport(
	resExport *mcsv1alpha1.ResourceExport,
	resImport *mcsv1alpha1.ResourceImport,
	createResImport bool) (*mcsv1alpha1.ResourceImport, bool, error) {
	newResImport := resImport.DeepCopy()
	newResImport.Spec.Name = resExport.Spec.Name
	newResImport.Spec.Namespace = resExport.Spec.Namespace
	newResImport.Spec.Kind = common.EndpointsKind
	if createResImport {
		newResImport.Spec.Endpoints = &mcsv1alpha1.EndpointsImport{
			Subsets: resExport.Spec.Endpoints.Subsets,
		}
		return newResImport, true, nil
	}
	// check all matched Endpoints ResourceExport and generate a new EndpointSubset
	newSubsets := []corev1.EndpointSubset{}
	undeleteItems, err := r.getUndeletedResourceExportItems(resExport)
	if err != nil {
		klog.Errorf("fail to list ResourceExports %v, retry later", err)
		return newResImport, false, err
	}
	for _, re := range undeleteItems {
		newSubsets = append(newSubsets, re.Spec.Endpoints.Subsets...)
	}
	newResImport.Spec.Endpoints = &mcsv1alpha1.EndpointsImport{Subsets: newSubsets}
	return newResImport, true, nil
}

func (r *ResourceExportReconciler) getUndeletedResourceExportItems(resExport *mcsv1alpha1.ResourceExport) ([]mcsv1alpha1.ResourceExport, error) {
	reList := &mcsv1alpha1.ResourceExportList{}
	err := r.Client.List(context.TODO(), reList, &client.ListOptions{
		LabelSelector: getLabelSelector(resExport),
	})
	if err != nil {
		return nil, err
	}
	return removeDeletedItems(reList.Items), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mcsv1alpha1.ResourceExport{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.DefaultWorkerCount,
		}).
		Complete(r)
}

func getLabelSelector(resExport *mcsv1alpha1.ResourceExport) labels.Selector {
	labelSelector := metav1.LabelSelector{
		MatchLabels: map[string]string{
			"sourceNamespace": resExport.Spec.Namespace,
			"sourceName":      resExport.Spec.Name,
			"sourceKind":      resExport.Spec.Kind,
		},
	}
	selector, _ := metav1.LabelSelectorAsSelector(&labelSelector)
	return selector
}

func svcPortsConverter(svcPort []corev1.ServicePort) []mcs.ServicePort {
	var mcsSP []mcs.ServicePort
	for _, v := range svcPort {
		mcsSP = append(mcsSP, mcs.ServicePort{
			Name:     strconv.Itoa(int(v.Port)) + string(v.Protocol),
			Port:     v.Port,
			Protocol: v.Protocol,
		})
	}
	return mcsSP
}

func getResourceImportName(resExport *mcsv1alpha1.ResourceExport) string {
	return resExport.Spec.Namespace + "-" + resExport.Spec.Name + "-" + strings.ToLower(resExport.Spec.Kind)
}

func removeDeletedItems(items []mcsv1alpha1.ResourceExport) []mcsv1alpha1.ResourceExport {
	undeleteItems := []mcsv1alpha1.ResourceExport{}
	for _, i := range items {
		if i.DeletionTimestamp.IsZero() {
			undeleteItems = append(undeleteItems, i)
		}
	}
	return undeleteItems
}
