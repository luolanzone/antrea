/*
Copyright 2023 Antrea Authors.

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

package leader

import (
	"context"
	"fmt"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/sets"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/retry"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
)

const indexKey = "spec.clusterID"

var getResourceExportsByClusterIDFunc = getResourceExportsByClusterID

// cleanUpRetry is the retry when the cleanUpStaleResourceExports
// failed to clean up all stale ResourceExports.
var cleanUpRetry = wait.Backoff{
	Steps:    5,
	Duration: 500 * time.Millisecond,
	Factor:   1.0,
	Jitter:   1,
}

// MemberResourcesCleanupController will remove all ResourceExports belong to a member
// cluster when the corresponding MemberClusterAnnounce CR is deleted. It will also try
// to clean up all stale ResourceExports during start.
type MemberResourcesCleanupController struct {
	client.Client
	Scheme    *runtime.Scheme
	namespace string
}

func NewMemberResourcesCleanupController(Client client.Client,
	Scheme *runtime.Scheme,
	namespace string) *MemberResourcesCleanupController {
	controller := MemberResourcesCleanupController{
		Client:    Client,
		Scheme:    Scheme,
		namespace: namespace,
	}
	return &controller
}

func (c *MemberResourcesCleanupController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	memberAnnounce := &mcv1alpha1.MemberClusterAnnounce{}
	err := c.Get(ctx, req.NamespacedName, memberAnnounce)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if err == nil && memberAnnounce.DeletionTimestamp.IsZero() {
		return ctrl.Result{}, nil
	}

	// Clean up all corresponding ResourceExports when the member cluster's
	// MemberClusterAnnounce is deleted.
	clusterID := getClusterIDFromName(req.Name)
	staleResExports, err := getResourceExportsByClusterIDFunc(c, ctx, clusterID)
	if err != nil {
		klog.ErrorS(err, "Failed to get ResourceExports in the Namespace by ClusterID", "namespace", c.namespace, "clusterID", clusterID)
		return ctrl.Result{}, err
	}
	cleanupSucceed := c.cleanUpResourceExports(ctx, staleResExports)
	if !cleanupSucceed {
		return ctrl.Result{}, fmt.Errorf("failed to clean up all stale ResourceExports for the member cluster %s, retry later", clusterID)
	}
	return ctrl.Result{}, nil
}

func getResourceExportsByClusterID(c *MemberResourcesCleanupController, ctx context.Context, clusterID string) ([]mcv1alpha1.ResourceExport, error) {
	resourceExports := &mcv1alpha1.ResourceExportList{}
	err := c.Client.List(ctx, resourceExports, &client.ListOptions{Namespace: c.namespace}, client.MatchingFields{indexKey: clusterID})
	if err != nil {
		klog.ErrorS(err, "Failed to get ResourceExports by ClusterID", "clusterID", clusterID)
		return nil, err
	}
	return resourceExports.Items, nil
}

func (c *MemberResourcesCleanupController) cleanUpStaleResourceExports(ctx context.Context) error {
	existingMemberClusterIDs, err := c.getExistingMemberClusterIDs(ctx)
	if err != nil {
		klog.ErrorS(err, "Failed to get existing member cluster's ClusterID in the Namespace", "namespace", c.namespace)
		return err
	}

	resourceExports := &mcv1alpha1.ResourceExportList{}
	err = c.Client.List(ctx, resourceExports, &client.ListOptions{Namespace: c.namespace})
	if err != nil {
		klog.ErrorS(err, "Failed to get ResourceExports in the Namespace", "namespace", c.namespace)
		return err
	}

	staleResExports := []mcv1alpha1.ResourceExport{}
	for _, resExport := range resourceExports.Items {
		// The AntreaClusterNetworkPolicy kind of ResourceExport is created in the leader directly
		// without a ClusterID info. It's not owned by any member cluster.
		if resExport.Spec.ClusterID != "" && !existingMemberClusterIDs.Has(resExport.Spec.ClusterID) {
			staleResExports = append(staleResExports, resExport)
		}
	}

	cleanUpSucceed := c.cleanUpResourceExports(ctx, staleResExports)
	if !cleanUpSucceed {
		return fmt.Errorf("stale ResourceExports are not fully cleaned up, retry later")
	}
	return nil
}

func (c *MemberResourcesCleanupController) getExistingMemberClusterIDs(ctx context.Context) (sets.Set[string], error) {
	validMemberClusterIDs := sets.Set[string]{}
	memberClusterAnnounces := &mcv1alpha1.MemberClusterAnnounceList{}
	err := c.Client.List(ctx, memberClusterAnnounces, &client.ListOptions{Namespace: c.namespace})
	if err != nil {
		return nil, err
	}

	for _, m := range memberClusterAnnounces.Items {
		validMemberClusterIDs.Insert(m.ClusterID)
	}
	return validMemberClusterIDs, nil
}

func (c *MemberResourcesCleanupController) cleanUpResourceExports(ctx context.Context, resouceExports []mcv1alpha1.ResourceExport) bool {
	cleanupSucceed := true
	for _, resourceExport := range resouceExports {
		tmpResExp := resourceExport
		if resourceExport.DeletionTimestamp.IsZero() {
			klog.V(2).InfoS("Clean up the stale ResourceExport from the member cluster", "resourceexport", klog.KObj(&tmpResExp), "clusterID", tmpResExp.Spec.ClusterID)
			err := c.Client.Delete(ctx, &tmpResExp, &client.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				klog.V(2).ErrorS(err, "Failed to clean up the stale ResourceExport from the member cluster", "resourceexport", klog.KObj(&tmpResExp), "clusterID", tmpResExp.Spec.ClusterID)
				cleanupSucceed = false
			}
		}
	}
	return cleanupSucceed
}

func (c *MemberResourcesCleanupController) RunOnce(stopCh <-chan struct{}) {
	// Try to clean up all stale ResourceExports before start in case
	// there was a MemberClusterAnnounce deleted but the event was not handled
	// properly if the Antrea Multi-cluster controller is not healthy.
	retry.OnError(cleanUpRetry, func(err error) bool { return true },
		func() error {
			ctx, _ := wait.ContextForChannel(stopCh)
			return c.cleanUpStaleResourceExports(ctx)
		})
}

// SetupWithManager sets up the controller with the Manager.
func (c *MemberResourcesCleanupController) SetupWithManager(mgr ctrl.Manager, stopCh <-chan struct{}) error {
	// Add an Indexer for ResourceExport, so it can be filtered by the ClusterID.
	if err := mgr.GetFieldIndexer().IndexField(context.Background(), &mcv1alpha1.ResourceExport{}, indexKey, func(rawObj client.Object) []string {
		resExport := rawObj.(*mcv1alpha1.ResourceExport)
		return []string{resExport.Spec.ClusterID}
	}); err != nil {
		klog.ErrorS(err, "Failed to create the index")
		return err
	}

	return ctrl.NewControllerManagedBy(mgr).
		For(&mcv1alpha1.MemberClusterAnnounce{}).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(c)
}

func getClusterIDFromName(name string) string {
	return strings.TrimPrefix(name, "member-announce-from-")
}
