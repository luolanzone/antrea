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
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	utilruntime "k8s.io/apimachinery/pkg/util/runtime"
	k8sscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2/klogr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	k8smcsapi "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
)

var (
	logger = klogr.New().WithName("controllers")

	localClusterID   = "cluster-a"
	leaderNamespace  = "default"
	svcResImportName = "default-nginx-service"
	epResImportName  = "default-nginx-endpoints"

	svcImportReq = ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: leaderNamespace,
		Name:      svcResImportName,
	}}
	epImportReq = ctrl.Request{NamespacedName: types.NamespacedName{
		Namespace: leaderNamespace,
		Name:      epResImportName,
	}}

	ctx    = context.Background()
	scheme = runtime.NewScheme()

	svcResImport = &mcsv1alpha1.ResourceImport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: leaderNamespace,
			Name:      svcResImportName,
		},
		Spec: mcsv1alpha1.ResourceImportSpec{
			Namespace: "default",
			Name:      "nginx",
			Kind:      "ServiceImport",
			ServiceImport: &k8smcsapi.ServiceImport{
				Spec: k8smcsapi.ServiceImportSpec{
					Ports: []k8smcsapi.ServicePort{
						{
							Name:     "http",
							Protocol: corev1.ProtocolTCP,
							Port:     80,
						},
					},
				},
			},
		},
	}
	epSubset = []corev1.EndpointSubset{
		{
			Addresses: []corev1.EndpointAddress{
				{
					IP:       "192.168.17.11",
					Hostname: "pod1",
				},
			},
			Ports: []corev1.EndpointPort{
				{
					Name:     "http",
					Port:     80,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}
	epResImport = &mcsv1alpha1.ResourceImport{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: leaderNamespace,
			Name:      epResImportName,
		},
		Spec: mcsv1alpha1.ResourceImportSpec{
			Namespace: "default",
			Name:      "nginx",
			Kind:      "Endpoints",
			Endpoints: &mcsv1alpha1.EndpointsImport{
				Subsets: epSubset,
			},
		},
	}
)

func init() {
	utilruntime.Must(mcsv1alpha1.AddToScheme(scheme))
	utilruntime.Must(k8smcsapi.AddToScheme(scheme))
	utilruntime.Must(k8sscheme.AddToScheme(scheme))
}

func TestResourceImportReconciler_handleCreateEvent(t *testing.T) {
	remoteMgr := NewRemoteClusterManager("test-clusterset", logger, common.ClusterID(localClusterID))
	remoteMgr.Start()

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	fakeRemoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(svcResImport, epResImport).Build()
	localClusterMgr := NewLocalClusterManager(fakeClient, common.ClusterID(localClusterID), "default", logger)
	remoteCluster := NewFakeRemoteCluster(scheme, &remoteMgr, fakeRemoteClient, "leader-cluster", "default")

	tests := []struct {
		name    string
		objType string
		req     ctrl.Request
	}{
		{
			name:    "import service",
			objType: "service",
			req:     svcImportReq,
		},
		{
			name:    "import endpoints",
			objType: "endpoints",
			req:     epImportReq,
		},
	}

	r := NewResourceImportReconciler(fakeClient, scheme, &localClusterMgr, remoteCluster)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.Reconcile(ctx, tt.req); err != nil {
				t.Errorf("ResourceImport Reconciler should handle create event successfully but got error = %v", err)
			} else {
				switch tt.objType {
				case "service":
					svc := &corev1.Service{}
					if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nginx"}, svc); err != nil {
						t.Errorf("ResourceImport Reconciler should import a Service successfully but got error = %v", err)
					}
				case "endpoints":
					ep := &corev1.Endpoints{}
					if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nginx"}, ep); err != nil {
						t.Errorf("ResourceImport Reconciler should import an Endpoint successfully but got error = %v", err)
					}
				}
			}
		})
	}
}

func TestResourceImportReconciler_handleDeleteEvent(t *testing.T) {
	remoteMgr := NewRemoteClusterManager("test-clusterset", logger, common.ClusterID(localClusterID))
	remoteMgr.Start()

	existSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "nginx",
		},
	}
	existEp := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "nginx",
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existSvc, existEp).Build()
	fakeRemoteClient := fake.NewClientBuilder().WithScheme(scheme).Build()
	localClusterMgr := NewLocalClusterManager(fakeClient, common.ClusterID(localClusterID), "default", logger)
	remoteCluster := NewFakeRemoteCluster(scheme, &remoteMgr, fakeRemoteClient, "leader-cluster", "default")

	tests := []struct {
		name    string
		objType string
		req     ctrl.Request
	}{
		{
			name:    "delete service",
			objType: "service",
			req:     svcImportReq,
		},
		{
			name:    "delete endpoints",
			objType: "endpoints",
			req:     epImportReq,
		},
	}

	r := NewResourceImportReconciler(fakeClient, scheme, &localClusterMgr, remoteCluster)
	r.installedResImports.Add(*svcResImport)
	r.installedResImports.Add(*epResImport)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.Reconcile(ctx, tt.req); err != nil {
				t.Errorf("ResourceImport Reconciler should handle delete event successfully but got error = %v", err)
			} else {
				switch tt.objType {
				case "service":
					svc := &corev1.Service{}
					if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nginx"}, svc); !apierrors.IsNotFound(err) {
						t.Errorf("ResourceImport Reconciler should delete a Service successfully but got error = %v", err)
					}
				case "endpoints":
					ep := &corev1.Endpoints{}
					if err := fakeClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "nginx"}, ep); !apierrors.IsNotFound(err) {
						t.Errorf("ResourceImport Reconciler should delete an Endpoint successfully but got error = %v", err)
					}
				}
			}
		})
	}
}

func TestResourceImportReconciler_handleUpdateEvent(t *testing.T) {
	remoteMgr := NewRemoteClusterManager("test-clusterset", logger, common.ClusterID(localClusterID))
	remoteMgr.Start()

	nginxPorts := []corev1.ServicePort{
		{
			Protocol: corev1.ProtocolTCP,
			Port:     80,
		},
	}
	existSvc := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "nginx",
			Labels:    map[string]string{common.AntreaMCSAutoGenAnnotation: "true"},
		},
		Spec: corev1.ServiceSpec{
			Ports: nginxPorts,
		},
	}
	existEp := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "nginx",
			Labels:    map[string]string{common.AntreaMCSAutoGenAnnotation: "true"},
		},
		Subsets: epSubset,
	}

	svcWithoutAutoLabel := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "nginx",
		},
		Spec: corev1.ServiceSpec{
			Ports: nginxPorts,
		},
	}

	epWithoutAutoLabel := &corev1.Endpoints{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "nginx",
		},
		Subsets: epSubset,
	}
	newPorts := []k8smcsapi.ServicePort{
		{
			Name:     "http",
			Protocol: corev1.ProtocolTCP,
			Port:     8080,
		},
	}
	newSubsets := []corev1.EndpointSubset{
		{
			Addresses: []corev1.EndpointAddress{
				{
					IP:       "192.168.17.12",
					Hostname: "pod2",
				},
			},
			Ports: []corev1.EndpointPort{
				{
					Name:     "http",
					Port:     8080,
					Protocol: corev1.ProtocolTCP,
				},
			},
		},
	}

	updatedSvcResImport := svcResImport.DeepCopy()
	updatedSvcResImport.Spec.ServiceImport = &k8smcsapi.ServiceImport{
		Spec: k8smcsapi.ServiceImportSpec{
			Ports: newPorts,
		},
	}
	updatedEpResImport := epResImport.DeepCopy()
	updatedEpResImport.Spec.Endpoints = &mcsv1alpha1.EndpointsImport{
		Subsets: newSubsets,
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(existSvc, existEp, svcWithoutAutoLabel, epWithoutAutoLabel).Build()
	fakeRemoteClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(updatedEpResImport, updatedSvcResImport).Build()
	localClusterMgr := NewLocalClusterManager(fakeClient, common.ClusterID(localClusterID), "default", logger)
	remoteCluster := NewFakeRemoteCluster(scheme, &remoteMgr, fakeRemoteClient, "leader-cluster", "default")

	tests := []struct {
		name             string
		objType          string
		req              ctrl.Request
		resNamespaceName types.NamespacedName
		expectedSvcPorts []corev1.ServicePort
		expectedSubset   []corev1.EndpointSubset
	}{
		{
			name:             "update service",
			objType:          "service",
			req:              svcImportReq,
			resNamespaceName: types.NamespacedName{Namespace: "default", Name: "nginx"},
			expectedSvcPorts: []corev1.ServicePort{
				{
					Protocol: corev1.ProtocolTCP,
					Port:     8080,
				},
			},
		},
		{
			name:             "update endpoints",
			objType:          "endpoints",
			req:              epImportReq,
			resNamespaceName: types.NamespacedName{Namespace: "default", Name: "nginx"},
			expectedSubset:   newSubsets,
		},
		{
			name:    "skip update a service without mcs label",
			objType: "service",
			req: ctrl.Request{NamespacedName: types.NamespacedName{
				Namespace: leaderNamespace,
				Name:      "kube-system-nginx-service",
			}},
			resNamespaceName: types.NamespacedName{Namespace: "kube-system", Name: "nginx"},
			expectedSvcPorts: nginxPorts,
		},
		{
			name:    "skip update an endpoint without mcs label",
			objType: "endpoints",
			req: ctrl.Request{NamespacedName: types.NamespacedName{
				Namespace: leaderNamespace,
				Name:      "kube-system-nginx-endpoints",
			}},
			resNamespaceName: types.NamespacedName{Namespace: "kube-system", Name: "nginx"},
			expectedSubset:   epSubset,
		},
	}

	r := NewResourceImportReconciler(fakeClient, scheme, &localClusterMgr, remoteCluster)
	r.installedResImports.Add(*svcResImport)
	r.installedResImports.Add(*epResImport)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := r.Reconcile(ctx, tt.req); err != nil {
				t.Errorf("ResourceImport Reconciler should handle update event successfully but got error = %v", err)
			} else {
				switch tt.objType {
				case "service":
					svc := &corev1.Service{}
					if err := fakeClient.Get(ctx, tt.resNamespaceName, svc); err != nil {
						t.Errorf("ResourceImport Reconciler should update a Service successfully but got error = %v", err)
					} else {
						if !reflect.DeepEqual(svc.Spec.Ports, tt.expectedSvcPorts) {
							t.Errorf("expected Service ports are %v but got %v", tt.expectedSvcPorts, svc.Spec.Ports)
						}
					}
				case "endpoints":
					ep := &corev1.Endpoints{}
					if err := fakeClient.Get(ctx, tt.resNamespaceName, ep); err != nil {
						t.Errorf("ResourceImport Reconciler should update an Endpoint successfully but got error = %v", err)
					} else {
						if !reflect.DeepEqual(ep.Subsets, tt.expectedSubset) {
							t.Errorf("expected Service ports are %v but got %v", tt.expectedSubset, ep.Subsets)
						}
					}
				}
			}
		})
	}
}
