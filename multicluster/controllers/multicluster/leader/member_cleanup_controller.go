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
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/multicluster/controllers/multicluster/commonarea"
)

// MemberResourcesCleanupController will remove all ResourceExports belongs to a member
// cluster when the corresponding MemberClusterAnnounce CR is deleted.
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
	if err != nil {
		// Clean up all corresponding ResourceExports when the member cluster's MemberClusterAnnounce is deleted.
		if apierrors.IsNotFound(err) {
			allResExports, err := c.getAllResourceExports(ctx)
			if err != nil {
				klog.V(2).ErrorS(err, "Failed to get the ResourceExports in the Namespace", "namespace", c.namespace)
				return ctrl.Result{}, err
			}
			clusterID := getClusterIDFromName(req.Name)
			cleanupSucceed := c.cleanupResources(ctx, getClusterIDFromName(req.Name), allResExports)
			if !cleanupSucceed {
				return ctrl.Result{}, fmt.Errorf("failed to clean up all stale ResourceExports for the member cluster %s, retry later", clusterID)
			}
			return ctrl.Result{}, nil
		}
		return ctrl.Result{}, err
	}
	return ctrl.Result{}, nil
}

func (c *MemberResourcesCleanupController) getAllResourceExports(ctx context.Context) ([]mcv1alpha1.ResourceExport, error) {
	resourceExports := &mcv1alpha1.ResourceExportList{}
	err := c.Client.List(ctx, resourceExports, &client.ListOptions{Namespace: c.namespace})
	if err != nil {
		return nil, err
	}
	return resourceExports.Items, nil
}

func (c *MemberResourcesCleanupController) checkAndCleanup(ctx context.Context) {
	validMemberClusterIDs, err := c.getValidMemberClusterIDs(ctx)
	if err != nil {
		klog.V(2).ErrorS(err, "Failed to get valid member cluster's ClusterID in the Namespace", "namespace", c.namespace)
		return
	}

	allResExports, err := c.getAllResourceExports(ctx)
	if err != nil {
		klog.V(2).ErrorS(err, "Failed to get the ResourceExports in the Namespace", "namespace", c.namespace)
		return
	}

	allMemberClusterIDs, err := c.getAllMemberClusterIDs(ctx, allResExports)
	if err != nil {
		klog.V(2).ErrorS(err, "Failed to get all member cluster's ClusterID in the Namespace", "namespace", c.namespace)
		return
	}

	staleMemberClusterIDs := allMemberClusterIDs.Difference(validMemberClusterIDs)
	for clusterID := range staleMemberClusterIDs {
		cleanupSucceed := c.cleanupResources(ctx, clusterID, allResExports)
		if cleanupSucceed {
			klog.InfoS("The member cluster's stale resources are cleaned up", "clusterID", clusterID)
		} else {
			klog.Error("Failed to clean up the member cluster's stale resources, will retry next time", "clusterID", clusterID)
		}
	}
}

func (c *MemberResourcesCleanupController) getAllMemberClusterIDs(ctx context.Context, resourceExports []mcv1alpha1.ResourceExport) (sets.Set[string], error) {
	allMemberClusterIDs := sets.Set[string]{}
	for _, resourceExport := range resourceExports {
		if resourceExport.Spec.ClusterID != "" {
			allMemberClusterIDs.Insert(resourceExport.Spec.ClusterID)
		}
	}
	return allMemberClusterIDs, nil
}

func (c *MemberResourcesCleanupController) getValidMemberClusterIDs(ctx context.Context) (sets.Set[string], error) {
	validMemberClusterIDs := sets.Set[string]{}
	memberClusterAnnounces := &mcv1alpha1.MemberClusterAnnounceList{}
	err := c.Client.List(ctx, memberClusterAnnounces, &client.ListOptions{Namespace: c.namespace})
	if err != nil {
		klog.V(2).ErrorS(err, "Failed to get the MemberClusterAnnounces in the Namespace", "namespace", c.namespace)
		return nil, err
	}

	for _, m := range memberClusterAnnounces.Items {
		lastUpdateTime, err := time.Parse(time.RFC3339, m.Annotations[commonarea.TimestampAnnotationKey])
		if err == nil && time.Since(lastUpdateTime) < common.MemberClusterAnnounceStaleTime {
			validMemberClusterIDs.Insert(m.ClusterID)
		}
	}
	return validMemberClusterIDs, nil
}

func (c *MemberResourcesCleanupController) cleanupResources(ctx context.Context, clusterID string, resouceExports []mcv1alpha1.ResourceExport) bool {
	cleanupSucceed := true
	for _, resourceExport := range resouceExports {
		if resourceExport.Spec.ClusterID == clusterID && resourceExport.DeletionTimestamp.IsZero() {
			klog.InfoS("Clean up stale ResourceExport from the member cluster", "ResourceExport", klog.KObj(&resourceExport), "clusterID", clusterID)
			err := c.Client.Delete(ctx, &resourceExport, &client.DeleteOptions{})
			if err == nil || (err != nil && apierrors.IsNotFound(err)) {
				continue
			} else {
				return false
			}
		}
	}
	return cleanupSucceed
}

// SetupWithManager sets up the controller with the Manager.
func (c *MemberResourcesCleanupController) SetupWithManager(mgr ctrl.Manager, stopCh <-chan struct{}) error {
	// Try to clean up all stale resources before start.
	ctx, _ := wait.ContextForChannel(stopCh)
	c.checkAndCleanup(ctx)

	return ctrl.NewControllerManagedBy(mgr).
		For(&mcv1alpha1.MemberClusterAnnounce{}).
		Complete(c)
}

func getClusterIDFromName(name string) string {
	return strings.TrimPrefix(name, "member-announce-from-")
}
