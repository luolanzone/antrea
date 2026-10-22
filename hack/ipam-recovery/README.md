# Host-local duplicate Pod IP recovery

This standalone workaround monitors duplicate primary Pod IPs on Antrea versions without
[PR #8240](https://github.com/antrea-io/antrea/pull/8240). It defaults to **monitor only**.
Recovery must be explicitly enabled and is limited to opted-in Linux host-local Nodes and
Deployment Pods. It requires Kubernetes `policy/v1` Eviction support (Kubernetes 1.22+).
It uses Kubernetes APIs and can run independently of the installed Antrea image.

## Root cause and recovery boundary

During Agent startup, old host-local IPAM garbage collection kept only addresses in the
API's Pod status. CNI ADD reserves an address before kubelet publishes that status. An
Agent restart during this interval can release a live allocation; a later CNI ADD can
reuse it. The fix additionally retains IPs recovered from local Pod interfaces / OVSDB.
See [issue #8239](https://github.com/antrea-io/antrea/issues/8239) for the reproduction.

API-only monitoring cannot detect a missing lease before a duplicate becomes visible,
and cannot prove sandbox or OVS cleanup. A single missing Pod IP is **not** evidence of
this bug. Neither ping failures nor Agent log matches are sufficient authorization to
restart a Pod. Upgrade / backport #8240 to prevent recurrence. This tool does not replace
that fix or repair host-local files.

Deleting just one IP owner may leave the other's flows damaged because duplicate IPs
can produce overlapping IP-based rules. Recovery therefore persists **all original
conflicting Pod UIDs** and replaces the entire group, even after the duplicate disappears.
It does not guess the legitimate owner based on creation time or allocation-file mtime.

The affected Node is cordoned **and remains cordoned**. Replacement replicas must become
Ready on other schedulable, Ready Nodes. This recovers workload capacity elsewhere; it
does not declare the quarantined Node's network repaired. Capacity for replacement Pods,
including volumes, affinity and topology constraints, is required. Do not opt in workloads
whose replacements cannot run elsewhere, or workloads that cannot tolerate graceful restarts.

Cordon is a scheduler filter, **not a kubelet / CNI fence**. Previously bound Pods,
DaemonSets, direct `nodeName` / binding clients, and Pods tolerating the unschedulable taint
can still start. A newly visible owner of an affected IP blocks the incident, without
adding its UID to the plan. An owner whose IP has not been published cannot be detected.
Automatic mode is intended for ordinary scheduler-managed Deployments in a cluster where
operators and admission controls prohibit bypassing quarantine for these workloads.
Node-local fencing and runtime / OVS inspection are required for stronger guarantees.
The tool never uncordons a Node based on API Pod disappearance.

## Recovery sequence and safeguards

1. List Pods in **all namespaces**, using a consistent paginated API snapshot. Ignore
   host-network and terminal Pods, but retain terminating owners as conflict evidence.
   Canonicalize IPv4 / IPv6, deduplicate UIDs, and detect both families of dual-stack Pods.
   Cross-Node duplicates and malformed data stop automatic recovery for manual diagnosis.
2. Require the same Node, duplicate IP set and Pod UID set for at least three successful
   scans and one minute. A failed Pod list, disappeared conflict or membership change resets
   confirmation. Default minimum Pod age is two minutes.
3. Require every owner to opt in, be Running and non-terminating, and belong to an existing
   ReplicaSet with a matching UID controlled by an existing Deployment with a matching UID.
   Reject paused / changing / scaled-down Deployments, critical Pods, bare Pods, static
   Pods, StatefulSets, Jobs and DaemonSets. Require the opted-in Linux Node to be Ready,
   not already cordoned, and not owned by another incident.
4. Persist an immutable victim list, baseline of existing ReplicaSet Pod UIDs, and incident
   ID in a pre-created ConfigMap using
   resourceVersion compare-and-swap. Persist before acting. Atomically cordon and annotate
   the Node using JSON Patch tests on its UID and resourceVersion. Confirm the quarantine
   in the journal before the first eviction.
   If the conflict changes before cordon, first write a cancellation marker to the Node
   without cordoning it, then archive the plan. This Node resourceVersion change fences
   delayed cordon requests from overlapping workers. Retain a separate
   `ipam-recovery.antrea.io/cancelled-incident` marker for audit. It does not block later
   independent incidents. Read the latest journal after the Node snapshot and before
   patching, so even a paused worker with an older cancellation marker cannot act on a
   superseded plan.
5. Recheck the journal, Node quarantine / UID / readiness, Pod opt-in and controller UIDs.
   Submit one graceful `policy/v1` Eviction with **both Pod UID and resourceVersion
   preconditions**. PDB admission is authoritative. Do not specify a shorter grace period.
   Never fall back to DELETE, force deletion, CNI DEL calls, lease-file edits or Agent restart.
6. Wait until that original UID disappears, then require a new Ready Pod from its original
   ReplicaSet outside the quarantined Node before evicting the next original UID. Require
   a UID absent from the persisted baseline, avoiding comparisons between controller and
   API-server creation timestamps (which can differ due to clock skew). A same-name
   replacement is never evicted. Resume the same plan after process / Deployment restart;
   confirmation of duplicate IPs is not required again once quarantine has begun.
   Require the replacement's namespace, ReplicaSet name and UID to match its original
   owner. A replacement involved in another visible IP conflict blocks the incident for
   manual intervention; it cannot authorize the next eviction or completion.
7. After every original UID is gone, verify replacement Pods and full Deployment availability,
   then record completion. Retain the Node annotation and cordon for operator inspection.

Workers coordinate through one durable journal rather than an expiring leader lease.
Only one incident can be active. Journal creation / completion use optimistic concurrency;
overlapping workers can only retry the first surviving original UID in the same plan.
An ineligible conflict is logged and skipped when selecting a new incident, allowing
other eligible Nodes to recover. An already active blocked incident remains authoritative.
UID preconditions protect against Pod recreation; requested RV preconditions narrow stale decisions.
This is idempotent reconciliation, not an exactly-once transaction across Kubernetes objects.
No API can atomically combine a Node readiness / cordon check with Pod eviction. Operators
must stop all controller copies before modifying quarantine, its journal, Node / workload opt-in
or controller ownership.

RV enforcement depends on the API server. For example, the
[Kubernetes 1.35 eviction implementation](https://github.com/kubernetes/kubernetes/blob/v1.35.0/pkg/registry/core/pod/storage/eviction.go#L266-L284)
replaces a supplied RV with its current Pod RV in some PDB-authorized unready-Pod paths.
The Pod UID is still checked. Do not treat label removal as instantaneous cancellation
of an already persisted / in-flight eviction. Verify both Ready and unready Pod eviction
preconditions against the actual Kubernetes release during staging; clients cannot repair
server-side precondition behavior.

A PDB rejection (429), conflict (409), timeout, API outage, ownership change, missing
replacement, NotReady Node or unexpected owner prevents further evictions. Normal Pod
termination / replacement startup is reported as waiting, keeps the monitor Ready and
does not increment error counters. Prolonged waiting has a separate alert and still
reaches the incident deadline. An ambiguous eviction
timeout is resolved by reading Pod state again. A 30-minute incident deadline blocks all
further actions and requires manual intervention. The Node remains quarantined. Defaults
also limit an incident to eight Pods, allow one completed incident per 30 minutes, and at
most three incidents in a rolling 24 hours. These limits survive restarts in the journal.
Cancelled plans also consume the incident budget.

Malformed Pod IP data intentionally stops automatic work cluster-wide. Ignoring that
Pod could omit an owner from a conflict group. Repair the source data before resuming;
this conservative containment policy applies even if the malformed Pod is not opted in.

The availability check uses Kubernetes Ready / available status. It does not verify
application correctness or clear existing client connections. Configure meaningful
readiness probes, PDBs and application monitoring before enabling recovery. A restrictive
PDB can intentionally block recovery; the controller never overrides it.

## Build and monitor locally

Build from this checkout (using its Go toolchain and dependencies), even when the cluster
runs an older Antrea release:

```bash
go build -o /tmp/antrea-ipam-recovery ./hack/ipam-recovery
CGO_ENABLED=1 go test -race -count=1 ./hack/ipam-recovery
/tmp/antrea-ipam-recovery --kubeconfig="$HOME/.kube/config" --once
/tmp/antrea-ipam-recovery --kubeconfig="$HOME/.kube/config" --dry-run=true
```

`--once` only supports monitor mode. Read-only mode never writes the journal, cordons
Nodes or evicts Pods. The executable is also suitable for a supervised host process.
When using an external kubeconfig, use the same restricted permissions as the manifest.

## Deploy and enable deliberately

Build a container from the repository root:

```bash
docker build -f hack/ipam-recovery/Dockerfile -t YOUR_REGISTRY/antrea-ipam-recovery:reviewed .
```

Publish through your normal image pipeline. Replace the sample image in `deploy.yml`
with the reviewed image's immutable digest. Initially keep `--dry-run=true`, and apply the
Deployment manifest. Bootstrap the separate journal with `kubectl create` once; an
existing journal is rejected instead of overwritten. Use reviewed manifests for production.
The container runs as
non-root with a read-only filesystem, no host mounts, no exec access and no privileges.
Size CPU / memory and the scan interval against the full cluster Pod inventory in staging;
the example resource limits are not a capacity guarantee for large clusters. API scans
retain a complete Pod snapshot and have a one-minute reconciliation deadline. The journal
baseline is limited to 10,000 distinct related Pod UIDs; exceeding it prevents recovery.
RBAC allows only Pod reads / eviction, Node reads / patch, workload reads and updates of
the one journal ConfigMap. Node patch and cluster-wide eviction are powerful permissions:
restrict access to the ServiceAccount, image and journal. Opt-in checks are implemented
in the controller and are not enforced by RBAC.

```bash
kubectl apply -f hack/ipam-recovery/deploy.yml
kubectl create -f hack/ipam-recovery/state.yml
kubectl -n antrea-ipam-recovery logs -f deployment/antrea-ipam-recovery
```

After confirming the Node uses Antrea's **host-local** IPAM, authorize that Node:

```bash
kubectl label node NODE ipam-recovery.antrea.io/host-local=true
```

Authorize selected workload Pods through their Deployment template. This template change
itself causes a normal rollout; schedule it through your normal change process:

```yaml
spec:
  template:
    metadata:
      labels:
        ipam-recovery.antrea.io/enabled: "true"
```

Do not add this label to the recovery controller itself, Antrea components or system
workloads. Ensure recovery has spare capacity outside the Nodes it monitors, configure
PDBs, and exercise the failure cases below in staging. Finally change the recovery
Deployment argument to `--dry-run=false` using your reviewed deployment configuration.
Run a single replica with `Recreate` as provided; accidental overlap is safe for the same
journal, but **separate journals / controller installations are not supported**.

**Preserve `state.json` across updates.** Do not delete / recreate the state ConfigMap,
reset its contents, or blindly reapply its initial value while a controller is running.
`state.yml` is a one-time bootstrap file; exclude it from recurring apply / GitOps
reconciliation. For subsequent upgrades apply only `deploy.yml`, which contains no journal
data. Back up the journal before maintenance. Resetting it discards recovery
progress and the persistent disruption budget.

## Monitoring and manual intervention

Scrape `/metrics` on port 8080. Alert on:

- `antrea_ipam_recovery_duplicate_nodes > 0` (including monitor-only mode).
- `antrea_ipam_recovery_blocked == 1` for persistent recovery failures.
- `antrea_ipam_recovery_waiting == 1` when its
  `antrea_ipam_recovery_waiting_since_timestamp_seconds` exceeds the workload's expected
  termination / startup time, to detect insufficient capacity or stuck replacements.
- Missing metrics, an unready controller, or a stale
  `antrea_ipam_recovery_last_success_timestamp_seconds`.
- Nodes with `ipam-recovery.antrea.io/incident` annotations, even after workload recovery.

Logs contain the Node, IPs, incident ID, original UIDs and blocked reason. `/healthz`
measures process health; `/readyz` requires recent successful reconciliation, including
healthy progress waiting. API / PDB
failures do not trigger liveness restart loops or erase the journal.

Before manual intervention, stop the recovery Deployment (scale it to zero) and ensure
any external copy of the executable has also stopped. Export the journal and inspect
the recorded Pod UIDs, Node UID, Deployment / ReplicaSet UIDs and actual Pod states.
For each quarantined Node, inspect the runtime sandboxes, OVS interfaces / flows and
host-local allocations using the installed version's diagnostics; API deletion alone
does not prove cleanup. Resolve unexpected owners and stuck CNI teardown. Upgrade or
backport the upstream fix before returning the Node to service. Only then remove the
incident annotation and uncordon through your normal operational procedure.

For a blocked / expired pending plan, reconcile its original UIDs and replacement replicas
manually before archiving it in the journal's history and clearing `pending`. Preserve
the history and version. The archive entry must contain the original plan's `id` and `node`,
the actual resolution time as RFC 3339 UTC in `at`, and `"outcome":"manually-resolved"`.
Use `kubectl replace` on the exported ConfigMap with its original resourceVersion so a
concurrent change is rejected. Do not clear a pending plan merely to bypass a PDB or retry the
same Node. Never remove host-local allocation files with `gc-host-local.sh` to fix this
bug: its purpose is reclaiming leaked allocations, while this failure prematurely freed
an allocation that was still in use.

## Verification

An actual startup-GC failure and recovery on official Antrea v2.6.2 images is recorded in
[the local validation report](validation.md), including PDB, restart, missing-capacity and
API-server eviction-precondition checks.

Unit tests exercise dual-stack canonicalization, complete owner discovery, confirmation,
opt-in / ownership checks, journal CAS, concurrent workers, persisted restart recovery,
graceful UID/RV eviction, PDB / conflict / ambiguous-timeout handling, same-name replacement,
late owners, external uncordon, Node replacement, missing capacity and incident budgets.

Before production rollout, use an isolated staging cluster running the affected Antrea
build and the upstream issue's reproduction. Verify duplicate detection in monitor mode,
PDB blocking, successful workload recovery elsewhere, graceful termination, controller
restart mid-incident, insufficient capacity, NotReady Nodes, the retained Node cordon,
Ready / unready eviction UID/RV behavior, and execution with all of the manifest's
container security settings.
Never run the reproduction against production: it deliberately removes Pod status IPs.
