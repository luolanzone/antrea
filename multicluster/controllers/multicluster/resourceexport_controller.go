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
	"fmt"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	apiequality "k8s.io/apimachinery/pkg/api/equality"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"
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
		Client              client.Client
		Scheme              *runtime.Scheme
		AntreamcsCrdClient  antreamcsversioned.Interface
		installedResExports cache.Indexer
	}
)

const (
	// cached indexer
	resExportIndexerByNameKind = "resourceexport.by.namekind"
)

func NewResourceExportReconciler(
	Client client.Client,
	Scheme *runtime.Scheme,
	antreamcsCrdClient antreamcsversioned.Interface) *ResourceExportReconciler {
	reconciler := &ResourceExportReconciler{
		Client:              Client,
		Scheme:              Scheme,
		AntreamcsCrdClient:  antreamcsCrdClient,
		installedResExports: cache.NewIndexer(resExportIndexerKeyFunc, cache.Indexers{resExportIndexerByNameKind: resExportIndexerByNameKindFunc}),
	}
	return reconciler
}

func resExportIndexerKeyFunc(obj interface{}) (string, error) {
	re := obj.(mcsv1alpha1.ResourceExport)
	return re.Namespace + "/" + re.Name, nil
}

func resExportIndexerByNameKindFunc(obj interface{}) ([]string, error) {
	re := obj.(mcsv1alpha1.ResourceExport)
	return []string{re.Spec.Namespace + re.Spec.Name + re.Spec.ClusterID + re.Spec.Kind}, nil
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports/finalizers,verbs=update

func (r *ResourceExportReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(2).Infof("reconcile for %s", req.NamespacedName)
	var resExport mcsv1alpha1.ResourceExport
	if err := r.Client.Get(ctx, req.NamespacedName, &resExport); err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		re, exist, _ := r.installedResExports.GetByKey(req.NamespacedName.String())
		if !exist {
			klog.Infof("no matched cache for %s", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		existRe := re.(mcsv1alpha1.ResourceExport)
		reList, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(req.Namespace).List(ctx,
			metav1.ListOptions{LabelSelector: getLabelSelector(&existRe)})
		if err != nil {
			return ctrl.Result{}, err
		}

		resImportName := getResourceImportName(&existRe)
		if len(reList.Items) == 0 {
			klog.Info("cleanup ResourceImport %s/%s", req.Namespace, resImportName)
			err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Delete(ctx, resImportName, metav1.DeleteOptions{})
			if err != nil {
				klog.Errorf("fail to delete ResourceImport %s/%s, err: %v", req.Namespace, resImportName, err)
				if apierrors.IsNotFound(err) {
					r.installedResExports.Delete(re)
					return ctrl.Result{}, nil
				}
				return ctrl.Result{}, err
			}
		}

		uniqueIndex := existRe.Spec.Namespace + existRe.Spec.Name + existRe.Spec.ClusterID + EndpointsKind
		epExportCache, err := r.installedResExports.ByIndex(resExportIndexerByNameKind, uniqueIndex)
		if err != nil {
			klog.Infof("fail to get cache, err: %v", err)
			return ctrl.Result{}, err
		}
		if len(epExportCache) == 0 {
			klog.Infof("no cache in installedResExports %s", req.NamespacedName)
			return ctrl.Result{}, nil
		} else if len(epExportCache) == 1 {
			epExport := epExportCache[0].(mcsv1alpha1.ResourceExport)
			resImport, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Get(ctx, resImportName, metav1.GetOptions{})
			if err != nil {
				klog.Errorf("fail to get ResourceImport %s/%s, err: %v", req.Namespace, resImportName, err)
				return ctrl.Result{}, client.IgnoreNotFound(err)
			}
			newResImport, changed := r.refreshResourceImport(&epExport, resImport, false)
			if changed {
				_, err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Update(ctx, newResImport, metav1.UpdateOptions{})
				if err != nil {
					klog.Errorf("fail to update ResourceImport %s/%s", req.Namespace, resImportName)
					return ctrl.Result{}, err
				}
			}
			r.installedResExports.Delete(epExport)
		} else {
			// this should never happen.
			return ctrl.Result{}, fmt.Errorf("duplicate Endpoint type of ResourceExport")
		}
	}

	importedResNameSpace := resExport.Labels["sourceNamespace"]
	importedResName := resExport.Labels["sourceName"]
	importedClusterID := resExport.Labels["sourceClusterID"]
	resImportName := getResourceImportName(&resExport)

	var createResImport bool
	existResImport, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Get(ctx, resImportName, metav1.GetOptions{})
	if err != nil {
		klog.Errorf("fail to get ResourceImport %s/%s, err: %v", req.Namespace, resImportName, err)
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		existResImport = &mcsv1alpha1.ResourceImport{
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
		createResImport = true
	}

	var latestResImport *mcsv1alpha1.ResourceImport
	resImport, changed := r.refreshResourceImport(&resExport, existResImport, createResImport)
	if changed {
		if createResImport {
			latestResImport, err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Create(ctx, resImport, metav1.CreateOptions{})
		} else {
			latestResImport, err = r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).Update(ctx, resImport, metav1.UpdateOptions{})
		}
	} else {
		return ctrl.Result{}, nil
	}

	if err != nil {
		if createResImport {
			klog.Errorf("fail to create ResourceImport %s/%s,err: %v", req.Namespace, resImportName, err)
			return ctrl.Result{}, err
		} else {
			klog.Errorf("fail to update ResourceImport %s/%s, err: %v", req.Namespace, resImportName, err)
			return ctrl.Result{}, err
		}
	}

	_, exist, _ := r.installedResExports.GetByKey(req.NamespacedName.String())
	if exist {
		r.installedResExports.Update(resExport)
	} else {
		r.installedResExports.Add(resExport)
	}

	updatedStatus := existResImport.DeepCopy().Status
	var message string
	if createResImport {
		message = "creation is successful"
	} else {
		message = "update is successful"
	}

	//TODO: should ResourceImport status be updated only by member cluster to tell the leader the service import status?
	updatedStatus.ClusterStatuses = append(updatedStatus.ClusterStatuses, mcsv1alpha1.ResourceImportClusterStatus{
		ClusterID: importedClusterID,
		Conditions: []mcsv1alpha1.ResourceImportCondition{{
			Type:               mcsv1alpha1.ResourceImportSucceeded,
			Status:             corev1.ConditionTrue,
			LastTransitionTime: metav1.NewTime(time.Now()),
			Message:            message,
		}},
	})
	latestResImport.Status = updatedStatus
	if _, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceImports(req.Namespace).UpdateStatus(ctx, latestResImport, metav1.UpdateOptions{}); err != nil {
		klog.Errorf("fail to update ResourceImport Status %s/%s, err: %v", req.Namespace, resImportName, err)
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (r *ResourceExportReconciler) refreshResourceImport(
	resExport *mcsv1alpha1.ResourceExport,
	resImport *mcsv1alpha1.ResourceImport,
	createResImport bool) (*mcsv1alpha1.ResourceImport, bool) {
	newResImport := resImport.DeepCopy()
	switch resExport.Spec.Kind {
	case ServiceKind:
		newResImport.Spec.Kind = ServiceImportKind
		if createResImport {
			newResImport.Spec.ServiceImport = &mcs.ServiceImport{
				Spec: mcs.ServiceImportSpec{
					Ports: svcPortsConverter(resExport.Spec.Service.ServiceSpec.Ports),
					Type:  mcs.ClusterSetIP,
					IPs:   []string{},
				},
			}
			return newResImport, true
		}
		convertedPorts := svcPortsConverter(resExport.Spec.Service.ServiceSpec.Ports)
		if !apiequality.Semantic.DeepEqual(newResImport.Spec.ServiceImport.Spec.Ports, convertedPorts) {
			klog.Infof("new ResourceExport ports %v doesn't match with ResourceImport Ports %v,skip it", resExport.Spec.Service.ServiceSpec.Ports, newResImport.Spec.ServiceImport.Spec.Ports)
			// TODO: update ResourceExport status to reflect port collision?
		}
		return newResImport, false
	case EndpointsKind:
		resImport.Spec.Kind = EndpointsKind
		if createResImport {
			resImport.Spec.Endpoints = &mcsv1alpha1.EndpointsImport{
				Subsets: resExport.Spec.Endpoints.Subsets,
			}
			return newResImport, true
		}
		// check all mateched Endpoints ResourceExport and generate a new EndpointSubset
		var newSubsets []corev1.EndpointSubset
		reList, err := r.AntreamcsCrdClient.MulticlusterV1alpha1().ResourceExports(resExport.Namespace).List(context.TODO(),
			metav1.ListOptions{
				LabelSelector: getLabelSelector(resExport),
			})
		if err != nil {
			klog.Errorf("fail to list ResourceExports %v, no update to exist ResourceImport", err)
			return newResImport, false
		}
		for _, re := range reList.Items {
			newSubsets = append(newSubsets, re.Spec.Endpoints.Subsets...)
		}
		newResImport.Spec.Endpoints = &mcsv1alpha1.EndpointsImport{Subsets: newSubsets}
		return newResImport, true
	}
	return newResImport, false
}

// SetupWithManager sets up the controller with the Manager.
func (r *ResourceExportReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mcsv1alpha1.ResourceExport{}).
		Complete(r)
}

func getLabelSelector(resExport *mcsv1alpha1.ResourceExport) string {
	return "sourceNamespace=" + resExport.Spec.Namespace + ",sourceName=" + resExport.Spec.Name + ",sourceKind=" + resExport.Spec.Kind
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
