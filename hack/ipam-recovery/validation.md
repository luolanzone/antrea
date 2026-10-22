# Antrea v2.6.2 recovery validation

## Environment and scope

Validated locally on 2026-10-22 in a newly created, disposable Kind cluster named
`ipam-recovery-262`, separate from the existing development cluster. Every Kubernetes
command used the disposable cluster's explicit kubeconfig. The reproduction deliberately
changes Pod status and restarts an Agent; it must never run against production.

- Kubernetes v1.36.1; containerd v2.3.1; Linux amd64, kernel 6.8.0-110-generic.
- One control-plane Node and two worker Nodes; Kind default CNI disabled.
- Cluster Pod CIDR `10.200.0.0/16`, Node Pod CIDRs `/28`, to exercise actual host-local
  round-robin reuse without creating hundreds of Pods.
- Official [v2.6.2 release manifest](https://github.com/antrea-io/antrea/releases/download/v2.6.2/antrea.yml)
  and official Agent / Controller images, without a source patch or substituted Agent binary.
- `antrea-agent --version` inside the cluster reported
  `antrea-agent version v2.6.2 linux/amd64 go1.26.4`.
- The recovery image was built from this branch with its current Go dependencies; the
  cluster ran the supplied Deployment, ServiceAccount and RBAC, using in-cluster credentials.
  Only test scheduling, a capacity-test toleration and timing arguments were overridden:
  `--interval=5s --confirm-duration=30s --min-pod-age=1m --v=2`.
  The production manifest retains its longer default confirmation and minimum age.

The release manifest SHA-256 was:

```text
12190fcf9bbebc6217d6915c58e38fd6d772475f9d6a0934e563b41ce0cb5280
```

Pulled image repository digests (multi-platform manifest digests):

```text
antrea/antrea-agent-ubuntu:v2.6.2
sha256:76f0e7d8b0e768b53c964bc47a67b963fd6a64374ae5121528c905d80feff349
antrea/antrea-controller-ubuntu:v2.6.2
sha256:852b645f2f64001f76d308f9763c949964bc94cdea903e9eae75332c5bcddcf4
```

## Confirming an unfixed release and reproducing the failure

The local `v2.6.2` tag resolves to `384f784fb34420bd11b59ca3e2660d5d70dd8af3`.
Its [CNIServer reconciliation source](https://github.com/antrea-io/antrea/blob/v2.6.2/pkg/agent/cniserver/pod_configuration.go)
populates `desiredPodIPs` only from API Pod status and passes that set to host-local GC.
It does not preserve the additional local interface IPs introduced by
[PR #8240](https://github.com/antrea-io/antrea/pull/8240).

The test followed [issue #8239](https://github.com/antrea-io/antrea/issues/8239), using
single-replica nginx Deployments rather than bare Pods so recovery ownership checks applied:

1. Temporarily reserve the second worker for later recovery and create an opted-in victim
   on the first worker. Verify its running sandbox, IP and host-local lease.
2. Hold that victim's API status IPs empty with repeated status-subresource patches, then
   kill only that worker's `antrea-agent` process. Stop the patch loop after startup GC
   removes the live lease. Do not manually remove or modify allocation files.
3. Wait for kubelet to republish the unchanged sandbox IP. Create separate opted-in
   single-replica Deployments until host-local wraps the small allocation range.
4. Verify two running sandboxes, API Pod statuses and OVSDB Port records claim the same
   address. Restore the spare worker's scheduling eligibility before recovery.

Observed Agent ordering:

```text
07:09:18.858  Starting reconciliation for CNI server
07:09:18.861  Unused IP was successfully released ... IP="10.200.0.34"
07:09:19.081  Syncing Pod interface ... Pod="ipam-validation/victim-7c5dc9d85b-h8chv"
```

Both Pods subsequently had IP `10.200.0.34` on `ipam-recovery-262-worker`:

| Original Pod | UID |
| --- | --- |
| `victim-7c5dc9d85b-h8chv` | `6031d6e8-9480-42e1-bf15-80c58abca76d` |
| `allocator-12-789f546f5c-tl6cb` | `38d910d3-2742-4d18-b69b-0d36e76eef2b` |

OVSDB Ports `victim-7-09d5f8` and `allocato-cce1c8` recorded distinct container IDs and
network namespaces with that same `ip-address`. This was a real allocation conflict;
the test did not assign duplicate IPs by patching status.

## Recovery results

| Case | Observed result |
| --- | --- |
| Monitor only | Duplicate logged over multiple scans; journal RV unchanged, no cordon or victim eviction. |
| Confirmation and quarantine | Both original UIDs persisted before cordon; Node UID and incident annotation recorded. |
| PDB denies disruption | Eviction returned PDB rejection; both original UIDs remained, Node stayed quarantined. |
| Graceful eviction | Original Pod remained terminating during its configured 20-second preStop; 45-second grace retained. |
| Restart during eviction | Scale controller to zero, wait for it to exit, then restart; same journal incident and victims resumed. |
| Missing replacement capacity | Temporary NoSchedule taint on spare worker held replacement Pending; next original UID stayed alive. |
| Normal waiting | `/readyz` and `/healthz` succeeded; waiting=1, blocked=0, errors=0 in the restarted process. |
| Capacity restored | Remove only the test capacity taint; replacement became Ready before the next eviction. |
| Entire original conflict group | Both original UIDs disappeared, including the remaining owner after the first duplicate disappeared. |
| Application traffic | Three HTTP requests to each replacement from an unaffected Pod on the quarantined Node succeeded. |
| Completion | No pending plan, one completed history entry, both Deployments fully available, cluster Pod IPs unique. |
| Node isolation retained | Original Node remained unschedulable with its incident annotation after completion. |

Incident `1f40795f-2eaf-4004-b72d-f2d435b07a52` was persisted at `07:12:07Z` and completed
at `07:13:37Z`, with outcome `replicas-available-node-quarantined`. The original ReplicaSet
owner references were retained by both replacements on `ipam-recovery-262-worker2`:

| Replacement Pod | IP | UID |
| --- | --- | --- |
| `allocator-12-789f546f5c-c5h5z` | `10.200.0.26` | `29d6af93-cc4a-472b-b037-8a6556ec29d8` |
| `victim-7c5dc9d85b-ns7z9` | `10.200.0.28` | `c62b61d2-52f2-4933-a99a-a3db3a7f05cb` |

The supplied container security settings were retained. Inspection of the running
container confirmed UID/GID 65532, effective capabilities zero, `NoNewPrivs: 1`,
`Seccomp: 2`, and a read-only root filesystem. Recovery needed no host mount, exec RBAC,
Agent restart permission or direct Pod delete permission.

## Actual API-server precondition behavior

Separate test Pods with PDBs exercised real `policy/v1` Eviction requests. An annotation
update made the supplied Pod RV stale; custom readiness gates controlled Ready status.
Each request included both UID and RV preconditions, as the workaround does.

| Pod / PDB state | Incorrect UID | Correct UID, stale RV |
| --- | --- | --- |
| Ready, disruption permitted | 409 Conflict, Pod retained | 409 Conflict, Pod retained |
| NotReady, `unhealthyPodEvictionPolicy: AlwaysAllow` | 409 Conflict, Pod retained | 201 Success, eviction accepted |

This confirms the documented RV limitation in the actual tested Kubernetes release.
The [v1.36.1 eviction implementation](https://github.com/kubernetes/kubernetes/blob/v1.36.1/pkg/registry/core/pod/storage/eviction.go#L219-L284)
replaces RV in PDB-authorized unhealthy-Pod paths while preserving UID. A default-policy
PDB with zero healthy Pods and `minAvailable: 0` instead returned 429 in a preliminary
probe; the final unhealthy-Pod precondition checks explicitly used `AlwaysAllow`.
The tool never changes a workload's PDB policy to obtain authorization.

## Other checks and limits

The package's race tests, vet, project-configured golangci checks on Linux and Windows,
formatting, repository markdownlint, container build and client-side manifest validation
passed. The actual recovery used Linux; the Windows check is a source lint check only.

This run validates recovery on Antrea v2.6.2 with the tested Kubernetes release and
workload shape. It does not certify every older release, dual-stack data plane, large
cluster scale, storage driver, admission policy, topology constraint or application.
Concurrent-worker / stale-request fences, malformed snapshots, ownership changes,
NotReady Nodes, deadlines and budgets are covered by unit tests, not fault-injected in
this cluster run. Repeat staging validation with the production environment's versions
and constraints. Permanent prevention still requires upgrading or backporting #8240;
workload availability alone does not authorize returning a quarantined Node to service.
