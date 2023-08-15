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
	mcv1alpha2 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha2"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/multicluster/controllers/multicluster/commonarea"
)

const (
	indexKey                       = "spec.clusterID"
	memberClusterAnnounceStaleTime = 24 * time.Hour
)

var (
	getResourceExportsByClusterIDFunc = getResourceExportsByClusterID

	// cleanUpRetry is the retry when the cleanUpStaleResourceExports
	// failed to clean up all stale ResourceExports.
	cleanUpRetry = wait.Backoff{
		Steps:    5,
		Duration: 500 * time.Millisecond,
		Factor:   1.0,
		Jitter:   1,
	}
)

// LeaderStaleResCleanupController will run periodically (memberClusterAnnounceStaleTime / 2 ) (12 hours)
// to clean up stale MemberClusterAnnounce resources in the leader cluster if the MemberClusterAnnounce
// timestamp annotation has not been updated for memberClusterAnnounceStaleTime (24 hours).
// It will remove all ResourceExports belong to a member cluster when the corresponding MemberClusterAnnounce
// CR is deleted. It will also try to clean up all stale ResourceExports during start.
type LeaderStaleResCleanupController struct {
	client.Client
	Scheme    *runtime.Scheme
	namespace string
}

func NewLeaderStaleResCleanupController(
	Client client.Client,
	Scheme *runtime.Scheme,
	namespace string,
) *LeaderStaleResCleanupController {
	reconciler := &LeaderStaleResCleanupController{
		Client:    Client,
		Scheme:    Scheme,
		namespace: namespace,
	}
	return reconciler
}

func cleanUpAllMemberClusterAnnounces(ctx context.Context, mgrClient client.Client) error {
	memberClusterAnnounceList := &mcv1alpha1.MemberClusterAnnounceList{}
	if err := mgrClient.List(ctx, memberClusterAnnounceList, &client.ListOptions{}); err != nil {
		klog.ErrorS(err, "Fail to get MemberClusterAnnounce")
		return err
	}
	for _, mca := range memberClusterAnnounceList.Items {
		if err := mgrClient.Delete(ctx, &mca, &client.DeleteOptions{}); err != nil {
			return err
		}
	}
	return nil
}

// cleanUpStaleMemberClusterAnnounces will delete any MemberClusterAnnounce if its
// last update timestamp is over 24 hours.
func (c *LeaderStaleResCleanupController) cleanUpStaleMemberClusterAnnounces(ctx context.Context) {
	memberClusterAnnounceList := &mcv1alpha1.MemberClusterAnnounceList{}
	if err := c.List(ctx, memberClusterAnnounceList, &client.ListOptions{Namespace: c.namespace}); err != nil {
		klog.ErrorS(err, "Fail to get MemberClusterAnnounce in the Namespace", "namespace", c.namespace)
		return
	}

	for _, m := range memberClusterAnnounceList.Items {
		memberClusterAnnounce := m
		lastUpdateTime, err := time.Parse(time.RFC3339, memberClusterAnnounce.Annotations[commonarea.TimestampAnnotationKey])
		if err == nil && time.Since(lastUpdateTime) < memberClusterAnnounceStaleTime {
			continue
		}
		if err == nil {
			klog.InfoS("Cleaning up stale MemberClusterAnnounce. It has not been updated within the agreed period", "MemberClusterAnnounce", klog.KObj(&memberClusterAnnounce), "agreedPeriod", memberClusterAnnounceStaleTime)
		} else {
			klog.InfoS("Cleaning up stale MemberClusterAnnounce. The latest update time is not in RFC3339 format", "MemberClusterAnnounce", klog.KObj(&memberClusterAnnounce))
		}

		if err := c.Client.Delete(ctx, &memberClusterAnnounce, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			klog.ErrorS(err, "Failed to delete stale MemberClusterAnnounce", "MemberClusterAnnounce", klog.KObj(&memberClusterAnnounce))
			return
		}
	}
}

func (c *LeaderStaleResCleanupController) RunPeriodically(stopCh <-chan struct{}) {
	klog.InfoS("Starting LeaderStaleResCleanupController")
	defer klog.InfoS("Shutting down LeaderStaleResCleanupController")

	ctx, _ := wait.ContextForChannel(stopCh)
	go wait.UntilWithContext(ctx, c.cleanUpStaleMemberClusterAnnounces, memberClusterAnnounceStaleTime/2)
	<-stopCh
}

func (c *LeaderStaleResCleanupController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	memberAnnounce := &mcv1alpha1.MemberClusterAnnounce{}
	err := c.Get(ctx, req.NamespacedName, memberAnnounce)
	if err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, err
	}

	if err == nil {
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
	cleanUpSucceed := c.deleteResourceExports(ctx, staleResExports)
	if !cleanUpSucceed {
		return ctrl.Result{}, fmt.Errorf("failed to clean up all stale ResourceExports for the member cluster %s, retry later", clusterID)
	}
	return ctrl.Result{}, nil
}

func (c *LeaderStaleResCleanupController) cleanUpStaleResourceExports(ctx context.Context) error {
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

	cleanUpSucceed := c.deleteResourceExports(ctx, staleResExports)
	if !cleanUpSucceed {
		return fmt.Errorf("stale ResourceExports are not fully cleaned up, retry later")
	}
	return nil
}

func (c *LeaderStaleResCleanupController) getExistingMemberClusterIDs(ctx context.Context) (sets.Set[string], error) {
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

func (c *LeaderStaleResCleanupController) deleteResourceExports(ctx context.Context, resouceExports []mcv1alpha1.ResourceExport) bool {
	cleanupSucceed := true
	for _, resourceExport := range resouceExports {
		tmpResExp := resourceExport
		if resourceExport.DeletionTimestamp.IsZero() {
			klog.V(2).InfoS("Clean up the stale ResourceExport from the member cluster", "resourceexport", klog.KObj(&tmpResExp), "clusterID", tmpResExp.Spec.ClusterID)
			err := c.Client.Delete(ctx, &tmpResExp, &client.DeleteOptions{})
			if err != nil && !apierrors.IsNotFound(err) {
				klog.ErrorS(err, "Failed to clean up the stale ResourceExport from the member cluster", "resourceexport", klog.KObj(&tmpResExp), "clusterID", tmpResExp.Spec.ClusterID)
				cleanupSucceed = false
			}
		}
	}
	return cleanupSucceed
}

func (c *LeaderStaleResCleanupController) RunOnce(stopCh <-chan struct{}) {
	// Try to clean up all stale ResourceExports before start in case
	// there was a MemberClusterAnnounce deleted but the event was not handled
	// properly if the Antrea Multi-cluster Controller is not healthy.
	// Or clean up all resources when there is no ClusterSet CR at all.
	retry.OnError(cleanUpRetry, func(err error) bool { return true },
		func() error {
			ctx, _ := wait.ContextForChannel(stopCh)
			clusterSets := &mcv1alpha2.ClusterSetList{}
			if err := c.Client.List(ctx, clusterSets, &client.ListOptions{Namespace: c.namespace}); err != nil {
				return err
			}
			if len(clusterSets.Items) == 0 {
				if err := cleanUpAllMemberClusterAnnounces(ctx, c.Client); err != nil {
					return err
				}
			}
			return c.cleanUpStaleResourceExports(ctx)
		})
}

// SetupWithManager sets up the controller with the Manager.
func (c *LeaderStaleResCleanupController) SetupWithManager(mgr ctrl.Manager, stopCh <-chan struct{}) error {
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
		WithEventFilter(common.DeleteEventPredicate).
		Complete(c)
}

func getClusterIDFromName(name string) string {
	return strings.TrimPrefix(name, "member-announce-from-")
}

func getResourceExportsByClusterID(c *LeaderStaleResCleanupController, ctx context.Context, clusterID string) ([]mcv1alpha1.ResourceExport, error) {
	resourceExports := &mcv1alpha1.ResourceExportList{}
	err := c.Client.List(ctx, resourceExports, &client.ListOptions{Namespace: c.namespace}, client.MatchingFields{indexKey: clusterID})
	if err != nil {
		klog.ErrorS(err, "Failed to get ResourceExports by ClusterID", "clusterID", clusterID)
		return nil, err
	}
	return resourceExports.Items, nil
}
