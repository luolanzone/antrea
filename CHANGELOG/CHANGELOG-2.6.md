# Changelog 2.6

## 2.6.0 - 2026-01-13

### Changed

- Include nftables information in Agent supportbundle. ([#7547](https://github.com/antrea-io/antrea/pull/7547), [@molegit9])
- [FlowAggregator] Generate self-signed certificates only if needed. ([#7559](https://github.com/antrea-io/antrea/pull/7559), [@andrew-su])
- Update K8s libraries to v1.35.0. ([#7668](https://github.com/antrea-io/antrea/pull/7668), [@antoninbas])
- Remove L7FlowExporter. ([#7593](https://github.com/antrea-io/antrea/pull/7593), [@antoninbas])
- Remove FlowExporter feature gate from Windows yaml. ([#7606](https://github.com/antrea-io/antrea/pull/7606), [@luolanzone])

### Fixed

- Fix handing self-destined connections on Egress Nodes in hybrid mode. ([#7611](https://github.com/antrea-io/antrea/pull/7611), [@hongliangl])
- Fix fillServiceUID to support services without port name. ([#7681](https://github.com/antrea-io/antrea/pull/7681), [@luolanzone])
- fix: missing service info to pod to lb flows. ([#7614](https://github.com/antrea-io/antrea/pull/7614), [@petertran-avgo])
- Update FlowAggregator helm chart to support installing to multiple namespace. ([#7670](https://github.com/antrea-io/antrea/pull/7670), [@andrew-su])
- Clarify rp_filter behavior in some Linux distributions and sysctl init option for Egress. ([#7661](https://github.com/antrea-io/antrea/pull/7661), [@hongliangl])
- Fix antctl mc join cleanup on error. ([#7649](https://github.com/antrea-io/antrea/pull/7649), [@SharanRP])
- Improve logging in Agent's NetworkPolicy controller. ([#7456](https://github.com/antrea-io/antrea/pull/7456), [@antoninbas])
- [FlowAggregator] Improve logging and minor refactor in cert provider. ([#7648](https://github.com/antrea-io/antrea/pull/7648), [@antoninbas])
- Use reserved OVS controller ports for Antrea SecondaryNetwork. ([#7645](https://github.com/antrea-io/antrea/pull/7645), [@luolanzone])
- Add init container to apply Antrea-specific sysctl configuration. ([#7651](https://github.com/antrea-io/antrea/pull/7651), [@hongliangl])
- Fix antrea-agent crashing when AntreaProxy is not enabled. ([#7636](https://github.com/antrea-io/antrea/pull/7636), [@hongliangl])
- Lock feature gates TopologyAwareHints and ServiceTrafficDistribution to true. ([#7620](https://github.com/antrea-io/antrea/pull/7620), [@hongliangl])
- Update module github.com/containernetworking/plugins to v1.9.0 [SECURITY] (main). ([#7622](https://github.com/antrea-io/antrea/pull/7622), [@app/renovate])

### CI/Tests Improvements

- Clean up saved image files when E2E job exits. ([#7590](https://github.com/antrea-io/antrea/pull/7590), [@luolanzone])
- Fix PR branch collision in post-release workflow. ([#7655](https://github.com/antrea-io/antrea/pull/7655), [@luolanzone])
- Fix Multi-cluster coverage build with no space left issue. ([#7652](https://github.com/antrea-io/antrea/pull/7652), [@luolanzone])
- Fix benchmark names and allow manual benchmark runs. ([#7664](https://github.com/antrea-io/antrea/pull/7664), [@luolanzone])
- test: backfill getAffectedNamespacesForAppliedto. ([#7560](https://github.com/antrea-io/antrea/pull/7560), [@petertran-avgo])
- Skip Windows unit tests for unsupported features. ([#7596](https://github.com/antrea-io/antrea/pull/7596), [@XinShuYang])
- Fix Prometheus test panic due to unset validation scheme. ([#7678](https://github.com/antrea-io/antrea/pull/7678), [@antoninbas])
- Fix testReconcileGatewayRoutesOnStartup with WireGuard. ([#7635](https://github.com/antrea-io/antrea/pull/7635), [@xliuxu])

### Documents

- Fix typo in network-flow-visibility doc. ([#7679](https://github.com/antrea-io/antrea/pull/7679), [@Atish-iaf])
- Clarify Multicast support in hybrid mode in feature-gates.md. ([#7653](https://github.com/antrea-io/antrea/pull/7653), [@hongliangl])
- Fix typo in Multicluster docs. ([#7640](https://github.com/antrea-io/antrea/pull/7640), [@antoninbas])


### Dependencies Upgrade

- fix(deps): update ginkgo dependencies (main). ([#7618](https://github.com/antrea-io/antrea/pull/7618), [@app/renovate])
- chore(deps): update peter-evans/create-pull-request action to v8 (main). ([#7623](https://github.com/antrea-io/antrea/pull/7623), [@app/renovate])
- fix(deps): update golang.org/x (main). ([#7619](https://github.com/antrea-io/antrea/pull/7619), [@app/renovate])
- fix(deps): update golang.org/x (main). ([#7616](https://github.com/antrea-io/antrea/pull/7616), [@app/renovate])
- Update module github.com/spf13/cobra to v1.10.2 (main). ([#7607](https://github.com/antrea-io/antrea/pull/7607), [@app/renovate])
- fix(deps): update module google.golang.org/protobuf to v1.36.11 (main). ([#7629](https://github.com/antrea-io/antrea/pull/7629), [@app/renovate])
- fix(deps): update module github.com/hashicorp/memberlist to v0.5.4 (main). ([#7631](https://github.com/antrea-io/antrea/pull/7631), [@app/renovate])
- fix(deps): update module github.com/miekg/dns to v1.1.70 (main). ([#7677](https://github.com/antrea-io/antrea/pull/7677), [@app/renovate])
- Update module github.com/miekg/dns to v1.1.69 (main). ([#7626](https://github.com/antrea-io/antrea/pull/7626), [@app/renovate])
- chore(deps): update artifact actions (main). ([#7630](https://github.com/antrea-io/antrea/pull/7630), [@app/renovate])
- chore(deps): update actions/cache action to v5 (main). ([#7627](https://github.com/antrea-io/antrea/pull/7627), [@app/renovate])
