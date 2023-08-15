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

package member

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/util/workqueue"
	"k8s.io/klog/v2"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	k8smcv1alpha1 "sigs.k8s.io/mcs-api/pkg/apis/v1alpha1"

	"antrea.io/antrea/multicluster/apis/multicluster/constants"
	mcv1alpha1 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha1"
	mcv1alpha2 "antrea.io/antrea/multicluster/apis/multicluster/v1alpha2"
	"antrea.io/antrea/multicluster/controllers/multicluster/common"
	"antrea.io/antrea/multicluster/controllers/multicluster/commonarea"
	crdv1beta1 "antrea.io/antrea/pkg/apis/crd/v1beta1"
)

// MemberStaleResCleanupController will clean up ServiceImport, MC Service, ACNP, ClusterInfoImport and LabelIdentity
// resources if no corresponding ResourceImports in the leader cluster and remove stale ResourceExports
// in the leader cluster if no corresponding ServiceExport or Gateway in the member cluster when it runs in
// the member cluster. MemberStaleResCleanupController one-time runner will run only once in the member cluster
// during Multi-cluster Controller starts, and it will retry only if there is an error.
// MemberStaleResCleanupController's reconciler will handle ClusterSet deletion event to clean up any stale resources.
type MemberStaleResCleanupController struct {
	client.Client
	Scheme           *runtime.Scheme
	localClusterID   string
	commonAreaGetter commonarea.RemoteCommonAreaGetter
	namespace        string
	// queue only ever has one item, but it has nice error handling backoff/retry semantics
	queue workqueue.RateLimitingInterface
}

func NewMemberStaleResCleanupController(
	Client client.Client,
	Scheme *runtime.Scheme,
	namespace string,
	commonAreaGetter commonarea.RemoteCommonAreaGetter,
) *MemberStaleResCleanupController {
	reconciler := &MemberStaleResCleanupController{
		Client:           Client,
		Scheme:           Scheme,
		namespace:        namespace,
		commonAreaGetter: commonAreaGetter,
		queue:            workqueue.NewNamedRateLimitingQueue(workqueue.DefaultControllerRateLimiter(), "MemberStaleResCleanupController"),
	}
	return reconciler
}

// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=multicluster.x-k8s.io,resources=serviceimports,verbs=get;list;watch;delete
// +kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceimports,verbs=get;list;watch;
// +kubebuilder:rbac:groups=multicluster.crd.antrea.io,resources=resourceexports,verbs=get;list;watch;delete

func (c *MemberStaleResCleanupController) CleanUp(ctx context.Context) error {
	var err error
	clusterSets := &mcv1alpha2.ClusterSetList{}
	if err = c.Client.List(ctx, clusterSets, &client.ListOptions{}); err != nil {
		return err
	}

	if len(clusterSets.Items) == 0 {
		klog.InfoS("There is no existing ClusterSet, try to clean up all auto-generated resources by Antrea Multi-cluster")
		if err = common.CleanUpResourcesCreatedByMC(ctx, c.Client); err != nil {
			return err
		}
		return nil
	}

	var commonArea commonarea.RemoteCommonArea
	commonArea, c.localClusterID, err = c.commonAreaGetter.GetRemoteCommonAreaAndLocalID()
	if err != nil {
		return err
	}

	if err = c.cleanUpStaleResourcesOnMember(ctx, commonArea); err != nil {
		return err
	}
	// Clean up stale ResourceExports in the leader cluster for a member cluster.
	if err := c.cleanUpStaleResourceExportsOnLeader(ctx, commonArea); err != nil {
		return err
	}
	return nil
}

func (c *MemberStaleResCleanupController) cleanUpStaleResourcesOnMember(ctx context.Context, commonArea commonarea.RemoteCommonArea) error {
	svcImpList := &k8smcv1alpha1.ServiceImportList{}
	if err := c.List(ctx, svcImpList, &client.ListOptions{}); err != nil {
		return err
	}
	svcList := &corev1.ServiceList{}
	if err := c.List(ctx, svcList, &client.ListOptions{}); err != nil {
		return err
	}
	acnpList := &crdv1beta1.ClusterNetworkPolicyList{}
	if err := c.List(ctx, acnpList, &client.ListOptions{}); err != nil {
		return err
	}
	ciImpList := &mcv1alpha1.ClusterInfoImportList{}
	if err := c.List(ctx, ciImpList, &client.ListOptions{}); err != nil {
		return err
	}
	labelIdentityList := &mcv1alpha1.LabelIdentityList{}
	if err := c.List(ctx, labelIdentityList, &client.ListOptions{}); err != nil {
		return err
	}
	// All previously imported resources need to be listed before ResourceImports are listed.
	// This prevents race condition between stale_controller and other reconcilers.
	// See https://github.com/antrea-io/antrea/issues/4854
	resImpList := &mcv1alpha1.ResourceImportList{}
	if err := commonArea.List(ctx, resImpList, &client.ListOptions{Namespace: commonArea.GetNamespace()}); err != nil {
		return err
	}
	// Clean up any imported Services that do not have corresponding ResourceImport anymore
	if err := c.cleanUpStaleServiceResources(ctx, svcImpList, svcList, resImpList); err != nil {
		klog.ErrorS(err, "Failed to cleanup stale imported Services")
		return err
	}
	// Clean up any imported ACNPs that do not have corresponding ResourceImport anymore
	if err := c.cleanUpACNPResources(ctx, acnpList, resImpList); err != nil {
		klog.ErrorS(err, "Failed to cleanup stale imported ACNPs")
		return err
	}
	// Clean up any imported ClusterInfos that do not have corresponding ResourceImport anymore
	if err := c.cleanUpClusterInfoImports(ctx, ciImpList, resImpList); err != nil {
		klog.ErrorS(err, "Failed to cleanup stale ClusterInfoImports")
		return err
	}
	// Clean up any imported LabelIdentities that do not have corresponding ResourceImport anymore
	if err := c.cleanUpLabelIdentities(ctx, labelIdentityList, resImpList); err != nil {
		klog.ErrorS(err, "Failed to cleanup stale imported LabelIdentities")
		return err
	}
	return nil
}

// Clean up stale ResourceExports in the leader cluster for a member cluster.
func (c *MemberStaleResCleanupController) cleanUpStaleResourceExportsOnLeader(ctx context.Context, commonArea commonarea.RemoteCommonArea) error {
	if err := c.cleanUpClusterInfoResourceExports(ctx, commonArea); err != nil {
		return err
	}
	resExpList := &mcv1alpha1.ResourceExportList{}
	if err := commonArea.List(ctx, resExpList, &client.ListOptions{Namespace: commonArea.GetNamespace()}); err != nil {
		return err
	}
	if len(resExpList.Items) == 0 {
		return nil
	}
	if err := c.cleanUpServiceResourceExports(ctx, commonArea, resExpList); err != nil {
		return err
	}
	if err := c.cleanUpLabelIdentityResourceExports(ctx, commonArea, resExpList); err != nil {
		return err
	}
	return nil
}

func (c *MemberStaleResCleanupController) cleanUpStaleServiceResources(ctx context.Context, svcImpList *k8smcv1alpha1.ServiceImportList,
	svcList *corev1.ServiceList, resImpList *mcv1alpha1.ResourceImportList) error {
	svcImpItems := map[string]k8smcv1alpha1.ServiceImport{}
	for _, svcImp := range svcImpList.Items {
		svcImpItems[svcImp.Namespace+"/"+svcImp.Name] = svcImp
	}

	mcsSvcItems := map[string]corev1.Service{}
	for _, svc := range svcList.Items {
		if _, ok := svc.Annotations[common.AntreaMCServiceAnnotation]; ok {
			mcsSvcItems[svc.Namespace+"/"+svc.Name] = svc
		}
	}
	for _, resImp := range resImpList.Items {
		if resImp.Spec.Kind == constants.ServiceImportKind {
			delete(mcsSvcItems, resImp.Spec.Namespace+"/"+common.AntreaMCSPrefix+resImp.Spec.Name)
			delete(svcImpItems, resImp.Spec.Namespace+"/"+resImp.Spec.Name)
		}
	}

	for _, staleSvc := range mcsSvcItems {
		svc := staleSvc
		klog.InfoS("Cleaning up stale imported Service", "service", klog.KObj(&svc))
		if err := c.Client.Delete(ctx, &svc, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	for _, staleSvcImp := range svcImpItems {
		svcImp := staleSvcImp
		klog.InfoS("Cleaning up stale ServiceImport", "serviceimport", klog.KObj(&svcImp))
		if err := c.Client.Delete(ctx, &svcImp, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *MemberStaleResCleanupController) cleanUpACNPResources(ctx context.Context, acnpList *crdv1beta1.ClusterNetworkPolicyList,
	resImpList *mcv1alpha1.ResourceImportList) error {
	staleMCACNPItems := map[string]crdv1beta1.ClusterNetworkPolicy{}
	for _, acnp := range acnpList.Items {
		if _, ok := acnp.Annotations[common.AntreaMCACNPAnnotation]; ok {
			staleMCACNPItems[acnp.Name] = acnp
		}
	}
	for _, resImp := range resImpList.Items {
		if resImp.Spec.Kind == constants.AntreaClusterNetworkPolicyKind {
			acnpNameFromResImp := common.AntreaMCSPrefix + resImp.Spec.Name
			delete(staleMCACNPItems, acnpNameFromResImp)
		}
	}
	for _, stalePolicy := range staleMCACNPItems {
		acnp := stalePolicy
		klog.InfoS("Cleaning up stale imported ACNP", "acnp", klog.KObj(&acnp))
		if err := c.Client.Delete(ctx, &acnp, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *MemberStaleResCleanupController) cleanUpClusterInfoImports(ctx context.Context, ciImpList *mcv1alpha1.ClusterInfoImportList,
	resImpList *mcv1alpha1.ResourceImportList) error {
	staleCIImps := map[string]mcv1alpha1.ClusterInfoImport{}
	for _, item := range ciImpList.Items {
		staleCIImps[item.Name] = item
	}
	for _, resImp := range resImpList.Items {
		if resImp.Spec.Kind == constants.ClusterInfoKind {
			delete(staleCIImps, resImp.Name)
		}
	}
	for _, staleCIImp := range staleCIImps {
		ciImp := staleCIImp
		klog.InfoS("Cleaning up stale ClusterInfoImport", "clusterinfoimport", klog.KObj(&ciImp))
		if err := c.Client.Delete(ctx, &ciImp, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *MemberStaleResCleanupController) cleanUpLabelIdentities(ctx context.Context, labelIdentityList *mcv1alpha1.LabelIdentityList,
	resImpList *mcv1alpha1.ResourceImportList) error {
	staleLabelIdentities := map[string]mcv1alpha1.LabelIdentity{}
	for _, labelIdentityObj := range labelIdentityList.Items {
		staleLabelIdentities[labelIdentityObj.Name] = labelIdentityObj
	}
	for _, labelImp := range resImpList.Items {
		delete(staleLabelIdentities, labelImp.Name)
	}
	for _, l := range staleLabelIdentities {
		labelIdentity := l
		klog.V(2).InfoS("Cleaning up stale imported LabelIdentity", "labelidentity", klog.KObj(&labelIdentity))
		if err := c.Client.Delete(ctx, &labelIdentity, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// cleanUpServiceResourceExports removes any Service/Endpoint kind of ResourceExports when there is no
// corresponding ServiceExport in the local cluster.
func (c *MemberStaleResCleanupController) cleanUpServiceResourceExports(ctx context.Context, commonArea commonarea.RemoteCommonArea, resExpList *mcv1alpha1.ResourceExportList) error {
	svcExpList := &k8smcv1alpha1.ServiceExportList{}
	if err := c.List(ctx, svcExpList, &client.ListOptions{}); err != nil {
		return err
	}
	allResExpItems := resExpList.Items
	svcExpItems := svcExpList.Items
	staleResExpItems := map[string]mcv1alpha1.ResourceExport{}

	for _, resExp := range allResExpItems {
		if resExp.Spec.Kind == constants.ServiceKind && resExp.Labels[constants.SourceClusterID] == c.localClusterID {
			staleResExpItems[resExp.Spec.Namespace+"/"+resExp.Spec.Name+"service"] = resExp
		}
		if resExp.Spec.Kind == constants.EndpointsKind && resExp.Labels[constants.SourceClusterID] == c.localClusterID {
			staleResExpItems[resExp.Spec.Namespace+"/"+resExp.Spec.Name+"endpoint"] = resExp
		}
	}

	for _, se := range svcExpItems {
		delete(staleResExpItems, se.Namespace+"/"+se.Name+"service")
		delete(staleResExpItems, se.Namespace+"/"+se.Name+"endpoint")
	}

	for _, r := range staleResExpItems {
		re := r
		klog.InfoS("Cleaning up stale ResourceExport", "ResourceExport", klog.KObj(&re))
		if err := commonArea.Delete(ctx, &re, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

func (c *MemberStaleResCleanupController) cleanUpLabelIdentityResourceExports(ctx context.Context, commonArea commonarea.RemoteCommonArea, resExpList *mcv1alpha1.ResourceExportList) error {
	podList, nsList := &corev1.PodList{}, &corev1.NamespaceList{}
	if err := c.List(ctx, podList, &client.ListOptions{}); err != nil {
		return err
	}
	if err := c.List(ctx, nsList, &client.ListOptions{}); err != nil {
		return err
	}
	allResExpItems := resExpList.Items
	staleResExpItems := map[string]mcv1alpha1.ResourceExport{}
	for _, resExp := range allResExpItems {
		if resExp.Spec.Kind == constants.LabelIdentityKind && resExp.Labels[constants.SourceClusterID] == c.localClusterID {
			staleResExpItems[resExp.Spec.LabelIdentity.NormalizedLabel] = resExp
		}
	}
	nsLabelMap := map[string]string{}
	for _, ns := range nsList.Items {
		if _, ok := ns.Labels[corev1.LabelMetadataName]; !ok {
			// NamespaceDefaultLabelName is supported from K8s v1.21. For K8s versions before v1.21,
			// we append the Namespace name label to the Namespace label set.
			ns.Labels[corev1.LabelMetadataName] = ns.Name
		}
		nsLabelMap[ns.Name] = "ns:" + labels.FormatLabels(ns.Labels)
	}
	for _, p := range podList.Items {
		podNSlabel, ok := nsLabelMap[p.Namespace]
		if !ok {
			continue
		}
		normalizedLabel := podNSlabel + "&pod:" + labels.Set(p.Labels).String()
		delete(staleResExpItems, normalizedLabel)
	}
	for _, r := range staleResExpItems {
		re := r
		klog.InfoS("Cleaning up stale ResourceExport", "ResourceExport", klog.KObj(&re))
		if err := commonArea.Delete(ctx, &re, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// cleanUpClusterInfoResourceExports removes any ClusterInfo kind of ResourceExports when there is no
// Gateway in the local cluster.
func (c *MemberStaleResCleanupController) cleanUpClusterInfoResourceExports(ctx context.Context, commonArea commonarea.RemoteCommonArea) error {
	var gws mcv1alpha1.GatewayList
	if err := c.Client.List(ctx, &gws, &client.ListOptions{}); err != nil {
		return err
	}

	if len(gws.Items) == 0 {
		ciExport := &mcv1alpha1.ResourceExport{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: commonArea.GetNamespace(),
				Name:      common.NewClusterInfoResourceExportName(c.localClusterID),
			},
		}
		klog.InfoS("Cleaning up stale ClusterInfo kind of ResourceExport", "resourceexport", klog.KObj(ciExport))
		if err := commonArea.Delete(ctx, ciExport, &client.DeleteOptions{}); err != nil && !apierrors.IsNotFound(err) {
			return err
		}
	}
	return nil
}

// Enqueue will be called after MemberStaleResCleanupController is initialized.
func (c *MemberStaleResCleanupController) Enqueue() {
	// The key can be anything as we only have single item.
	c.queue.Add("key")
}

// Run starts the MemberStaleResCleanupController and blocks until stopCh is closed.
// it will run only once to clean up stale resources if no error happens.
func (c *MemberStaleResCleanupController) Run(stopCh <-chan struct{}) {
	defer c.queue.ShutDown()

	klog.InfoS("Starting MemberStaleResCleanupController")
	defer klog.InfoS("Shutting down MemberStaleResCleanupController")

	ctx, cancel := wait.ContextForChannel(stopCh)

	if err := c.CleanUp(ctx); err != nil {
		c.Enqueue()
		go wait.UntilWithContext(ctx, func(ctx context.Context) {
			c.runWorker(ctx, cancel)
		}, 5*time.Second)
	}
	<-stopCh
}

func (c *MemberStaleResCleanupController) runWorker(ctx context.Context, cancel context.CancelFunc) {
	for c.processNextWorkItem(ctx, cancel) {
	}
}

func (c *MemberStaleResCleanupController) processNextWorkItem(ctx context.Context, cancel context.CancelFunc) bool {
	key, quit := c.queue.Get()
	if quit {
		return false
	}
	defer c.queue.Done(key)
	err := c.CleanUp(ctx)
	if err == nil {
		c.queue.Forget(key)
		cancel()
		return false
	}

	klog.ErrorS(err, "Error removing stale resources, re-queuing it")
	c.queue.AddRateLimited(key)
	return true
}

// Reconcile ClusterSet changes
func (c *MemberStaleResCleanupController) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	var err error
	var commonArea commonarea.RemoteCommonArea
	commonArea, c.localClusterID, err = c.commonAreaGetter.GetRemoteCommonAreaAndLocalID()
	if err != nil {
		return ctrl.Result{}, err
	}
	// When the commonArea is not nil, the ClusterSet is ready since we ignore creation event
	// and filter other status from update event.
	if commonArea != nil {
		klog.InfoS("Clean up all stale imported and exported resources created by Antrea Multi-cluster Controller",
			"clusterset", req.NamespacedName)
		if err = c.cleanUpStaleResourcesOnMember(ctx, commonArea); err != nil {
			return ctrl.Result{}, err
		}
		if err = c.cleanUpStaleResourceExportsOnLeader(ctx, commonArea); err != nil {
			return ctrl.Result{}, err
		}
	}
	return ctrl.Result{}, nil
}

var (
	statusReadyPredicate = predicate.Funcs{
		CreateFunc: func(e event.CreateEvent) bool {
			return false
		},
		UpdateFunc: common.StatusReadyPredicate,
		DeleteFunc: func(e event.DeleteEvent) bool {
			return false
		},
	}
)

// SetupWithManager sets up the controller with the Manager.
func (r *MemberStaleResCleanupController) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&mcv1alpha2.ClusterSet{}).
		WithEventFilter(statusReadyPredicate).
		WithOptions(controller.Options{
			MaxConcurrentReconciles: 1,
		}).
		Complete(r)
}
