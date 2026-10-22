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
	"fmt"
	"sync"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
	ktesting "k8s.io/client-go/testing"
)

var testTime = time.Date(2026, 10, 22, 12, 0, 0, 0, time.UTC)

func owner(kind, name, uid string) []metav1.OwnerReference {
	controller := true
	return []metav1.OwnerReference{{APIVersion: "apps/v1", Kind: kind, Name: name, UID: types.UID(uid), Controller: &controller}}
}

func testPod(name, ip string) corev1.Pod {
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "workload", UID: types.UID(name), ResourceVersion: "1",
			Labels: map[string]string{enabledLabel: "true"}, CreationTimestamp: metav1.NewTime(testTime.Add(-time.Hour)),
			OwnerReferences: owner("ReplicaSet", name+"-rs", name+"-rs")},
		Spec:   corev1.PodSpec{NodeName: "bad-node"},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: ip, PodIPs: []corev1.PodIP{{IP: ip}}},
	}
}

func fixture(t *testing.T) (*recovery, *fake.Clientset) {
	t.Helper()
	objects := []runtime.Object{
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "bad-node", UID: "bad-node-uid", ResourceVersion: "1",
			Labels: map[string]string{hostLocalLabel: "true", corev1.LabelOSStable: "linux"}},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}},
		&corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "good-node", UID: "good-node-uid"},
			Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}}},
		&corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "state", Namespace: "recovery", ResourceVersion: "1"},
			Data: map[string]string{stateKey: `{"version":1}`}},
	}
	for _, name := range []string{"a", "b"} {
		pod := testPod(name, "10.10.1.10")
		replicas := int32(1)
		objects = append(objects, &pod,
			&appsv1.ReplicaSet{ObjectMeta: metav1.ObjectMeta{Name: name + "-rs", Namespace: "workload", UID: types.UID(name + "-rs"),
				OwnerReferences: owner("Deployment", name+"-deployment", name+"-deployment")},
				Spec: appsv1.ReplicaSetSpec{Replicas: &replicas}},
			&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: name + "-deployment", Namespace: "workload",
				UID: types.UID(name + "-deployment"), Generation: 1},
				Spec: appsv1.DeploymentSpec{Replicas: &replicas}, Status: appsv1.DeploymentStatus{ObservedGeneration: 1,
					Replicas: 1, UpdatedReplicas: 1, AvailableReplicas: 1}})
	}
	client := fake.NewSimpleClientset(objects...)
	// The client-go tracker does not implement resourceVersion concurrency checks. Model
	// apiserver compare-and-swap here so competing workers really test journal serialization.
	var stateMutex sync.Mutex
	version := 1
	client.PrependReactor("update", "configmaps", func(action ktesting.Action) (bool, runtime.Object, error) {
		stateMutex.Lock()
		defer stateMutex.Unlock()
		cm := action.(ktesting.UpdateAction).GetObject().(*corev1.ConfigMap)
		current, err := client.Tracker().Get(corev1.SchemeGroupVersion.WithResource("configmaps"), cm.Namespace, cm.Name)
		if err != nil {
			return true, nil, err
		}
		if cm.ResourceVersion != current.(*corev1.ConfigMap).ResourceVersion {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, cm.Name, fmt.Errorf("stale version"))
		}
		version++
		result := cm.DeepCopy()
		result.ResourceVersion = fmt.Sprint(version)
		if err := client.Tracker().Update(corev1.SchemeGroupVersion.WithResource("configmaps"), result, cm.Namespace); err != nil {
			return true, nil, err
		}
		return true, result, nil
	})
	r := &recovery{client: client, settings: settings{Namespace: "recovery", StateName: "state", Confirm: time.Minute,
		MinAge: 2 * time.Minute, Timeout: 30 * time.Minute, Cooldown: 30 * time.Minute, MaxDaily: 3, MaxPods: 8},
		now: func() time.Time { return testTime }}
	return r, client
}

func mustStep(t *testing.T, r *recovery) {
	t.Helper()
	if _, err := r.step(context.Background()); err != nil {
		t.Fatalf("reconcile failed: %v", err)
	}
}

func prepare(t *testing.T, r *recovery) *plan {
	t.Helper()
	for _, elapsed := range []time.Duration{0, 30 * time.Second, time.Minute} {
		at := testTime.Add(elapsed)
		r.now = func() time.Time { return at }
		mustStep(t, r)
	}
	_, j, err := r.readJournal(context.Background())
	if err != nil || j.Pending == nil {
		t.Fatalf("expected durable plan, journal=%+v error=%v", j, err)
	}
	return j.Pending
}

func quarantine(t *testing.T, r *recovery) *plan {
	t.Helper()
	p := prepare(t, r)
	mustStep(t, r) // Atomically cordon and annotate the Node.
	mustStep(t, r) // Persist confirmation of quarantine before any eviction.
	return p
}

func mutations(client *fake.Clientset) []ktesting.Action {
	var actions []ktesting.Action
	for _, a := range client.Actions() {
		if a.GetVerb() != "get" && a.GetVerb() != "list" {
			actions = append(actions, a)
		}
	}
	return actions
}

func evictionActions(client *fake.Clientset) []*policyv1.Eviction {
	var result []*policyv1.Eviction
	for _, a := range client.Actions() {
		if a.GetVerb() == "create" && a.GetSubresource() == "eviction" {
			result = append(result, a.(ktesting.CreateAction).GetObject().(*policyv1.Eviction))
		}
	}
	return result
}

func changePod(t *testing.T, client *fake.Clientset, name string, change func(*corev1.Pod)) {
	t.Helper()
	pod, err := client.CoreV1().Pods("workload").Get(context.Background(), name, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	change(pod)
	if _, err := client.CoreV1().Pods("workload").Update(context.Background(), pod, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func changeNode(t *testing.T, client *fake.Clientset, change func(*corev1.Node)) {
	t.Helper()
	node, err := client.CoreV1().Nodes().Get(context.Background(), "bad-node", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	change(node)
	if _, err := client.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func replace(t *testing.T, client *fake.Clientset, name string) {
	t.Helper()
	if err := client.CoreV1().Pods("workload").Delete(context.Background(), name, metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	pod := testPod(name+"-new", "10.10.2."+map[string]string{"a": "10", "b": "11"}[name])
	pod.Spec.NodeName = "good-node"
	pod.CreationTimestamp = metav1.NewTime(testTime.Add(2 * time.Minute))
	pod.OwnerReferences = owner("ReplicaSet", name+"-rs", name+"-rs")
	pod.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if _, err := client.CoreV1().Pods("workload").Create(context.Background(), &pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
}

func TestDuplicates(t *testing.T) {
	for _, tc := range []struct {
		name      string
		change    func([]corev1.Pod) []corev1.Pod
		groups    int
		wantError bool
	}{
		{name: "same-node", groups: 1},
		{name: "protected-owner-still-counted", groups: 1, change: func(p []corev1.Pod) []corev1.Pod { p[1].Labels = nil; return p }},
		{name: "terminating-owner-still-counted", groups: 1, change: func(p []corev1.Pod) []corev1.Pod {
			p[1].DeletionTimestamp = &metav1.Time{Time: testTime}
			return p
		}},
		{name: "host-network", change: func(p []corev1.Pod) []corev1.Pod { p[1].Spec.HostNetwork = true; return p }},
		{name: "terminal-stale-IP", change: func(p []corev1.Pod) []corev1.Pod { p[1].Status.Phase = corev1.PodSucceeded; return p }},
		{name: "IP-not-published", change: func(p []corev1.Pod) []corev1.Pod { p[1].Status.PodIPs = nil; p[1].Status.PodIP = ""; return p }},
		{name: "legacy-PodIP", groups: 1, change: func(p []corev1.Pod) []corev1.Pod { p[1].Status.PodIPs = nil; return p }},
		{name: "dual-stack-canonicalized", groups: 1, change: func(p []corev1.Pod) []corev1.Pod {
			p[0].Status.PodIPs = append(p[0].Status.PodIPs, corev1.PodIP{IP: "fd00::1"})
			p[1].Status.PodIPs = append(p[1].Status.PodIPs, corev1.PodIP{IP: "fd00:0:0:0:0:0:0:1"})
			return p
		}},
		{name: "mapped-IPv4", groups: 1, change: func(p []corev1.Pod) []corev1.Pod { p[1].Status.PodIPs[0].IP = "::ffff:10.10.1.10"; return p }},
		{name: "repeated-same-UID", change: func(p []corev1.Pod) []corev1.Pod { return []corev1.Pod{p[0], p[0]} }},
		{name: "different-Nodes", wantError: true, change: func(p []corev1.Pod) []corev1.Pod { p[1].Spec.NodeName = "other"; return p }},
		{name: "invalid-IP", wantError: true, change: func(p []corev1.Pod) []corev1.Pod { p[1].Status.PodIPs[0].IP = "invalid"; return p }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pods := []corev1.Pod{testPod("a", "10.10.1.10"), testPod("b", "10.10.1.10")}
			if tc.change != nil {
				pods = tc.change(pods)
			}
			groups, err := duplicates(pods)
			if (err != nil) != tc.wantError || len(groups) != tc.groups {
				t.Fatalf("got %d groups and error %v; want %d groups, error=%t", len(groups), err, tc.groups, tc.wantError)
			}
			if tc.name == "dual-stack-canonicalized" && (len(groups[0].Pods) != 2 || len(groups[0].IPs) != 2) {
				t.Fatalf("dual-stack group must deduplicate UIDs: %+v", groups)
			}
		})
	}
}

func TestDryRunHasNoWrites(t *testing.T) {
	r, client := fixture(t)
	r.settings.DryRun = true
	for range 5 {
		mustStep(t, r)
	}
	if actions := mutations(client); len(actions) != 0 {
		t.Fatalf("dry-run wrote to cluster: %v", actions)
	}
}

func TestConfirmationResetsAfterFailedList(t *testing.T) {
	r, client := fixture(t)
	mustStep(t, r)
	r.now = func() time.Time { return testTime.Add(time.Minute) }
	fail := true
	client.PrependReactor("list", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		if fail {
			return true, nil, fmt.Errorf("api unavailable")
		}
		return false, nil, nil
	})
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("expected list failure")
	}
	fail = false
	mustStep(t, r)
	mustStep(t, r)
	if len(mutations(client)) != 0 {
		t.Fatal("failed scan must reset confirmation")
	}
}

func TestEligibilityFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *recovery, *fake.Clientset)
	}{
		{"no-pod-opt-in", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "b", func(p *corev1.Pod) { p.Labels = nil })
		}},
		{"no-node-opt-in", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changeNode(t, c, func(n *corev1.Node) { delete(n.Labels, hostLocalLabel) })
		}},
		{"Windows", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changeNode(t, c, func(n *corev1.Node) { n.Labels[corev1.LabelOSStable] = "windows" })
		}},
		{"not-ready", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changeNode(t, c, func(n *corev1.Node) { n.Status.Conditions = nil })
		}},
		{"existing-cordon", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changeNode(t, c, func(n *corev1.Node) { n.Spec.Unschedulable = true })
		}},
		{"bare-Pod", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "b", func(p *corev1.Pod) { p.OwnerReferences = nil })
		}},
		{"StatefulSet", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "b", func(p *corev1.Pod) { p.OwnerReferences = owner("StatefulSet", "db", "db") })
		}},
		{"DaemonSet", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "b", func(p *corev1.Pod) { p.OwnerReferences = owner("DaemonSet", "agent", "agent") })
		}},
		{"critical", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "b", func(p *corev1.Pod) { p.Spec.PriorityClassName = "system-cluster-critical" })
		}},
		{"young", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "b", func(p *corev1.Pod) { p.CreationTimestamp = metav1.NewTime(testTime) })
		}},
		{"terminating", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "b", func(p *corev1.Pod) { p.DeletionTimestamp = &metav1.Time{Time: testTime} })
		}},
		{"too-many-victims", func(t *testing.T, r *recovery, c *fake.Clientset) { r.settings.MaxPods = 1 }},
		{"replicaset-UID-mismatch", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "b", func(p *corev1.Pod) { p.OwnerReferences[0].UID = "new-UID" })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, c := fixture(t)
			tc.change(t, r, c)
			c.ClearActions()
			mustStep(t, r)
			r.now = func() time.Time { return testTime.Add(30 * time.Second) }
			mustStep(t, r)
			r.now = func() time.Time { return testTime.Add(time.Minute) }
			if _, err := r.step(context.Background()); err == nil {
				t.Fatal("expected eligibility rejection")
			}
			if len(mutations(c)) != 0 {
				t.Fatalf("ineligible conflict must have no writes: %v", mutations(c))
			}
		})
	}
}

func TestRecoveryResumesAndEvictsEntireOriginalGroup(t *testing.T) {
	r, c := fixture(t)
	p := quarantine(t, r)
	c.ClearActions()
	mustStep(t, r)
	evictions := evictionActions(c)
	if len(evictions) != 1 || evictions[0].Name != "a" {
		t.Fatalf("expected first victim eviction: %v", evictions)
	}
	options := evictions[0].DeleteOptions
	if options == nil || options.Preconditions == nil || *options.Preconditions.UID != "a" ||
		*options.Preconditions.ResourceVersion != "1" || options.GracePeriodSeconds != nil {
		t.Fatalf("eviction must carry UID/RV and normal graceful termination: %+v", options)
	}
	changePod(t, c, "a", func(p *corev1.Pod) { p.DeletionTimestamp = &metav1.Time{Time: testTime} })
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("must wait for terminating UID")
	}
	if len(evictionActions(c)) != 0 {
		t.Fatal("must not advance while first UID still exists")
	}
	replace(t, c, "a")
	// A new process has no observation cache. The durable plan must still evict b even
	// though there is no longer a duplicate in Pod status, because its flows may be damaged.
	restarted := &recovery{client: c, settings: r.settings, now: r.now}
	c.ClearActions()
	mustStep(t, restarted)
	if e := evictionActions(c); len(e) != 1 || e[0].Name != "b" {
		t.Fatalf("must finish original group after restart: %v", e)
	}
	replace(t, c, "b")
	mustStep(t, restarted)
	_, j, err := r.readJournal(context.Background())
	if err != nil || j.Pending != nil || len(j.History) != 1 || j.History[0].ID != p.ID ||
		j.History[0].Outcome != "replicas-available-node-quarantined" {
		t.Fatalf("unexpected journal: %+v error=%v", j, err)
	}
	node, err := c.CoreV1().Nodes().Get(context.Background(), "bad-node", metav1.GetOptions{})
	if err != nil || !node.Spec.Unschedulable || node.Annotations[incidentAnnotation] != p.ID {
		t.Fatalf("quarantine must be retained: %+v error=%v", node, err)
	}
}

func TestNotReadyVictimsCanStartRecovery(t *testing.T) {
	r, c := fixture(t)
	for _, name := range []string{"a-deployment", "b-deployment"} {
		d, err := c.AppsV1().Deployments("workload").Get(context.Background(), name, metav1.GetOptions{})
		if err != nil {
			t.Fatal(err)
		}
		d.Status.AvailableReplicas = 0
		if _, err := c.AppsV1().Deployments("workload").Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	quarantine(t, r)
	c.ClearActions()
	mustStep(t, r)
	if len(evictionActions(c)) != 1 {
		t.Fatal("unavailable replicas must not deadlock first eviction; PDB decides admission")
	}
}

func TestSameDeploymentUnavailableReplicasRecoverSequentially(t *testing.T) {
	r, c := fixture(t)
	changePod(t, c, "b", func(p *corev1.Pod) { p.OwnerReferences = owner("ReplicaSet", "a-rs", "a-rs") })
	d, err := c.AppsV1().Deployments("workload").Get(context.Background(), "a-deployment", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	replicas := int32(2)
	d.Spec.Replicas = &replicas
	d.Status.Replicas, d.Status.UpdatedReplicas, d.Status.AvailableReplicas = 2, 2, 0
	if _, err := c.AppsV1().Deployments("workload").Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	quarantine(t, r)
	c.ClearActions()
	mustStep(t, r)
	if e := evictionActions(c); len(e) != 1 || e[0].Name != "a" {
		t.Fatalf("expected first eviction: %v", e)
	}
	replace(t, c, "a")
	// Only the replacement of the preceding victim must be Ready. Waiting for full
	// availability here would deadlock while the other victim is still broken.
	d.Status.AvailableReplicas = 1
	if _, err := c.AppsV1().Deployments("workload").Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	c.ClearActions()
	mustStep(t, r)
	if e := evictionActions(c); len(e) != 1 || e[0].Name != "b" {
		t.Fatalf("must allow second eviction with one recovered replica: %v", e)
	}
	replace(t, c, "b")
	changePod(t, c, "b-new", func(p *corev1.Pod) { p.OwnerReferences = owner("ReplicaSet", "a-rs", "a-rs") })
	d.Status.AvailableReplicas = 2
	if _, err := c.AppsV1().Deployments("workload").Update(context.Background(), d, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	mustStep(t, r)
	_, j, err := r.readJournal(context.Background())
	if err != nil || j.Pending != nil {
		t.Fatalf("same-Deployment recovery did not complete: %+v error=%v", j, err)
	}
}

func TestMembershipChangeRestartsConfirmation(t *testing.T) {
	r, c := fixture(t)
	mustStep(t, r)
	r.now = func() time.Time { return testTime.Add(30 * time.Second) }
	mustStep(t, r)
	changePod(t, c, "b", func(p *corev1.Pod) { p.UID = "b-recreated" })
	c.ClearActions()
	r.now = func() time.Time { return testTime.Add(time.Minute) }
	mustStep(t, r)
	if len(mutations(c)) != 0 {
		t.Fatal("UID membership change must restart confirmation")
	}
}

func TestOptInRevokedBeforeCordon(t *testing.T) {
	r, c := fixture(t)
	prepare(t, r)
	changePod(t, c, "b", func(p *corev1.Pod) { p.Labels = nil })
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("must revalidate every victim before cordon")
	}
	if len(mutations(c)) != 0 {
		t.Fatalf("must not cordon after opt-in is revoked: %v", mutations(c))
	}
}

func TestPodRecreatedBetweenReadAndEvictionFailsPrecondition(t *testing.T) {
	r, c := fixture(t)
	quarantine(t, r)
	gets := 0
	c.PrependReactor("get", "nodes", func(a ktesting.Action) (bool, runtime.Object, error) {
		gets++
		if gets != 2 {
			return false, nil, nil
		}
		object, err := c.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "workload", "a")
		if err != nil {
			return true, nil, err
		}
		pod := object.(*corev1.Pod).DeepCopy()
		pod.UID, pod.ResourceVersion = "new-pod", "2"
		if err := c.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, "workload"); err != nil {
			return true, nil, err
		}
		return false, nil, nil
	})
	c.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		e := a.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
		if *e.DeleteOptions.Preconditions.UID != "a" || *e.DeleteOptions.Preconditions.ResourceVersion != "1" {
			t.Fatalf("must send the previously verified identity, got %+v", e.DeleteOptions.Preconditions)
		}
		return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "a", fmt.Errorf("uid precondition failed"))
	})
	c.ClearActions()
	if _, err := r.step(context.Background()); !apierrors.IsConflict(err) {
		t.Fatalf("expected fenced eviction rejection, got %v", err)
	}
	for _, a := range mutations(c) {
		if a.GetSubresource() != "eviction" {
			t.Fatalf("unsafe fallback after reincarnation: %v", a)
		}
	}
}

func TestWaitsForReplacementBeforeNextEviction(t *testing.T) {
	r, c := fixture(t)
	quarantine(t, r)
	replace(t, c, "a")
	changePod(t, c, "a-new", func(p *corev1.Pod) { p.Status.Conditions = nil })
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("must wait for Ready replacement")
	}
	if len(evictionActions(c)) != 0 {
		t.Fatal("must not evict next victim without Ready replacement")
	}
}

func TestExistingReadyReplicaDoesNotCountAsReplacement(t *testing.T) {
	r, c := fixture(t)
	existing := testPod("already-ready", "10.10.2.20")
	existing.Spec.NodeName = "good-node"
	existing.OwnerReferences = owner("ReplicaSet", "a-rs", "a-rs")
	existing.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	if _, err := c.CoreV1().Pods("workload").Create(context.Background(), &existing, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	quarantine(t, r)
	if err := c.CoreV1().Pods("workload").Delete(context.Background(), "a", metav1.DeleteOptions{}); err != nil {
		t.Fatal(err)
	}
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("baseline replica must not stand in for a new replacement")
	}
	if len(evictionActions(c)) != 0 {
		t.Fatal("must not advance with only a pre-existing Ready replica")
	}
}

func TestReplacementDetectionDoesNotDependOnClockAgreement(t *testing.T) {
	r, c := fixture(t)
	quarantine(t, r)
	replace(t, c, "a")
	changePod(t, c, "a-new", func(p *corev1.Pod) {
		// Model an apiserver clock behind the controller. The new UID is authoritative.
		p.CreationTimestamp = metav1.NewTime(testTime.Add(-time.Minute))
	})
	c.ClearActions()
	mustStep(t, r)
	if e := evictionActions(c); len(e) != 1 || e[0].Name != "b" {
		t.Fatalf("clock skew must not block verified new replacement: %v", e)
	}
}

func TestReplacementOwnerNamespaceAndNameMustMatch(t *testing.T) {
	for _, wrongNamespace := range []bool{false, true} {
		t.Run(fmt.Sprint(wrongNamespace), func(t *testing.T) {
			r, c := fixture(t)
			quarantine(t, r)
			replace(t, c, "a")
			pod, err := c.CoreV1().Pods("workload").Get(context.Background(), "a-new", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			if wrongNamespace {
				if err := c.CoreV1().Pods("workload").Delete(context.Background(), pod.Name, metav1.DeleteOptions{}); err != nil {
					t.Fatal(err)
				}
				pod.Namespace = "another-namespace"
				if _, err := c.CoreV1().Pods(pod.Namespace).Create(context.Background(), pod, metav1.CreateOptions{}); err != nil {
					t.Fatal(err)
				}
			} else {
				changePod(t, c, pod.Name, func(p *corev1.Pod) { p.OwnerReferences[0].Name = "wrong-rs-name" })
			}
			c.ClearActions()
			if _, err := r.step(context.Background()); !isWaiting(err) {
				t.Fatalf("invalid owner identity must not count as replacement: %v", err)
			}
			if len(evictionActions(c)) != 0 {
				t.Fatal("must not evict next victim using an unrelated replacement")
			}
		})
	}
}

func TestConflictingReplacementCannotAuthorizeFurtherRecovery(t *testing.T) {
	r, c := fixture(t)
	p := quarantine(t, r)
	replace(t, c, "a")
	pod := testPod("unrelated-owner", "10.10.2.10")
	pod.Spec.NodeName = "good-node"
	if _, err := c.CoreV1().Pods(pod.Namespace).Create(context.Background(), &pod, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	good := testPod("another-healthy-replacement", "10.10.2.30")
	good.Spec.NodeName = "good-node"
	good.OwnerReferences = owner("ReplicaSet", "a-rs", "a-rs")
	good.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}}
	items, err := r.listPods(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	items = append([]corev1.Pod{good}, items...)
	groups, err := duplicates(items)
	if err != nil {
		t.Fatal(err)
	}
	if err := r.replacementsReady(context.Background(), p, items, p.Victims[:1], groups); err == nil || isWaiting(err) {
		t.Fatalf("healthy-first ordering must not hide a later conflicting replacement: %v", err)
	}
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil || isWaiting(err) {
		t.Fatalf("known duplicate on replacement Node must be a real blocker: %v", err)
	}
	if len(evictionActions(c)) != 0 {
		t.Fatal("must not disrupt another victim with a conflicting replacement")
	}
	// Completion must enforce the same check, even if the other original UID disappears.
	replace(t, c, "b")
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil || isWaiting(err) {
		t.Fatalf("conflicting replacement must also block completion: %v", err)
	}
	_, j, err := r.readJournal(context.Background())
	if err != nil || j.Pending == nil {
		t.Fatalf("must not archive incomplete recovery: %+v error=%v", j, err)
	}
}

func TestScaledDownReplicaSetCannotBeRecovered(t *testing.T) {
	r, c := fixture(t)
	rs, err := c.AppsV1().ReplicaSets("workload").Get(context.Background(), "a-rs", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	zero := int32(0)
	rs.Spec.Replicas = &zero
	if _, err := c.AppsV1().ReplicaSets("workload").Update(context.Background(), rs, metav1.UpdateOptions{}); err != nil {
		t.Fatal(err)
	}
	mustStep(t, r)
	r.now = func() time.Time { return testTime.Add(30 * time.Second) }
	mustStep(t, r)
	r.now = func() time.Time { return testTime.Add(time.Minute) }
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("old scaled-down ReplicaSet cannot create replacements")
	}
	if len(mutations(c)) != 0 {
		t.Fatalf("must not start recovery against inactive ReplicaSet: %v", mutations(c))
	}
}

func TestEvictionFailuresNeverFallBackToDelete(t *testing.T) {
	for _, err := range []error{apierrors.NewTooManyRequests("PDB rejects eviction", 10),
		apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, "a", fmt.Errorf("resourceVersion changed")),
		apierrors.NewTimeoutError("ambiguous timeout", 0)} {
		t.Run(err.Error(), func(t *testing.T) {
			r, c := fixture(t)
			quarantine(t, r)
			c.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
				if a.GetSubresource() == "eviction" {
					return true, nil, err
				}
				return false, nil, nil
			})
			c.ClearActions()
			if _, got := r.step(context.Background()); got == nil {
				t.Fatal("expected eviction failure")
			}
			for _, a := range mutations(c) {
				if a.GetVerb() != "create" || a.GetSubresource() != "eviction" {
					t.Fatalf("unsafe fallback: %v", a)
				}
			}
		})
	}
}

func TestFencesPreventEviction(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*testing.T, *recovery, *fake.Clientset)
	}{
		{"Node-UID-changed", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changeNode(t, c, func(n *corev1.Node) { n.UID = "replacement-node" })
		}},
		{"uncordoned", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changeNode(t, c, func(n *corev1.Node) { n.Spec.Unschedulable = false })
		}},
		{"annotation-removed", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changeNode(t, c, func(n *corev1.Node) { delete(n.Annotations, incidentAnnotation) })
		}},
		{"NotReady", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changeNode(t, c, func(n *corev1.Node) { n.Status.Conditions = nil })
		}},
		{"pod-opt-in-revoked", func(t *testing.T, r *recovery, c *fake.Clientset) {
			changePod(t, c, "a", func(p *corev1.Pod) { p.Labels = nil })
		}},
		{"deadline", func(t *testing.T, r *recovery, c *fake.Clientset) {
			r.now = func() time.Time { return testTime.Add(time.Hour) }
		}},
		{"clock-backwards", func(t *testing.T, r *recovery, c *fake.Clientset) {
			r.now = func() time.Time { return testTime.Add(-time.Hour) }
		}},
		{"late-unplanned-owner", func(t *testing.T, r *recovery, c *fake.Clientset) {
			p := testPod("late", "10.10.1.10")
			p.Status.PodIPs = nil
			if _, err := c.CoreV1().Pods("workload").Create(context.Background(), &p, metav1.CreateOptions{}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, c := fixture(t)
			quarantine(t, r)
			tc.change(t, r, c)
			c.ClearActions()
			if _, err := r.step(context.Background()); err == nil {
				t.Fatal("expected fence rejection")
			}
			if len(mutations(c)) != 0 {
				t.Fatalf("fence must prevent mutations: %v", mutations(c))
			}
		})
	}
}

func TestSameNameReplacementIsNeverEvicted(t *testing.T) {
	r, c := fixture(t)
	quarantine(t, r)
	changePod(t, c, "a", func(p *corev1.Pod) {
		p.UID = "new-UID"
		p.Spec.NodeName = "good-node"
		p.Status.PodIPs = []corev1.PodIP{{IP: "10.10.2.10"}}
	})
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("must wait for verified new replacement")
	}
	if len(evictionActions(c)) != 0 {
		t.Fatal("must never evict same-name replacement")
	}
}

func TestChangedConflictBeforeCordonCancelsPlan(t *testing.T) {
	r, c := fixture(t)
	p := prepare(t, r)
	staleNode, err := c.CoreV1().Nodes().Get(context.Background(), p.Node, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	changePod(t, c, "b", func(p *corev1.Pod) { p.Status.PodIPs = []corev1.PodIP{{IP: "10.10.1.11"}} })
	c.ClearActions()
	mustStep(t, r)
	for _, a := range mutations(c) {
		if a.GetResource().Resource != "nodes" || a.GetVerb() != "patch" {
			t.Fatalf("cancel must fence the Node before archiving: %v", a)
		}
	}
	// Model the apiserver's RV increment, which the fake tracker does not implement.
	changeNode(t, c, func(n *corev1.Node) { n.ResourceVersion = "2" })
	if err := r.cordon(context.Background(), staleNode, p); err == nil {
		t.Fatal("cancelled plan must reject delayed cordon request")
	}
	mustStep(t, r)
	_, j, err := r.readJournal(context.Background())
	if err != nil || j.Pending != nil || len(j.History) != 1 || j.History[0].Outcome != "cancelled-before-cordon" {
		t.Fatalf("unexpected cancellation journal: %+v error=%v", j, err)
	}
	node, err := c.CoreV1().Nodes().Get(context.Background(), p.Node, metav1.GetOptions{})
	if err != nil || node.Spec.Unschedulable || node.Annotations[cancelledAnnotation] != p.ID ||
		node.Annotations[incidentAnnotation] != "" {
		t.Fatalf("cancellation must retain a marker without cordoning: %+v error=%v", node, err)
	}
}

func TestBaselineDeduplicatesRepeatedPodEntries(t *testing.T) {
	r, c := fixture(t)
	items := []corev1.Pod{testPod("a", "10.10.1.10"), testPod("b", "10.10.1.10"), testPod("a", "10.10.1.10")}
	c.PrependReactor("list", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		return true, &corev1.PodList{Items: items}, nil
	})
	p := prepare(t, r)
	if len(p.BaselineUIDs) != 2 {
		t.Fatalf("baseline must deduplicate UIDs: %v", p.BaselineUIDs)
	}
	if _, _, err := r.readJournal(context.Background()); err != nil {
		t.Fatalf("generated plan must remain readable: %v", err)
	}
}

func TestCancelledNodeCanRecoverLaterIncident(t *testing.T) {
	r, c := fixture(t)
	old := prepare(t, r)
	changePod(t, c, "b", func(p *corev1.Pod) { p.Status.PodIPs = []corev1.PodIP{{IP: "10.10.1.11"}} })
	mustStep(t, r)
	mustStep(t, r)
	changePod(t, c, "b", func(p *corev1.Pod) { p.Status.PodIPs = []corev1.PodIP{{IP: "10.10.1.10"}} })
	for _, elapsed := range []time.Duration{time.Hour, time.Hour + 30*time.Second, time.Hour + time.Minute} {
		at := testTime.Add(elapsed)
		r.now = func() time.Time { return at }
		mustStep(t, r)
	}
	_, j, err := r.readJournal(context.Background())
	if err != nil || j.Pending == nil || j.Pending.ID == old.ID {
		t.Fatalf("later incident must be planned: %+v error=%v", j, err)
	}
	mustStep(t, r)
	node, err := c.CoreV1().Nodes().Get(context.Background(), old.Node, metav1.GetOptions{})
	if err != nil || !node.Spec.Unschedulable || node.Annotations[incidentAnnotation] != j.Pending.ID ||
		node.Annotations[cancelledAnnotation] != old.ID {
		t.Fatalf("later incident must quarantine without losing tombstone: %+v error=%v", node, err)
	}
}

func TestSupersededPlanCannotCordonAfterCancellationMarkerRotates(t *testing.T) {
	r, c := fixture(t)
	prepare(t, r)
	cm, old, err := r.readJournal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	changePod(t, c, "b", func(p *corev1.Pod) { p.Status.PodIPs = []corev1.PodIP{{IP: "10.10.1.11"}} })
	mustStep(t, r)
	mustStep(t, r)
	changeNode(t, c, func(n *corev1.Node) {
		n.Annotations[cancelledAnnotation] = "later-cancelled-incident"
		n.ResourceVersion = "3"
	})
	changePod(t, c, "b", func(p *corev1.Pod) { p.Status.PodIPs = []corev1.PodIP{{IP: "10.10.1.10"}} })
	pods, err := r.listPods(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	groups, err := duplicates(pods)
	if err != nil {
		t.Fatal(err)
	}
	c.ClearActions()
	if err := r.advance(context.Background(), cm, old, pods, groups, r.now()); err == nil {
		t.Fatal("stale plan must fail latest-journal fence")
	}
	if len(mutations(c)) != 0 {
		t.Fatalf("superseded worker must not cordon: %v", mutations(c))
	}
}

func TestReplacementCountRequiresDistinctPodUIDs(t *testing.T) {
	for _, duplicateRows := range []bool{false, true} {
		t.Run(fmt.Sprint(duplicateRows), func(t *testing.T) {
			r, c := fixture(t)
			changePod(t, c, "b", func(p *corev1.Pod) { p.OwnerReferences = owner("ReplicaSet", "a-rs", "a-rs") })
			p := quarantine(t, r)
			replace(t, c, "a")
			if err := c.CoreV1().Pods("workload").Delete(context.Background(), "b", metav1.DeleteOptions{}); err != nil {
				t.Fatal(err)
			}
			pods, err := r.listPods(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if duplicateRows {
				pods = append(pods, pods[0])
			} else {
				node, err := c.CoreV1().Nodes().Get(context.Background(), "good-node", metav1.GetOptions{})
				if err != nil {
					t.Fatal(err)
				}
				node.Status.Conditions = append(node.Status.Conditions, node.Status.Conditions[0])
				if _, err := c.CoreV1().Nodes().Update(context.Background(), node, metav1.UpdateOptions{}); err != nil {
					t.Fatal(err)
				}
			}
			if err := r.replacementsReady(context.Background(), p, pods, p.Victims, nil); !isWaiting(err) {
				t.Fatalf("one Pod must not count as two replacements: %v", err)
			}
		})
	}
}

func TestExpectedProgressKeepsMonitorReady(t *testing.T) {
	var status monitorState
	interval := 15 * time.Second
	if status.ready(testTime, interval) {
		t.Fatal("uninitialized monitor must not be Ready")
	}
	status.update(1, nil, testTime)
	if status.ready(testTime.Add(-time.Minute), interval) {
		t.Fatal("backwards clock must not hide a stale success timestamp")
	}
	later := testTime.Add(3 * time.Minute)
	status.update(0, waitingFor("waiting for replacement"), later)
	if !status.ready(later, interval) || status.blocked.Load() != 0 || status.failures.Load() != 0 ||
		status.waiting.Load() != 1 || status.waitingSince.Load() != later.Unix() {
		t.Fatal("normal progress must retain readiness without false errors")
	}
	status.update(0, waitingFor("still waiting"), later.Add(time.Minute))
	if status.waitingSince.Load() != later.Unix() {
		t.Fatal("waiting start must remain stable while waiting")
	}
	status.update(0, fmt.Errorf("pdb rejects eviction"), later.Add(2*time.Minute))
	if status.blocked.Load() != 1 || status.failures.Load() != 1 || status.waiting.Load() != 0 || status.waitingSince.Load() != 0 {
		t.Fatal("real blocker must count as an error")
	}
	if status.ready(later.Add(5*time.Minute), interval) {
		t.Fatal("persistent real failures must eventually fail readiness")
	}
}

func TestJournalCASRejectsCompetingPlan(t *testing.T) {
	r, _ := fixture(t)
	ctx := context.Background()
	cm1, j1, err := r.readJournal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	cm2, j2, err := r.readJournal(ctx)
	if err != nil {
		t.Fatal(err)
	}
	j1.History = []completion{{ID: "first", Node: "node", At: testTime, Outcome: "cancelled-before-cordon"}}
	j2.History = []completion{{ID: "second", Node: "node", At: testTime, Outcome: "cancelled-before-cordon"}}
	if err := r.saveJournal(ctx, cm1, j1); err != nil {
		t.Fatal(err)
	}
	if err := r.saveJournal(ctx, cm2, j2); !apierrors.IsConflict(err) {
		t.Fatalf("stale writer must fail CAS: %v", err)
	}
}

func TestTwoWorkersOnlyEvictFirstExistingUID(t *testing.T) {
	r, c := fixture(t)
	quarantine(t, r)
	var mu sync.Mutex
	c.PrependReactor("create", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.GetSubresource() != "eviction" {
			return false, nil, nil
		}
		mu.Lock()
		defer mu.Unlock()
		eviction := a.(ktesting.CreateAction).GetObject().(*policyv1.Eviction)
		object, err := c.Tracker().Get(corev1.SchemeGroupVersion.WithResource("pods"), "workload", eviction.Name)
		if err != nil {
			return true, nil, err
		}
		pod := object.(*corev1.Pod).DeepCopy()
		if pod.UID != *eviction.DeleteOptions.Preconditions.UID || pod.ResourceVersion != *eviction.DeleteOptions.Preconditions.ResourceVersion {
			return true, nil, apierrors.NewConflict(schema.GroupResource{Resource: "pods"}, pod.Name, fmt.Errorf("precondition failed"))
		}
		pod.DeletionTimestamp = &metav1.Time{Time: testTime}
		pod.ResourceVersion = "2"
		if err := c.Tracker().Update(corev1.SchemeGroupVersion.WithResource("pods"), pod, "workload"); err != nil {
			return true, nil, err
		}
		return true, &metav1.Status{Status: "Success"}, nil
	})
	c.ClearActions()
	other := &recovery{client: c, settings: r.settings, now: r.now}
	var wg sync.WaitGroup
	for _, worker := range []*recovery{r, other} {
		wg.Add(1)
		go func(worker *recovery) {
			defer wg.Done()
			_, err := worker.step(context.Background())
			if err != nil {
				t.Logf("concurrent worker safely blocked: %v", err)
			}
		}(worker)
	}
	wg.Wait()
	for _, e := range evictionActions(c) {
		if e.Name != "a" {
			t.Fatalf("concurrent workers advanced to second UID: %+v", e)
		}
	}
}

func TestCorruptJournalAndBudgetBlockNewActions(t *testing.T) {
	for _, tc := range []struct {
		name string
		data string
	}{
		{"invalid-json", `{"version":`},
		{"unknown-version", `{"version":2}`},
		{"empty-plan", `{"version":1,"pending":{}}`},
		{"unknown-field", `{"version":1,"typo":true}`},
		{"trailing-JSON", `{"version":1} {"version":1}`},
		{"cooldown", `{"version":1,"history":[{"id":"old","node":"n","at":"2026-10-22T12:00:00Z","outcome":"cancelled-before-cordon"}]}`},
		{"daily-budget", `{"version":1,"history":[
			{"id":"1","node":"n1","at":"2026-10-22T10:00:00Z","outcome":"cancelled-before-cordon"},
			{"id":"2","node":"n2","at":"2026-10-22T09:00:00Z","outcome":"cancelled-before-cordon"},
			{"id":"3","node":"n3","at":"2026-10-22T08:00:00Z","outcome":"cancelled-before-cordon"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, c := fixture(t)
			cm, err := c.CoreV1().ConfigMaps("recovery").Get(context.Background(), "state", metav1.GetOptions{})
			if err != nil {
				t.Fatal(err)
			}
			cm.Data[stateKey] = tc.data
			if _, err := c.CoreV1().ConfigMaps("recovery").Update(context.Background(), cm, metav1.UpdateOptions{}); err != nil {
				t.Fatal(err)
			}
			c.ClearActions()
			for _, elapsed := range []time.Duration{0, 30 * time.Second, time.Minute} {
				at := testTime.Add(elapsed)
				r.now = func() time.Time { return at }
				_, err = r.step(context.Background())
			}
			if err == nil {
				t.Fatal("expected journal or budget failure")
			}
			if len(mutations(c)) != 0 {
				t.Fatalf("journal/budget failure must prevent actions: %v", mutations(c))
			}
		})
	}
}

func TestListPaginationRequiresCompleteSnapshot(t *testing.T) {
	r, c := fixture(t)
	call := 0
	c.PrependReactor("list", "pods", func(a ktesting.Action) (bool, runtime.Object, error) {
		call++
		if call == 1 {
			return true, &corev1.PodList{ListMeta: metav1.ListMeta{Continue: "page2", ResourceVersion: "100"},
				Items: []corev1.Pod{testPod("a", "10.10.1.10")}}, nil
		}
		return true, nil, apierrors.NewResourceExpired("snapshot expired")
	})
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("must fail closed on incomplete paginated snapshot")
	}
	if call != 2 || len(mutations(c)) != 0 {
		t.Fatalf("expected two pages and no writes, pages=%d mutations=%v", call, mutations(c))
	}
}

func TestEvictionRechecksNodeAtLastMoment(t *testing.T) {
	r, c := fixture(t)
	quarantine(t, r)
	gets := 0
	c.PrependReactor("get", "nodes", func(a ktesting.Action) (bool, runtime.Object, error) {
		if a.(ktesting.GetAction).GetName() != "bad-node" {
			return false, nil, nil
		}
		gets++
		if gets != 2 {
			return false, nil, nil
		}
		object, err := c.Tracker().Get(corev1.SchemeGroupVersion.WithResource("nodes"), "", "bad-node")
		if err != nil {
			return true, nil, err
		}
		node := object.(*corev1.Node).DeepCopy()
		node.Spec.Unschedulable = false
		return true, node, nil
	})
	c.ClearActions()
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("must observe last-moment uncordon")
	}
	if len(evictionActions(c)) != 0 {
		t.Fatal("must not evict after Node fence changes")
	}
}

func TestJournalPlanJSONRoundTrip(t *testing.T) {
	r, _ := fixture(t)
	p := prepare(t, r)
	data, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	var decoded plan
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.ID != p.ID || decoded.Victims[0] != p.Victims[0] {
		t.Fatalf("durable identity lost: %+v", decoded)
	}
}

func TestStaleCordonPatchCannotOverwriteNodeChanges(t *testing.T) {
	r, c := fixture(t)
	p := prepare(t, r)
	node, err := c.CoreV1().Nodes().Get(context.Background(), p.Node, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	changeNode(t, c, func(n *corev1.Node) {
		n.ResourceVersion = "2"
		n.Annotations = map[string]string{"operator": "maintenance"}
	})
	if err := r.cordon(context.Background(), node, p); err == nil {
		t.Fatal("stale Node patch must fail its resourceVersion test")
	}
	fresh, err := c.CoreV1().Nodes().Get(context.Background(), p.Node, metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Spec.Unschedulable || fresh.Annotations["operator"] != "maintenance" || fresh.Annotations[incidentAnnotation] != "" {
		t.Fatalf("stale patch overwrote Node state: %+v", fresh)
	}
}

func TestNodeHeartbeatDuringValidationDoesNotPreventCordon(t *testing.T) {
	r, c := fixture(t)
	prepare(t, r)
	updated := false
	c.PrependReactor("get", "replicasets", func(a ktesting.Action) (bool, runtime.Object, error) {
		if updated {
			return false, nil, nil
		}
		updated = true
		object, err := c.Tracker().Get(corev1.SchemeGroupVersion.WithResource("nodes"), "", "bad-node")
		if err != nil {
			return true, nil, err
		}
		node := object.(*corev1.Node).DeepCopy()
		node.ResourceVersion = "2"
		if err := c.Tracker().Update(corev1.SchemeGroupVersion.WithResource("nodes"), node, ""); err != nil {
			return true, nil, err
		}
		return false, nil, nil
	})
	mustStep(t, r)
	node, err := c.CoreV1().Nodes().Get(context.Background(), "bad-node", metav1.GetOptions{})
	if err != nil || !node.Spec.Unschedulable {
		t.Fatalf("fresh RV must permit cordon after heartbeat: %+v error=%v", node, err)
	}
}

func TestManualResolutionPreservesCooldown(t *testing.T) {
	r, _ := fixture(t)
	cm, j, err := r.readJournal(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	j.History = []completion{{ID: "manual-incident", Node: "node", At: testTime, Outcome: "manually-resolved"}}
	if err := r.saveJournal(context.Background(), cm, j); err != nil {
		t.Fatal(err)
	}
	if _, _, err := r.readJournal(context.Background()); err != nil {
		t.Fatalf("documented manual resolution must parse: %v", err)
	}
	mustStep(t, r)
	r.now = func() time.Time { return testTime.Add(30 * time.Second) }
	mustStep(t, r)
	r.now = func() time.Time { return testTime.Add(time.Minute) }
	if _, err := r.step(context.Background()); err == nil {
		t.Fatal("manual resolution must retain cooldown protection")
	}
}

func TestIneligibleNodeDoesNotStarveEligibleNodes(t *testing.T) {
	r, c := fixture(t)
	changePod(t, c, "b", func(p *corev1.Pod) { p.Labels = nil })
	node, err := c.CoreV1().Nodes().Get(context.Background(), "bad-node", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	node.Name, node.UID, node.ResourceVersion = "later-node", "later-node-uid", ""
	if _, err := c.CoreV1().Nodes().Create(context.Background(), node, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"c", "d"} {
		pod := testPod(name, "10.10.3.10")
		pod.Spec.NodeName = "later-node"
		rs := []string{"a-rs", "b-rs"}[i]
		pod.OwnerReferences = owner("ReplicaSet", rs, rs)
		if _, err := c.CoreV1().Pods("workload").Create(context.Background(), &pod, metav1.CreateOptions{}); err != nil {
			t.Fatal(err)
		}
	}
	p := prepare(t, r)
	if p.Node != "later-node" || len(p.Victims) != 2 || p.Victims[0].UID != "c" || p.Victims[1].UID != "d" {
		t.Fatalf("protected first Node must not starve later eligible conflict: %+v", p)
	}
}
