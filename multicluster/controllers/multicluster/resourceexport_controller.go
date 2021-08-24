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
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	mcs "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	antreamcsversioned "antrea.io/antrea/multicluster/pkg/client/clientset/versioned"
)

type (
	// ResourceExportReconciler reconciles a ResourceExport object
	ResourceExportReconciler struct {
		Client             client.Client
		Scheme             *runtime.Scheme
		K8sClient          kubernetes.Interface
		AntreamcsCrdClient antreamcsversioned.Interface
	}
)

func NewResourceExportReconciler(
	Client client.Client,
	Scheme *runtime.Scheme,
	k8sClient kubernetes.Interface,
	antreamcsCrdClient antreamcsversioned.Interface) *ResourceExportReconciler {
	reconciler := &ResourceExportReconciler{
		Client:             Client,
		Scheme:             Scheme,
		K8sClient:          k8sClient,
		AntreamcsCrdClient: antreamcsCrdClient,
	}
	return reconciler
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/finalizers,verbs=update

func (r *ResourceExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.Infof("request namespace %s, name %s ", req.Namespace, req.Name)
	//??? ResourceImport name will be the same name as ResourceExport
	resImportName := req.Name
	var resExport mcsv1alpha1.ResourceExport
	if err := r.Client.Get(ctx, req.NamespacedName, &resExport); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Delete(ctx, req.Name, metav1.DeleteOptions{})
		if err != nil {
			klog.Errorf("fail to delete ResourceImport %s/%s, err: %v", req.Namespace, resImportName, err)
			return ctrl.Result{}, client.IgnoreNotFound(err)
		}
		return ctrl.Result{}, nil
	}

	importedResNameSpace := resExport.Labels["sourceNamespace"]
	importedResName := resExport.Labels["sourceName"]
	importedClusterID := resExport.Labels["sourceClusterID"]
	// check the kind of ResourceExport
	resImportBase := &mcsv1alpha1.ResourceImport{
		ObjectMeta: metav1.ObjectMeta{
			Name:      resImportName,
			Namespace: req.Namespace,
		},
		Spec: mcsv1alpha1.ResourceImportSpec{
			ClusterIDs: []string{},
			Name:       importedResName,
			Namespace:  importedResNameSpace,
		},
	}
	resImport := r.refreshResourceImport(&resExport, resImportBase)
	existResImport, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Get(ctx, resImportName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("fail to get ResourceImport %s/%s, err: %v", req.Namespace, resImportName, err)
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		_, err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Create(ctx, resImport, metav1.CreateOptions{})
		if err != nil {
			klog.Errorf("fail to create ResourceImport %s/%s,err: %v", req.Namespace, resImportName, err)
			return ctrl.Result{}, err
		}
		newResImport, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Get(ctx, resImportName, metav1.GetOptions{})
		if err != nil {
			klog.Errorf("fail to get ResourceImport %s/%s", req.Namespace, resImportName)
			return ctrl.Result{}, err
		}
		newResImport.Status = mcsv1alpha1.ResourceImportStatus{
			ClusterStatuses: []mcsv1alpha1.ResourceImportClusterStatus{
				{
					ClusterID: importedClusterID,
					Conditions: []mcsv1alpha1.ResourceImportCondition{{
						Type:               mcsv1alpha1.ResourceImportSucceeded,
						Status:             corev1.ConditionTrue,
						LastTransitionTime: metav1.NewTime(time.Now()),
						Message:            "creation is successful",
					}},
				},
			},
		}
		if _, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Update(ctx, newResImport, metav1.UpdateOptions{}); err != nil {
			klog.Errorf("fail to update ResourceImport %s/%s, err: %v", req.Namespace, resImportName, err)
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	updatedResImport := r.refreshResourceImport(&resExport, existResImport)
	_, err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Update(ctx, updatedResImport, metav1.UpdateOptions{})
	if err != nil {
		klog.Errorf("fail to update ResourceImport %s/%s", req.Namespace, resImportName)
		return ctrl.Result{}, err
	}
	existStatus := existResImport.DeepCopy().Status
	existStatus.ClusterStatuses = append(existStatus.ClusterStatuses, mcsv1alpha1.ResourceImportClusterStatus{
		ClusterID: importedClusterID,
		Conditions: []mcsv1alpha1.ResourceImportCondition{{
			Type:               mcsv1alpha1.ResourceImportSucceeded,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.NewTime(time.Now()),
			Message:            "update is successful",
		}},
	})
	updatedResImport.Status = existStatus
	if _, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).UpdateStatus(ctx, existResImport, metav1.UpdateOptions{}); err != nil {
		klog.Errorf("fail to update ResourceImport Status %s/%s, err: %v", req.Namespace, resImportName, err)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ResourceExportReconciler) refreshResourceImport(
	resExport *mcsv1alpha1.ResourceExport,
	resImport *mcsv1alpha1.ResourceImport) *mcsv1alpha1.ResourceImport {
	switch resExport.Spec.Kind {
	case ServiceKind:
		resImport.Spec.Kind = ServiceImportKind
		resImport.Spec.ServiceImport = &mcs.ServiceImport{
			Spec: mcs.ServiceImportSpec{
				Ports: svcPortsConverter(resExport.Spec.Service.ServiceSpec.Ports),
				Type:  mcs.ClusterSetIP,
				IPs:   []string{resExport.Spec.Service.ServiceSpec.ClusterIP},
			},
		}
	case EndpointsKind:
		resImport.Spec.Kind = EndpointsKind
		resImport.Spec.Endpoints = &mcsv1alpha1.EndpointsImport{
			Subsets: resExport.Spec.Endpoints.Subsets,
		}
	}
	return resImport
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mcsv1alpha1.ResourceExport{}).
		Complete(r)
}

func svcPortsConverter(svcPort []corev1.ServicePort) []mcs.ServicePort {
	var mcsSP []mcs.ServicePort
	for _, v := range svcPort {
		mcsSP = append(mcsSP, mcs.ServicePort{
			Name:        v.Name,
			Port:        v.Port,
			Protocol:    v.Protocol,
			AppProtocol: v.AppProtocol,
		})
	}
	return mcsSP
}
