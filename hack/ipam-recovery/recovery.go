// Copyright 2026 Antrea Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"sort"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/uuid"
	"k8s.io/client-go/kubernetes"
	"k8s.io/klog/v2"
)

const (
	enabledLabel        = "ipam-recovery.antrea.io/enabled"
	hostLocalLabel      = "ipam-recovery.antrea.io/host-local"
	incidentAnnotation  = "ipam-recovery.antrea.io/incident"
	cancelledAnnotation = "ipam-recovery.antrea.io/cancelled-incident"
	stateKey            = "state.json"
)

// waitingError represents normal asynchronous progress, distinct from safety blockers.
type waitingError struct {
	reason string
}

func (e *waitingError) Error() string { return e.reason }

func waitingFor(format string, args ...interface{}) error {
	return &waitingError{reason: fmt.Sprintf(format, args...)}
}

func isWaiting(err error) bool {
	var waiting *waitingError
	return errors.As(err, &waiting)
}

type victim struct {
	Namespace     string    `json:"namespace"`
	Name          string    `json:"name"`
	UID           types.UID `json:"uid"`
	ReplicaSet    string    `json:"replicaSet"`
	ReplicaSetUID types.UID `json:"replicaSetUID"`
	Deployment    string    `json:"deployment"`
	DeploymentUID types.UID `json:"deploymentUID"`
}

type plan struct {
	ID           string      `json:"id"`
	Node         string      `json:"node"`
	NodeUID      types.UID   `json:"nodeUID"`
	IPs          []string    `json:"ips"`
	Victims      []victim    `json:"victims"`
	BaselineUIDs []types.UID `json:"baselineUIDs"`
	Started      time.Time   `json:"started"`
	Quarantined  bool        `json:"quarantined,omitempty"`
}

type completion struct {
	ID      string    `json:"id"`
	Node    string    `json:"node"`
	At      time.Time `json:"at"`
	Outcome string    `json:"outcome"`
}

type journal struct {
	Version int          `json:"version"`
	Pending *plan        `json:"pending,omitempty"`
	History []completion `json:"history,omitempty"`
}

type candidate struct {
	Node string
	IPs  []string
	Pods []corev1.Pod
}

// fingerprint includes every UID and IP, so membership changes restart confirmation.
func (g candidate) fingerprint() string {
	parts := append([]string{g.Node}, g.IPs...)
	for _, p := range g.Pods {
		parts = append(parts, string(p.UID))
	}
	return strings.Join(parts, "/")
}

// duplicates reads all namespaces, including workloads which did not opt in. A label-filtered
// list would hide owners and could turn a conflict with a protected Pod into an unsafe repair.
func duplicates(pods []corev1.Pod) ([]candidate, error) {
	owners := make(map[string]map[types.UID]corev1.Pod)
	for _, p := range pods {
		if p.Spec.HostNetwork || p.Spec.NodeName == "" || p.Status.Phase == corev1.PodSucceeded ||
			p.Status.Phase == corev1.PodFailed {
			continue
		}
		ips := p.Status.PodIPs
		if len(ips) == 0 && p.Status.PodIP != "" {
			ips = []corev1.PodIP{{IP: p.Status.PodIP}}
		}
		for _, raw := range ips {
			ip, err := netip.ParseAddr(raw.IP)
			if err != nil || ip.Zone() != "" || !ip.IsGlobalUnicast() || p.UID == "" {
				return nil, fmt.Errorf("invalid Pod identity or IP for %s/%s", p.Namespace, p.Name)
			}
			key := ip.Unmap().String()
			if owners[key] == nil {
				owners[key] = make(map[types.UID]corev1.Pod)
			}
			owners[key][p.UID] = p
		}
	}
	byNode := make(map[string]*candidate)
	for ip, group := range owners {
		if len(group) < 2 {
			continue
		}
		node := ""
		for _, p := range group {
			if node != "" && node != p.Spec.NodeName {
				return nil, fmt.Errorf("address %s is duplicated across Nodes; requires manual diagnosis", ip)
			}
			node = p.Spec.NodeName
		}
		if byNode[node] == nil {
			byNode[node] = &candidate{Node: node}
		}
		g := byNode[node]
		g.IPs = append(g.IPs, ip)
		for _, p := range group {
			found := false
			for _, existing := range g.Pods {
				found = found || existing.UID == p.UID
			}
			if !found {
				g.Pods = append(g.Pods, p)
			}
		}
	}
	var groups []candidate
	for _, g := range byNode {
		sort.Strings(g.IPs)
		sort.Slice(g.Pods, func(i, j int) bool { return g.Pods[i].UID < g.Pods[j].UID })
		groups = append(groups, *g)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Node < groups[j].Node })
	return groups, nil
}

type observation struct {
	Since time.Time
	Count int
}

type settings struct {
	Namespace string
	StateName string
	DryRun    bool
	Confirm   time.Duration
	MinAge    time.Duration
	Timeout   time.Duration
	Cooldown  time.Duration
	MaxDaily  int
	MaxPods   int
}

type recovery struct {
	client   kubernetes.Interface
	settings settings
	seen     map[string]observation
	now      func() time.Time
}

func (r *recovery) listPods(ctx context.Context) ([]corev1.Pod, error) {
	// Continue tokens preserve the collection snapshot. Never use resourceVersion=0 / informer
	// caches for repair decisions. An expired token fails the entire scan closed.
	var pods []corev1.Pod
	options := metav1.ListOptions{Limit: 500}
	for {
		list, err := r.client.CoreV1().Pods("").List(ctx, options)
		if err != nil {
			return nil, err
		}
		pods = append(pods, list.Items...)
		if list.Continue == "" {
			return pods, nil
		}
		options.Continue = list.Continue
	}
}

func (r *recovery) readJournal(ctx context.Context) (*corev1.ConfigMap, journal, error) {
	cm, err := r.client.CoreV1().ConfigMaps(r.settings.Namespace).Get(ctx, r.settings.StateName, metav1.GetOptions{})
	if err != nil {
		return nil, journal{}, err
	}
	var j journal
	decoder := json.NewDecoder(strings.NewReader(cm.Data[stateKey]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&j); err != nil {
		return nil, j, fmt.Errorf("invalid recovery journal: %w", err)
	}
	if err := decoder.Decode(new(interface{})); err != io.EOF {
		return nil, j, fmt.Errorf("recovery journal must contain exactly one JSON document")
	}
	if j.Version != 1 {
		return nil, j, fmt.Errorf("unsupported journal version %d", j.Version)
	}
	if p := j.Pending; p != nil {
		if p.ID == "" || p.Node == "" || p.NodeUID == "" || p.Started.IsZero() ||
			len(p.IPs) == 0 || len(p.Victims) < 2 || len(p.Victims) > r.settings.MaxPods {
			return nil, j, fmt.Errorf("invalid pending recovery plan")
		}
		seen := make(map[types.UID]bool)
		if len(p.BaselineUIDs) < len(p.Victims) || len(p.BaselineUIDs) > 10000 {
			return nil, j, fmt.Errorf("invalid replacement baseline")
		}
		baseline := make(map[types.UID]bool)
		for i, uid := range p.BaselineUIDs {
			if uid == "" || (i > 0 && p.BaselineUIDs[i-1] >= uid) {
				return nil, j, fmt.Errorf("invalid replacement baseline UID")
			}
			baseline[uid] = true
		}
		for i, v := range p.Victims {
			if v.Namespace == "" || v.Name == "" || v.UID == "" || v.ReplicaSet == "" ||
				v.ReplicaSetUID == "" || v.Deployment == "" || v.DeploymentUID == "" || seen[v.UID] ||
				(i > 0 && p.Victims[i-1].UID >= v.UID) || !baseline[v.UID] {
				return nil, j, fmt.Errorf("invalid recovery victim")
			}
			seen[v.UID] = true
		}
		for i, raw := range p.IPs {
			ip, err := netip.ParseAddr(raw)
			if err != nil || ip.Zone() != "" || !ip.IsGlobalUnicast() || ip.Unmap().String() != raw ||
				(i > 0 && p.IPs[i-1] >= raw) {
				return nil, j, fmt.Errorf("invalid recovery IP set")
			}
		}
	}
	for _, h := range j.History {
		if h.ID == "" || h.Node == "" || h.At.IsZero() || (h.Outcome != "cancelled-before-cordon" &&
			h.Outcome != "replicas-available-node-quarantined" && h.Outcome != "manually-resolved") {
			return nil, j, fmt.Errorf("invalid recovery history entry")
		}
	}
	return cm, j, nil
}

func (r *recovery) saveJournal(ctx context.Context, cm *corev1.ConfigMap, j journal) error {
	data, err := json.Marshal(j)
	if err != nil {
		return err
	}
	cm = cm.DeepCopy()
	if cm.Data == nil {
		cm.Data = make(map[string]string)
	}
	cm.Data[stateKey] = string(data)
	// ConfigMap resourceVersion serializes creation / completion of immutable plans. There is
	// no expiring leader lock which a paused worker could outlive. Duplicate workers can only
	// perform the same idempotent UID-preconditioned action on the same durable plan.
	_, err = r.client.CoreV1().ConfigMaps(cm.Namespace).Update(ctx, cm, metav1.UpdateOptions{})
	return err
}

func readyNode(n *corev1.Node) error {
	if n.DeletionTimestamp != nil || n.Labels[hostLocalLabel] != "true" ||
		n.Labels[corev1.LabelOSStable] != "linux" {
		return fmt.Errorf("node %s is not an opted-in Linux host-local Node", n.Name)
	}
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
			return nil
		}
	}
	return fmt.Errorf("node %s is not Ready", n.Name)
}

func (r *recovery) workload(ctx context.Context, p *corev1.Pod, now time.Time) (victim, error) {
	v := victim{Namespace: p.Namespace, Name: p.Name, UID: p.UID}
	if p.Labels[enabledLabel] != "true" || p.Spec.HostNetwork || p.DeletionTimestamp != nil ||
		p.Annotations[corev1.MirrorPodAnnotationKey] != "" || p.Spec.PriorityClassName == "system-node-critical" ||
		p.Spec.PriorityClassName == "system-cluster-critical" || p.Status.Phase != corev1.PodRunning ||
		p.CreationTimestamp.IsZero() || now.Sub(p.CreationTimestamp.Time) < r.settings.MinAge {
		return v, fmt.Errorf("pod %s/%s is protected, terminating, too young, or not Running", p.Namespace, p.Name)
	}
	owner := metav1.GetControllerOf(p)
	if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "ReplicaSet" {
		return v, fmt.Errorf("pod %s/%s is not controlled by a ReplicaSet", p.Namespace, p.Name)
	}
	rs, err := r.client.AppsV1().ReplicaSets(p.Namespace).Get(ctx, owner.Name, metav1.GetOptions{})
	if err != nil {
		return v, err
	}
	if rs.UID != owner.UID || rs.DeletionTimestamp != nil || rs.Spec.Replicas == nil || *rs.Spec.Replicas < 1 {
		return v, fmt.Errorf("replicaset identity changed, is terminating, or is scaled down")
	}
	deploymentOwner := metav1.GetControllerOf(rs)
	if deploymentOwner == nil || deploymentOwner.APIVersion != "apps/v1" || deploymentOwner.Kind != "Deployment" {
		return v, fmt.Errorf("replicaset %s/%s is not controlled by a Deployment", rs.Namespace, rs.Name)
	}
	d, err := r.client.AppsV1().Deployments(p.Namespace).Get(ctx, deploymentOwner.Name, metav1.GetOptions{})
	if err != nil {
		return v, err
	}
	if d.UID != deploymentOwner.UID || d.DeletionTimestamp != nil || d.Spec.Paused ||
		d.Spec.Replicas == nil || *d.Spec.Replicas < 1 {
		return v, fmt.Errorf("deployment %s/%s changed identity, is paused, or is scaled down", d.Namespace, d.Name)
	}
	if d.Status.ObservedGeneration < d.Generation || d.Status.UpdatedReplicas != *d.Spec.Replicas ||
		d.Status.Replicas != *d.Spec.Replicas {
		return v, waitingFor("waiting for Deployment %s/%s to stabilize", d.Namespace, d.Name)
	}
	v.ReplicaSet, v.ReplicaSetUID = rs.Name, rs.UID
	v.Deployment, v.DeploymentUID = d.Name, d.UID
	return v, nil
}

func (r *recovery) buildPlan(ctx context.Context, g candidate, pods []corev1.Pod, now time.Time) (*plan, error) {
	if len(g.Pods) > r.settings.MaxPods {
		return nil, fmt.Errorf("conflict exceeds maximum of %d Pods", r.settings.MaxPods)
	}
	node, err := r.client.CoreV1().Nodes().Get(ctx, g.Node, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	if err := readyNode(node); err != nil {
		return nil, err
	}
	// An existing cordon belongs to an operator or a different incident. Never adopt it.
	if node.Spec.Unschedulable || node.Annotations[incidentAnnotation] != "" {
		return nil, fmt.Errorf("node %s is already cordoned or has a recovery incident", node.Name)
	}
	p := &plan{ID: string(uuid.NewUUID()), Node: node.Name, NodeUID: node.UID, IPs: g.IPs, Started: now}
	for i := range g.Pods {
		v, err := r.workload(ctx, &g.Pods[i], now)
		if err != nil {
			return nil, err
		}
		p.Victims = append(p.Victims, v)
	}
	replicaSets := make(map[types.UID]bool)
	for _, v := range p.Victims {
		replicaSets[v.ReplicaSetUID] = true
	}
	baseline := make(map[types.UID]bool)
	for _, pod := range pods {
		owner := metav1.GetControllerOf(&pod)
		if owner != nil && owner.APIVersion == "apps/v1" && owner.Kind == "ReplicaSet" && replicaSets[owner.UID] {
			if pod.UID == "" {
				return nil, fmt.Errorf("replacement baseline contains an empty Pod UID")
			}
			if !baseline[pod.UID] {
				baseline[pod.UID] = true
				p.BaselineUIDs = append(p.BaselineUIDs, pod.UID)
			}
		}
	}
	if len(p.BaselineUIDs) > 10000 {
		return nil, fmt.Errorf("replacement baseline exceeds journal safety limit")
	}
	sort.Slice(p.BaselineUIDs, func(i, j int) bool { return p.BaselineUIDs[i] < p.BaselineUIDs[j] })
	return p, nil
}

func (r *recovery) step(ctx context.Context) (int, error) {
	pods, err := r.listPods(ctx)
	if err != nil {
		r.seen = nil
		return 0, err
	}
	groups, err := duplicates(pods)
	if err != nil {
		r.seen = nil
		return 0, err
	}
	now := r.now()
	observed := make(map[string]observation)
	for _, g := range groups {
		key := g.fingerprint()
		o, exists := r.seen[key]
		if !exists {
			o.Since = now
		}
		o.Count++
		observed[key] = o
		klog.InfoS("Duplicate Pod IPs detected", "node", g.Node, "IPs", g.IPs, "fingerprint", key,
			"observations", o.Count, "dryRun", r.settings.DryRun)
	}
	r.seen = observed
	if r.settings.DryRun {
		return len(groups), nil
	}
	cm, j, err := r.readJournal(ctx)
	if err != nil {
		return len(groups), err
	}
	if j.Pending != nil {
		return len(groups), r.advance(ctx, cm, j, pods, groups, now)
	}
	var blocked []error
	var waiting []error
	for _, g := range groups {
		o := observed[g.fingerprint()]
		if o.Count < 3 || now.Sub(o.Since) < r.settings.Confirm {
			continue
		}
		p, err := r.buildPlan(ctx, g, pods, now)
		if err != nil {
			if isWaiting(err) {
				klog.V(2).InfoS("Waiting for conflict owners to stabilize", "node", g.Node, "reason", err)
				waiting = append(waiting, err)
			} else {
				klog.ErrorS(err, "Skipping conflict which cannot be recovered automatically", "node", g.Node)
				blocked = append(blocked, fmt.Errorf("node %s: %w", g.Node, err))
			}
			continue
		}
		var history []completion
		for _, h := range j.History {
			if now.Before(h.At) || now.Sub(h.At) < r.settings.Cooldown {
				return len(groups), fmt.Errorf("recovery cooldown is active")
			}
			if now.Sub(h.At) < 24*time.Hour {
				history = append(history, h)
			}
		}
		if len(history) >= r.settings.MaxDaily {
			return len(groups), fmt.Errorf("daily recovery limit reached")
		}
		j.History, j.Pending = history, p
		if err := r.saveJournal(ctx, cm, j); err != nil {
			return len(groups), err
		}
		klog.InfoS("Persisted recovery plan", "incident", p.ID, "node", p.Node, "victims", p.Victims)
		return len(groups), nil
	}
	if len(blocked) > 0 {
		return len(groups), errors.Join(blocked...)
	}
	return len(groups), errors.Join(waiting...)
}

func sameGroup(p *plan, groups []candidate) bool {
	for _, g := range groups {
		if g.Node != p.Node || strings.Join(g.IPs, ",") != strings.Join(p.IPs, ",") || len(g.Pods) != len(p.Victims) {
			continue
		}
		match := true
		for i := range g.Pods {
			match = match && g.Pods[i].UID == p.Victims[i].UID
		}
		return match
	}
	return false
}

func (r *recovery) finish(ctx context.Context, cm *corev1.ConfigMap, j journal, now time.Time, outcome string) error {
	p := j.Pending
	j.History = append(j.History, completion{ID: p.ID, Node: p.Node, At: now, Outcome: outcome})
	j.Pending = nil
	if err := r.saveJournal(ctx, cm, j); err != nil {
		return err
	}
	klog.InfoS("Recovery plan finished; any cordon is retained", "incident", p.ID, "node", p.Node, "outcome", outcome)
	return nil
}

func (r *recovery) cordon(ctx context.Context, node *corev1.Node, p *plan) error {
	return r.markNode(ctx, node, incidentAnnotation, p.ID, true)
}

func (r *recovery) markNode(ctx context.Context, node *corev1.Node, key, incident string, cordon bool) error {
	annotations := make(map[string]string)
	for k, v := range node.Annotations {
		annotations[k] = v
	}
	annotations[key] = incident
	patch := []map[string]interface{}{
		{"op": "test", "path": "/metadata/uid", "value": node.UID},
		{"op": "test", "path": "/metadata/resourceVersion", "value": node.ResourceVersion},
		{"op": "add", "path": "/metadata/annotations", "value": annotations},
	}
	if cordon {
		patch = append(patch, map[string]interface{}{"op": "add", "path": "/spec/unschedulable", "value": true})
	}
	data, err := json.Marshal(patch)
	if err != nil {
		return err
	}
	_, err = r.client.CoreV1().Nodes().Patch(ctx, node.Name, types.JSONPatchType, data, metav1.PatchOptions{})
	return err
}

func (r *recovery) deploymentsAvailable(ctx context.Context, p *plan) error {
	for _, v := range p.Victims {
		d, err := r.client.AppsV1().Deployments(v.Namespace).Get(ctx, v.Deployment, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if d.UID != v.DeploymentUID || d.DeletionTimestamp != nil || d.Spec.Paused ||
			d.Spec.Replicas == nil || *d.Spec.Replicas < 1 {
			return fmt.Errorf("deployment %s/%s changed identity, is paused, or is scaled down", v.Namespace, v.Deployment)
		}
		if d.Status.ObservedGeneration < d.Generation || d.Status.AvailableReplicas < *d.Spec.Replicas ||
			d.Status.UpdatedReplicas != *d.Spec.Replicas || d.Status.Replicas != *d.Spec.Replicas {
			return waitingFor("waiting for Deployment %s/%s to become fully available", v.Namespace, v.Deployment)
		}
	}
	return nil
}

func (r *recovery) replacementsReady(ctx context.Context, p *plan, pods []corev1.Pod, gone []victim,
	groups []candidate) error {
	needed := make(map[types.UID]int)
	owners := make(map[types.UID]victim)
	for _, v := range gone {
		needed[v.ReplicaSetUID]++
		owners[v.ReplicaSetUID] = v
	}
	conflicting := make(map[types.UID]bool)
	for _, g := range groups {
		for _, pod := range g.Pods {
			conflicting[pod.UID] = true
		}
	}
	baseline := make(map[types.UID]bool)
	for _, uid := range p.BaselineUIDs {
		baseline[uid] = true
	}
	counted := make(map[types.UID]bool)
	for _, pod := range pods {
		owner := metav1.GetControllerOf(&pod)
		if owner == nil || owner.APIVersion != "apps/v1" || owner.Kind != "ReplicaSet" ||
			pod.UID == "" || baseline[pod.UID] || pod.DeletionTimestamp != nil {
			continue
		}
		identity, related := owners[owner.UID]
		if !related || pod.Namespace != identity.Namespace || owner.Name != identity.ReplicaSet {
			continue
		}
		// Inspect every new related Pod, including those beyond the required quota, so
		// the result does not depend on whether a healthy Pod appears before a bad one.
		if conflicting[pod.UID] {
			return fmt.Errorf("replacement Pod %s/%s has duplicate IPs; manual intervention required", pod.Namespace, pod.Name)
		}
		if needed[owner.UID] == 0 || counted[pod.UID] || pod.Spec.HostNetwork || pod.Spec.NodeName == "" ||
			pod.Spec.NodeName == p.Node || pod.Status.Phase != corev1.PodRunning ||
			(len(pod.Status.PodIPs) == 0 && pod.Status.PodIP == "") {
			continue
		}
		ready := false
		for _, c := range pod.Status.Conditions {
			ready = ready || (c.Type == corev1.PodReady && c.Status == corev1.ConditionTrue)
		}
		if !ready {
			continue
		}
		n, err := r.client.CoreV1().Nodes().Get(ctx, pod.Spec.NodeName, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if n.DeletionTimestamp != nil || n.Spec.Unschedulable || n.Annotations[incidentAnnotation] != "" {
			continue
		}
		for _, c := range n.Status.Conditions {
			if c.Type == corev1.NodeReady && c.Status == corev1.ConditionTrue {
				needed[owner.UID]--
				counted[pod.UID] = true
				break
			}
		}
	}
	for rs, count := range needed {
		if count > 0 {
			return waitingFor("waiting for %d Ready replacement(s) of ReplicaSet UID %s outside the quarantined Node", count, rs)
		}
	}
	return nil
}

func (r *recovery) advance(ctx context.Context, cm *corev1.ConfigMap, j journal, pods []corev1.Pod,
	groups []candidate, now time.Time) error {
	p := j.Pending
	if now.Before(p.Started) || now.Sub(p.Started) > r.settings.Timeout {
		return fmt.Errorf("incident %s timed out or clock moved backwards; manual intervention required", p.ID)
	}
	node, err := r.client.CoreV1().Nodes().Get(ctx, p.Node, metav1.GetOptions{})
	if err != nil {
		return err
	}
	if node.UID != p.NodeUID {
		return fmt.Errorf("incident Node UID changed")
	}
	if err := readyNode(node); err != nil {
		return err
	}
	if node.Annotations[cancelledAnnotation] == p.ID {
		if p.Quarantined {
			return fmt.Errorf("quarantined incident has an invalid cancellation marker")
		}
		return r.finish(ctx, cm, j, now, "cancelled-before-cordon")
	}
	if node.Annotations[incidentAnnotation] != p.ID {
		if p.Quarantined {
			return fmt.Errorf("incident Node annotation removed externally; manual intervention required")
		}
		if node.Annotations[incidentAnnotation] != "" || node.Spec.Unschedulable {
			return fmt.Errorf("node is controlled by another operator or incident")
		}
		if !sameGroup(p, groups) {
			// Fence delayed cordon requests before completing a cancelled plan. A journal
			// update alone cannot stop a paused worker from patching an unchanged Node RV.
			// Keep cancellation history separate from the active incident annotation so
			// that a later independent incident on this Node can still be recovered.
			return r.markNode(ctx, node, cancelledAnnotation, p.ID, false)
		}
		for _, v := range p.Victims {
			pod, err := r.client.CoreV1().Pods(v.Namespace).Get(ctx, v.Name, metav1.GetOptions{})
			if err != nil {
				return err
			}
			current, err := r.workload(ctx, pod, now)
			if err != nil {
				return err
			}
			if current != v || pod.Spec.NodeName != p.Node {
				return fmt.Errorf("victim changed before cordon")
			}
		}
		// Workload validation can take longer than the Node heartbeat interval. Fetch a
		// fresh RV so routine status updates cannot indefinitely prevent the cordon.
		fresh, err := r.client.CoreV1().Nodes().Get(ctx, p.Node, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if fresh.UID != p.NodeUID || fresh.Spec.Unschedulable || fresh.Annotations[incidentAnnotation] != "" ||
			fresh.Annotations[cancelledAnnotation] == p.ID {
			return fmt.Errorf("node changed while preparing cordon")
		}
		if err := readyNode(fresh); err != nil {
			return err
		}
		// Read the journal AFTER the Node snapshot. If cancellation happens after this
		// read, its Node RV update invalidates our patch. If a later cancellation has
		// already replaced the tombstone, this read rejects the superseded plan ID.
		_, latest, err := r.readJournal(ctx)
		if err != nil {
			return err
		}
		if latest.Pending == nil || latest.Pending.ID != p.ID {
			return fmt.Errorf("recovery plan changed before cordon")
		}
		// The journal already exists before this atomic UID/RV-guarded cordon and annotation.
		return r.cordon(ctx, fresh, p)
	}
	if !node.Spec.Unschedulable {
		return fmt.Errorf("incident Node was uncordoned externally; refusing further evictions")
	}
	if !p.Quarantined {
		p.Quarantined = true
		j.Pending = p
		return r.saveJournal(ctx, cm, j)
	}

	allowed := make(map[types.UID]bool)
	for _, v := range p.Victims {
		allowed[v.UID] = true
	}
	for _, pod := range pods {
		if pod.Spec.NodeName != p.Node || pod.Spec.HostNetwork || allowed[pod.UID] ||
			pod.Status.Phase == corev1.PodSucceeded || pod.Status.Phase == corev1.PodFailed {
			continue
		}
		// Cordon is a scheduler filter, not a kubelet fence. A Pod already bound at cordon
		// time, a direct binding, or a DaemonSet can still allocate an IP. Never add new UIDs
		// to the immutable plan: unknown / late owners stop this incident for manual repair.
		ips := pod.Status.PodIPs
		if len(ips) == 0 && pod.Status.PodIP != "" {
			ips = []corev1.PodIP{{IP: pod.Status.PodIP}}
		}
		for _, ip := range ips {
			addr, err := netip.ParseAddr(ip.IP)
			if err != nil {
				return err
			}
			for _, affected := range p.IPs {
				if addr.Unmap().String() == affected {
					return fmt.Errorf("unplanned Pod %s/%s now owns affected IP %s", pod.Namespace, pod.Name, affected)
				}
			}
		}
	}

	var gone []victim
	for _, v := range p.Victims {
		pod, err := r.client.CoreV1().Pods(v.Namespace).Get(ctx, v.Name, metav1.GetOptions{})
		if apierrors.IsNotFound(err) || (err == nil && pod.UID != v.UID) {
			gone = append(gone, v)
			continue
		}
		if err != nil {
			return err
		}
		if pod.DeletionTimestamp != nil {
			return waitingFor("waiting for Pod %s/%s UID %s to terminate", v.Namespace, v.Name, v.UID)
		}
		if pod.Spec.NodeName != p.Node {
			return fmt.Errorf("victim Node changed")
		}
		// Wait for the preceding eviction's replacement before disrupting another replica.
		// PDB admission remains the authority; this check also bounds disruption without a PDB.
		if err := r.replacementsReady(ctx, p, pods, gone, groups); err != nil {
			return err
		}
		current, err := r.workload(ctx, pod, now)
		if err != nil {
			return err
		}
		if current != v {
			return fmt.Errorf("victim ownership changed")
		}
		// Re-read both fences immediately before the action. The immutable plan and UID/RV
		// preconditions make delayed / duplicate requests harmless after a process restart.
		_, latest, err := r.readJournal(ctx)
		if err != nil {
			return err
		}
		if latest.Pending == nil || latest.Pending.ID != p.ID {
			return fmt.Errorf("recovery plan changed before eviction")
		}
		fresh, err := r.client.CoreV1().Nodes().Get(ctx, p.Node, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if fresh.UID != p.NodeUID || !fresh.Spec.Unschedulable || fresh.Annotations[incidentAnnotation] != p.ID {
			return fmt.Errorf("node quarantine changed before eviction")
		}
		if err := readyNode(fresh); err != nil {
			return err
		}
		actionTime := r.now()
		if actionTime.Before(p.Started) || actionTime.Sub(p.Started) > r.settings.Timeout {
			return fmt.Errorf("incident deadline exceeded before eviction")
		}
		rv := pod.ResourceVersion
		err = r.client.PolicyV1().Evictions(v.Namespace).Evict(ctx, &policyv1.Eviction{
			ObjectMeta:    metav1.ObjectMeta{Namespace: v.Namespace, Name: v.Name},
			DeleteOptions: &metav1.DeleteOptions{Preconditions: &metav1.Preconditions{UID: &v.UID, ResourceVersion: &rv}},
		})
		if err != nil {
			// Includes 429 (PDB), 409 (identity / status changed), and ambiguous timeouts.
			// Never fall back to DELETE or force deletion. Reconcile from the API next scan.
			return fmt.Errorf("eviction of %s/%s UID %s: %w", v.Namespace, v.Name, v.UID, err)
		}
		klog.InfoS("Eviction accepted", "incident", p.ID, "Pod", klog.KRef(v.Namespace, v.Name), "UID", v.UID)
		return nil
	}
	if err := r.replacementsReady(ctx, p, pods, gone, groups); err != nil {
		return err
	}
	if err := r.deploymentsAvailable(ctx, p); err != nil {
		return err
	}
	return r.finish(ctx, cm, j, now, "replicas-available-node-quarantined")
}
