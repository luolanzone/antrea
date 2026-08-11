# Changelog 2.7 (2026-07-07 to 2026-08-11)

## 2.7.0 - 2026-08-15

### Added

- Add AntreaNodeConfig CRD and AntreaNodeConfig-aware secondary network bridge management to allow users to define multiple uplinks and manage secondary bridge per node pool. ([#7812](https://github.com/antrea-io/antrea/pull/7812) [#7835](https://github.com/antrea-io/antrea/pull/7835) [#8039](https://github.com/antrea-io/antrea/pull/8039) [#8068](https://github.com/antrea-io/antrea/pull/8068) [#8149](https://github.com/antrea-io/antrea/pull/8149), [@luolanzone])
- Support external flows from NodePortLocal in flow exporter, preserving the original external client IP and destination node port. ([#8001](https://github.com/antrea-io/antrea/pull/8001), [@Dyanngg])
- Add ClusterNetworkPolicy network policy type in flow record for flows hitting ClusterNetworkPolicies. ([#8210](https://github.com/antrea-io/antrea/pull/8210), [@Dyanngg])
- Support ClusterNetworkPolicy in network-policy-api v0.2.0. ([#8018](https://github.com/antrea-io/antrea/pull/8018) [#8044](https://github.com/antrea-io/antrea/pull/8044), [@Dyanngg])

### Changed

- Support IPv6 SFTP URL in PacketCapture CRD. ([#8250](https://github.com/antrea-io/antrea/pull/8250), [@hangyan])
- Enforce strict Pod IP and IPPool address family validation to reject requests that assign multiple IPs of the same family to a single Pod. ([#7994](https://github.com/antrea-io/antrea/pull/7994), [@wenqiq])
- Change OFBridge get/set OFSwitch to use an atomic pointer. ([#8167](https://github.com/antrea-io/antrea/pull/8167), [@jianjuns])
- Upgrade Linux OVS to version 3.7.1. ([#8082](https://github.com/antrea-io/antrea/pull/8082), [@luolanzone])
- Deprecate the 'destinationClusterIP' IE for 'destinationServiceIP' in flow visibility. ([#8157](https://github.com/antrea-io/antrea/pull/8157), [@Dyanngg])
- Add documentation for restricting multicluster ServiceExports to mitigate the risk of arbitrary namespace imports. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@luolanzone] [@antoninbas])

### Fixed

- Start member StaleResCleanupController after cache is synced for Antrea Multi-cluster. ([#8180](https://github.com/antrea-io/antrea/pull/8180), [@Archong-Liu])
- Fix bridge-scoped OVS ofport lookup after the libovsdb migration. ([#8169](https://github.com/antrea-io/antrea/pull/8169), [@luolanzone])
- Fix FlowAggregator IPFIX export missing proxySnat fields. ([#8227](https://github.com/antrea-io/antrea/pull/8227), [@Dyanngg])
- Fix scrambled Aggregate-mode stats/throughput IEs in FlowAggregator IPFIX export. ([#8228](https://github.com/antrea-io/antrea/pull/8228), [@Dyanngg])
- Recover Service group install after a timed-out bundle commit. ([#8190](https://github.com/antrea-io/antrea/pull/8190), [@hongliangl])
- Return the error when adding messages to a bundle fails, so failed OVS flow installations are retried. ([#8211](https://github.com/antrea-io/antrea/pull/8211), [@hongliangl])
- Fix nil transaction panic in ClickHouse batchCommitAll when BeginTx fails. ([#8214](https://github.com/antrea-io/antrea/pull/8214), [@SAY-5])
- Close files explicitly in the extraction loop to prevent file descriptor exhaustion in compress utility. ([#8197](https://github.com/antrea-io/antrea/pull/8197), [@magic-peach])
- Fix host-local IPAM GC releasing in-use Pod IPs. ([#8240](https://github.com/antrea-io/antrea/pull/8240), [@antoninbas])
- Stop forwarding antctl caller credentials to Agents, authenticating with a short-lived token minted for the dedicated antctl ServiceAccount instead. ([#8251](https://github.com/antrea-io/antrea/pull/8251)), [@antoninbas])
- Check requester authorization for SupportBundleCollection authSecret, rejecting requests where the requester cannot read the referenced Secret. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Do not realize Groups whose childGroups are nested too deeply, preventing an antrea-controller crash caused by a ChildGroups cycle. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Check requester authorization for PacketCapture fileServer, rejecting requests where the requester cannot read the file server credentials. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Prevent antrea-agent panic on short reject packets. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Prevent antrea-agent panic on non-echo ICMPv6 packet-in. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Sanitize comment and log-prefix args in iptables rule builder to prevent injection of arbitrary rules. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Bind Windows ovsdb-server to loopback instead of all interfaces. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Prevent antrea-agent panic on short IGMP packets. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Fix antrea-agent panic on Traceflow to a portless Service. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])
- Bound FQDN tracking growth in fqdnController with a single capped cache. ([#8251](https://github.com/antrea-io/antrea/pull/8251), [@antoninbas])

[@Archong-Liu]: https://github.com/Archong-Liu
[@Dyanngg]: https://github.com/Dyanngg
[@SAY-5]: https://github.com/SAY-5
[@antoninbas]: https://github.com/antoninbas
[@hangyan]: https://github.com/hangyan
[@hongliangl]: https://github.com/hongliangl
[@jianjuns]: https://github.com/jianjuns
[@luolanzone]: https://github.com/luolanzone
[@magic-peach]: https://github.com/magic-peach
[@wenqiq]: https://github.com/wenqiq
