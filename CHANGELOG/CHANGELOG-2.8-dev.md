# Changelog 2.8 (2026-09-01 to 2026-09-29)

## 2.8.0 - dev

### Added

- Authenticate FlowStreamService clients using Kubernetes bearer tokens or client certificates, and enforce stream concurrency limits. ([#8191](https://github.com/antrea-io/antrea/pull/8191), [@Dyanngg])
- Authorize FlowStreamService requests using Kubernetes RBAC via SubjectAccessReviews on virtual "flows" resources in flow.antrea.io, with sensitive field redaction for unauthorized endpoints. ([#8276](https://github.com/antrea-io/antrea/pull/8276), [@Dyanngg])
- Add sequence numbers and resume tokens to FlowStreamService to allow clients to resume interrupted flow streams without losing or duplicating records. ([#8432](https://github.com/antrea-io/antrea/pull/8432), [@Dyanngg] [@antoninbas])

### Changed

- Enable the FlowStreamService gRPC server by default for Flow Aggregator. ([#8456](https://github.com/antrea-io/antrea/pull/8456), [@Dyanngg])
- Send an initial empty flow response at FlowStreamService stream establishment so clients can immediately confirm authorization and stream readiness. ([#8420](https://github.com/antrea-io/antrea/pull/8420), [@Dyanngg])
- Mount host paths (/etc/cni/net.d and /lib/modules) read-only in the antctl check cluster Job. ([#8424](https://github.com/antrea-io/antrea/pull/8424), [@ayushsarode])
- Improve error messages and input validation for missing or malformed peer arguments in antctl query networkpolicyevaluation. ([#8375](https://github.com/antrea-io/antrea/pull/8375), [@sakethalladaaa])
- Disable IPv6 Router Advertisement (RA) and SLAAC on container interfaces when IPAM is configured. ([#8302](https://github.com/antrea-io/antrea/pull/8302), [@wenqiq])
- Upgrade Go to 1.27. ([#8367](https://github.com/antrea-io/antrea/pull/8367), [@antoninbas])

### Fixed

- Clean up stale multicast remote receivers and OpenFlow flows on Node deletion when a Node is deleted abruptly without sending an IGMP Leave report. ([#8275](https://github.com/antrea-io/antrea/pull/8275), [@wenyingd])
- Fix lock release timing during CNI CmdAdd rollback to ensure the container lock is held throughout rollback execution. ([#8309](https://github.com/antrea-io/antrea/pull/8309), [@hangyan])
- Reload OVS after updating port trunks in OVSDB so VLAN configuration changes take effect immediately. ([#8353](https://github.com/antrea-io/antrea/pull/8353), [@luolanzone])
- Fix data race in Multi-cluster MemberClusterSetReconciler between periodic status updates and reconciliation state changes. ([#8321](https://github.com/antrea-io/antrea/pull/8321), [@luolanzone])
- Fix internal NetworkPolicyType constant mismatch for ClusterNetworkPolicy (CNP) so flow records correctly export the policy type. ([#8372](https://github.com/antrea-io/antrea/pull/8372), [@Dyanngg])
- Retry NodeLatencyMonitor socket creation with exponential backoff on failure, preventing transient host errors from disabling latency monitoring permanently. ([#8310](https://github.com/antrea-io/antrea/pull/8310), [@luolanzone])

[@Dyanngg]: https://github.com/Dyanngg
[@antoninbas]: https://github.com/antoninbas
[@ayushsarode]: https://github.com/ayushsarode
[@hangyan]: https://github.com/hangyan
[@luolanzone]: https://github.com/luolanzone
[@sakethalladaaa]: https://github.com/sakethalladaaa
[@wenqiq]: https://github.com/wenqiq
[@wenyingd]: https://github.com/wenyingd
