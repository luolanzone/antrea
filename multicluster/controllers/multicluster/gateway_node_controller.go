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
	"regexp"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/intstr"
	"k8s.io/client-go/tools/cache"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	mcsv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/multicluster/controllers/multicluster/commonarea"
)

const (
	nodeIndexerByHostname = "hostname"
)

type (
	// It will create a TunnelEndpoint object if a Node has an annotation `antrea.io/gateway:true`
	GatewayNodeReconciler struct {
		client.Client
		Scheme                  *runtime.Scheme
		remoteCommonAreaManager *commonarea.RemoteCommonAreaManager
		namespace               string
		localClusterID          string
		installedNodes          cache.Indexer
		clusterIPCIDR           string
	}

	nodeInfo struct {
		name      string
		cidrs     []string
		privateIP string
		publicIP  string
		hostname  string
	}
)

// GatewayNodeReconciler will watch Node object change and create a
// corresponding TunnelEndpoint if the Node has an annotation
// `multicluster.antrea.io/gateway:true`
func NewGatewayNodeReconciler(
	Client client.Client,
	Scheme *runtime.Scheme,
	namespace string,
	remoteCommonAreaManager *commonarea.RemoteCommonAreaManager,
	serviceCIDR string) *GatewayNodeReconciler {
	reconciler := &GatewayNodeReconciler{
		Client:                  Client,
		Scheme:                  Scheme,
		namespace:               namespace,
		remoteCommonAreaManager: remoteCommonAreaManager,
		installedNodes: cache.NewIndexer(nodeInfoKeyFunc, cache.Indexers{
			nodeIndexerByHostname: nodeIndexerByHostnameFunc,
		}),
		clusterIPCIDR: serviceCIDR,
	}
	return reconciler
}

func nodeInfoKeyFunc(obj interface{}) (string, error) {
	node := obj.(*nodeInfo)
	return node.name, nil
}

func nodeIndexerByHostnameFunc(obj interface{}) ([]string, error) {
	return []string{obj.(*nodeInfo).hostname}, nil
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints/finalizers,verbs=update

func (r *GatewayNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.V(2).InfoS("reconciling Node", "node", req.NamespacedName)

	if *r.remoteCommonAreaManager == nil {
		klog.InfoS("clusterset has not been initialized properly, no remote cluster manager")
		return ctrl.Result{Requeue: true}, nil
	}
	r.localClusterID = string((*r.remoteCommonAreaManager).GetLocalClusterID())
	if len(r.localClusterID) == 0 {
		klog.InfoS("localClusterID is not initialized, skip reconcile")
		return ctrl.Result{Requeue: true}, nil
	}

	var node corev1.Node
	if err := r.Client.Get(ctx, req.NamespacedName, &node); err != nil {
		if apierrors.IsNotFound(err) {
			// Clean up TunnelEndpoint if Gateway Node is deleted.
			klog.InfoS("deleting TunnelEndpoint for Node", "node", req.NamespacedName)
		}
		return ctrl.Result{}, nil
	}

	if _, ok := node.Annotations[common.GatewayNodeAnnotation]; !ok {
		klog.InfoS("Skip reconciling due to it's not a Gateway Node", "node", req.NamespacedName)
		// If it's not a Gateway node change, may need to update the local TunnelEnpoint's Subnets
		return ctrl.Result{}, nil
	}

	nodeHostname := node.Labels["kubernetes.io/hostname"]
	// TODO: retrieve ClusterCIDR from Pod directly when https://github.com/kubernetes/enhancements/issues/2593 is resolved.
	clusterPodCIDRs, err := r.getClusterPodCIDRs(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	if r.clusterIPCIDR == "" {
		r.clusterIPCIDR, err = r.findClusterIPRangeFromServiceCreation(ctx)
		if err != nil {
			return ctrl.Result{}, fmt.Errorf("fail to get service IP range: %v, you can set the 'serviceCIDR' config as an alternative", err)
		}
	}
	subnets := append(clusterPodCIDRs, r.clusterIPCIDR)
	te := &mcsv1alpha1.TunnelEndpoint{
		ObjectMeta: metav1.ObjectMeta{
			Name:      nodeHostname,
			Namespace: r.namespace,
		},
		Spec: mcsv1alpha1.TunnelEndpointSpec{
			Role:      "leader",
			ClusterID: r.localClusterID,
			Hostname:  nodeHostname,
			Subnets:   subnets,
		},
	}
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeExternalIP {
			te.Spec.PublicIP = addr.Address
		}
		if addr.Type == corev1.NodeInternalIP {
			te.Spec.PrivateIP = addr.Address
		}
	}
	ndInfo := &nodeInfo{
		name:      node.Name,
		cidrs:     subnets,
		privateIP: te.Spec.PrivateIP,
		publicIP:  te.Spec.PublicIP,
		hostname:  te.Spec.Hostname,
	}
	existTunnelEP := &mcsv1alpha1.TunnelEndpoint{}
	teNamespaced := types.NamespacedName{Namespace: r.namespace, Name: nodeHostname}
	err = r.Client.Get(ctx, teNamespaced, existTunnelEP)
	if err != nil {
		if !apierrors.IsNotFound(err) {
			return ctrl.Result{}, err
		}
		// Create a TunnelEndpoint if a Node has an annotation `multicluster.antrea.io/gateway:true`
		if err := r.Client.Create(ctx, te, &client.CreateOptions{}); err != nil {
			return ctrl.Result{}, err
		}
		klog.InfoS("A new TunnelEndpoint is created for Node", "tunneledpoint", teNamespaced.String(), "node", req.NamespacedName)
		r.installedNodes.Add(ndInfo)
	}

	// Update a TunnelEndpoint if a Node has an annotation `multicluster.antrea.io/gateway:true`
	nInfoObj, nodeInstalled, _ := r.installedNodes.GetByKey(req.Name)
	if nodeInstalled {
		nInfo := nInfoObj.(*nodeInfo)
		if elementsMatch(nInfo.cidrs, subnets) && nInfo.hostname == te.Spec.Hostname &&
			nInfo.privateIP == te.Spec.PrivateIP && nInfo.publicIP == te.Spec.PublicIP {
			klog.InfoS("The TunnelEndpoint is no change, skip reconciling for Node", "tunnelendpoint", teNamespaced.String(), "node", req.NamespacedName)
			return ctrl.Result{}, nil
		}
	}
	te.ResourceVersion = existTunnelEP.ResourceVersion
	if err := r.Client.Update(ctx, te, &client.UpdateOptions{}); err != nil {
		return ctrl.Result{}, err
	}
	r.installedNodes.Update(ndInfo)
	klog.InfoS("The TunnelEndpoint is updated for Node", "tunneledpoint", teNamespaced.String(), "node", req.NamespacedName)
	return ctrl.Result{}, nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: common.DefaultWorkerCount,
		}).
		Complete(r)
}

// getClusterCIDRs collects CIDRs used in all Nodes.
func (r *GatewayNodeReconciler) getClusterPodCIDRs(ctx context.Context) ([]string, error) {
	nodes := &corev1.NodeList{}
	if err := r.List(ctx, nodes, &client.ListOptions{}); err != nil {
		return nil, err
	}
	var clusterCIDRs []string
	for _, i := range nodes.Items {
		clusterCIDRs = append(clusterCIDRs, i.Spec.PodCIDR)
	}
	return clusterCIDRs, nil
}

// Copy from submariner operator repo with a few minor changes.
// TODO: support invalid Service creation in dual stack cluster?
func (r *GatewayNodeReconciler) findClusterIPRangeFromServiceCreation(ctx context.Context) (string, error) {
	// find service cidr based on https://stackoverflow.com/questions/44190607/how-do-you-find-the-cluster-service-cidr-of-a-kubernetes-cluster
	invalidSvcSpec := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "invalid-svc",
			Namespace: r.namespace,
		},
		Spec: corev1.ServiceSpec{
			ClusterIP: "1.1.1.1",
			Ports: []corev1.ServicePort{
				{
					Port: 443,
					TargetPort: intstr.IntOrString{
						IntVal: 443,
					},
				},
			},
		},
	}

	err := r.Create(ctx, invalidSvcSpec, &client.CreateOptions{})

	// creating invalid Service didn't fail as expected
	if err == nil {
		return "", fmt.Errorf("could not determine the Service IP range via Service creation - " +
			"expected a specific error but none was returned")
	}

	return parseServiceCIDRFrom(err.Error())
}

// Copy from submariner operator repo.
func parseServiceCIDRFrom(msg string) (string, error) {
	// expected msg is below:
	//   "The Service \"invalid-svc\" is invalid: spec.clusterIPs: Invalid value: []string{\"1.1.1.1\"}:
	//   failed to allocated ip:1.1.1.1 with error:provided IP is not in the valid range.
	//   The range of valid IPs is 10.45.0.0/16"
	// expected matched string is below:
	//   10.45.0.0/16
	re := regexp.MustCompile(".*valid IPs is (.*)$")

	match := re.FindStringSubmatch(msg)
	if match == nil {
		return "", fmt.Errorf("could not determine the service IP range via service creation - the expected error "+
			"was not returned. The actual error was %q", msg)
	}

	// returns first matching string
	return match[1], nil
}

type dummyT struct{}

func (t dummyT) Errorf(string, ...interface{}) {}

// elementsMatch compares array ignoring the order of elements.
func elementsMatch(listA, listB interface{}) bool {
	return assert.ElementsMatch(dummyT{}, listA, listB)
}
