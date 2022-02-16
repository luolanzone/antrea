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
	"fmt"
	"regexp"
	"time"

	"github.com/stretchr/testify/assert"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
	teIndexerBySubnets     = "subnets"
	defaultRequeueDuration = 5 * time.Second
)

type (
	// GatewayNodeReconciler is for member cluster only.
	// It will create a TunnelEndpoint object if a Node has an annotation `multicluster.antrea.io/gateway:true`
	GatewayNodeReconciler struct {
		client.Client
		Scheme                  *runtime.Scheme
		remoteCommonAreaManager *commonarea.RemoteCommonAreaManager
		namespace               string
		localClusterID          string
		installedTE             cache.Indexer
		clusterIPCIDR           string
	}
)

// GatewayNodeReconciler will watch Node object change and create a
// corresponding TunnelEndpoint if the Node has an annotation
// `multicluster.antrea.io/gateway:true`, add Node's PodCIDR to
// TunnelEndpoint's subsets if it's a new Node, or update TunnelEndpoint's
// subsets if Node's PodCIDR is changed.
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
		installedTE: cache.NewIndexer(tunnelEndpointKeyFunc, cache.Indexers{
			teIndexerBySubnets: tunnelEndpointIndexerBySubnetsFunc,
		}),
		clusterIPCIDR: serviceCIDR,
	}
	return reconciler
}

func tunnelEndpointKeyFunc(obj interface{}) (string, error) {
	return obj.(*mcsv1alpha1.TunnelEndpoint).Name, nil
}

func tunnelEndpointIndexerBySubnetsFunc(obj interface{}) ([]string, error) {
	return obj.(*mcsv1alpha1.TunnelEndpoint).Spec.Subnets, nil
}

//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints,verbs=get;list;watch;create;update;patch;delete
//+kubebuilder:rbac:groups="",resources=nodes,verbs=get;list;watch;
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints/status,verbs=get;update;patch
//+kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=tunnelendpoints/finalizers,verbs=update

func (r *GatewayNodeReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	klog.InfoS("reconciling Node", "node", req.Name)

	if *r.remoteCommonAreaManager == nil {
		klog.InfoS("clusterset has not been initialized properly, no remote cluster manager")
		return ctrl.Result{Requeue: true}, nil
	}
	r.localClusterID = string((*r.remoteCommonAreaManager).GetLocalClusterID())
	if len(r.localClusterID) == 0 {
		klog.InfoS("localClusterID is not initialized, skip reconcile")
		return ctrl.Result{Requeue: true}, nil
	}

	subnets, err := r.getSubnets(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	// When the Node is a Gateway Node:
	//  Delete corresponding TunnelEndpoint
	//  Update corresponding TunnelEndpoint if there is Node's PoDCIDR is updated
	//  Create a new TunnelEndpoint if there is no existing TunnelEndpoint or update existing
	//  TunnelEndpoint if there is Node's PoDCIDR is updated or do nothing.
	// When the Node is a general Node:
	//  Update tunnelEndpoint if there is Node's PoDCIDRs are changed: remove or update PodCIDR from TunnelEndpoint subnets.
	var node corev1.Node
	if err := r.Client.Get(ctx, req.NamespacedName, &node); err != nil {
		klog.ErrorS(err, "Failed to get Node", "node", req.Name)
		if apierrors.IsNotFound(err) {
			tes := &mcsv1alpha1.TunnelEndpointList{}
			if err := r.Client.List(ctx, tes, &client.ListOptions{}); err != nil {
				return ctrl.Result{}, err
			}
			var existingTE mcsv1alpha1.TunnelEndpoint
			for _, n := range tes.Items {
				existingTE = n
				if existingTE.Name == req.Name {
					if err = r.deleteTunnelEndpoint(ctx, &existingTE); err != nil {
						return ctrl.Result{}, err
					}
					r.installedTE.Delete(&existingTE)
				}
			}
			klog.InfoS("Updating TunnelEndpoint subnets when a general Node is removed", "node", req.NamespacedName)
			existingTE.Spec.Subnets = subnets
			if err := r.Client.Update(ctx, &existingTE, &client.UpdateOptions{}); err != nil {
				return ctrl.Result{}, err
			}
			r.installedTE.Update(&existingTE)
		}
		return ctrl.Result{}, err
	}

	tes := &mcsv1alpha1.TunnelEndpointList{}
	if err := r.Client.List(ctx, tes, &client.ListOptions{}); err != nil {
		return ctrl.Result{}, err
	}

	teSize := len(tes.Items)
	if _, ok := node.Annotations[common.GatewayNodeAnnotation]; !ok {
		// General Node create or update -> update TunnelEndpoint or do nothing if there is no Gateway Node yet.
		switch teSize {
		case 0:
			return ctrl.Result{}, nil
		case 1:
			te := tes.Items[0]
			// the annotation of Node is removed, then we need to delete correspoding TunnelEndpoint.
			if te.Name == node.Name {
				if err = r.deleteTunnelEndpoint(ctx, &te); err != nil {
					return ctrl.Result{}, err
				}
				return ctrl.Result{}, nil
			}
			te.Spec.Subnets = subnets
			if err := r.updateTunnelEndpoint(ctx, &te, node); err != nil {
				return ctrl.Result{}, err
			}
			return ctrl.Result{}, nil
		default:
			// this should never happen
			return ctrl.Result{}, fmt.Errorf("there is more than one TunnelEndpoint, please check your Gateway Node")
		}
	}

	nodeHostname := node.Labels["kubernetes.io/hostname"]
	subnets, err = r.getSubnets(ctx)
	if err != nil {
		return ctrl.Result{}, err
	}
	switch teSize {
	case 0:
		te := &mcsv1alpha1.TunnelEndpoint{
			ObjectMeta: metav1.ObjectMeta{
				Name:      node.Name,
				Namespace: r.namespace,
			},
			Spec: mcsv1alpha1.TunnelEndpointSpec{
				Role:      "leader",
				ClusterID: r.localClusterID,
				Hostname:  nodeHostname,
				Subnets:   subnets,
			},
		}
		te.Finalizers = []string{common.TunnelEndpointFinalizer}
		// Create a TunnelEndpoint when there is a Gateway Node
		if err := r.Client.Create(ctx, te, &client.CreateOptions{}); err != nil {
			return ctrl.Result{}, err
		}
		klog.InfoS("A new TunnelEndpoint is created for the Gateway Node", "tunnelendpoint", klog.KObj(te), "node", req.NamespacedName)
		r.installedTE.Add(te)
	case 1:
		// Update a TunnelEndpoint
		if tes.Items[0].Name != node.Name {
			return ctrl.Result{}, fmt.Errorf("you can't assign a new Gateway node when there is already one, please remove Node %s's annotation "+common.GatewayNodeAnnotation+" if you'd like to change the Gateway", tes.Items[0].Name)
		}
		te := tes.Items[0]
		te.Spec.Subnets = subnets
		if err = r.updateTunnelEndpointIP(&te, node); err != nil {
			return ctrl.Result{RequeueAfter: defaultRequeueDuration}, err
		}
		if err := r.updateTunnelEndpoint(ctx, &te, node); err != nil {
			return ctrl.Result{}, err
		}
	default:
		// this should never happen
		return ctrl.Result{}, fmt.Errorf("there is more than one TunnelEndpoint, please check your Gateway Node setting")
	}
	return ctrl.Result{}, nil
}

func (r *GatewayNodeReconciler) deleteTunnelEndpoint(ctx context.Context, te *mcsv1alpha1.TunnelEndpoint) error {
	var err error
	// Delete corresponding TunnelEndpoint due to Node is removed
	if err = r.Client.Delete(ctx, te, &client.DeleteOptions{}); err != nil {
		return err
	}
	klog.InfoS("TunnelEndpoint is deleted", "tunnelendpoint", te.Namespace+"/"+te.Name)
	if err = r.installedTE.Delete(te); err != nil {
		klog.ErrorS(err, "Failed to delete installed TunnelEndpoint", "tunnelendpoint", klog.KObj(te))
	}
	return nil
}

func (r *GatewayNodeReconciler) updateTunnelEndpointIP(te *mcsv1alpha1.TunnelEndpoint, node corev1.Node) error {
	for _, addr := range node.Status.Addresses {
		if addr.Type == corev1.NodeExternalIP {
			te.Spec.PublicIP = addr.Address
		}
		if addr.Type == corev1.NodeInternalIP {
			te.Spec.PrivateIP = addr.Address
		}
	}
	if te.Spec.PublicIP == "" && te.Spec.PrivateIP == "" {
		klog.InfoS("No valid public or private IP for Node", "node", node.Name)
		return fmt.Errorf("no valid IP for Node %s", node.Name)
	}
	return nil
}

func (r *GatewayNodeReconciler) updateTunnelEndpoint(ctx context.Context,
	te *mcsv1alpha1.TunnelEndpoint, node corev1.Node) error {
	// Update a TunnelEndpoint
	teObj, isInstalled, _ := r.installedTE.GetByKey(te.Name)
	if isInstalled {
		teInstalled := teObj.(*mcsv1alpha1.TunnelEndpoint)
		if elementsMatch(te.Spec.Subnets, teInstalled.Spec.Subnets) && teInstalled.Spec.Hostname == te.Spec.Hostname &&
			teInstalled.Spec.PrivateIP == te.Spec.PrivateIP && teInstalled.Spec.PublicIP == te.Spec.PublicIP {
			klog.InfoS("The TunnelEndpoint is no change, skip reconciling for Node", "tunnelendpoint",
				klog.KObj(te), "node", node.Name)
			return nil
		}
	}
	if err := r.Client.Update(ctx, te, &client.UpdateOptions{}); err != nil {
		return err
	}
	klog.InfoS("The TunnelEndpoint is updated for a Node", "tunneledpoint", klog.KObj(te), "node", node.Name)
	r.installedTE.Update(te)
	return nil
}

// getSubnets get all CIDRs being used in a cluster including PodCIDRs of all Nodes and Service ClusterIP CIDR.
func (r *GatewayNodeReconciler) getSubnets(ctx context.Context) ([]string, error) {
	clusterPodCIDRs, err := r.getClusterPodCIDRs(ctx)
	if err != nil {
		klog.InfoS("Fail to get cluster PoDCIDRs")
		return nil, err
	}
	if r.clusterIPCIDR == "" {
		r.clusterIPCIDR, err = r.findClusterIPRangeFromServiceCreation(ctx)
		if err != nil {
			return nil, fmt.Errorf("fail to get Service ClusterIP range: %v, you can set the 'serviceCIDR' config as an alternative", err)
		}
	}
	return append(clusterPodCIDRs, r.clusterIPCIDR), nil
}

// SetupWithManager sets up the controller with the Manager.
func (r *GatewayNodeReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&corev1.Node{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
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
