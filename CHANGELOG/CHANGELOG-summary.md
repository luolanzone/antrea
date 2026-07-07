# Antrea Main Branch Changelog Summary (2026-06-09 to 2026-07-07)

### Added

- Support ClusterNetworkPolicy in network-policy-api v0.2.0. ([#8018](https://github.com/antrea-io/antrea/pull/8018), [@Dyanngg] [@cursoragent])
- Support external-to-Pod flows in flow visibility. ([ef4fb65](https://github.com/antrea-io/antrea/commit/ef4fb65), [@Dyanngg] [@petertran-avgo])
- Add AntreaNodeConfig validation webhook to validate secondary OVS bridge VLAN configuration. ([#8149](https://github.com/antrea-io/antrea/pull/8149), [@luolanzone])

### Changed

- Upgrade Linux OVS to version 3.7.1. ([#8082](https://github.com/antrea-io/antrea/pull/8082), [@luolanzone])
- Document OFSwitch pointer lifecycle. ([#8162](https://github.com/antrea-io/antrea/pull/8162), [@hongliangl])
- Multi-cluster controller leader cache changes. ([#8133](https://github.com/antrea-io/antrea/pull/8133), [@aclfe])
- Annotate end-of-initial-events bookmark for watch-list clients. ([#8124](https://github.com/antrea-io/antrea/pull/8124), [@stroebs])
- Log invalid Namespace enable-logging annotation values. ([#8135](https://github.com/antrea-io/antrea/pull/8135), [@Anand-240])
- Migrate from OVSDB-golang-lib to libovsdb. ([#8092](https://github.com/antrea-io/antrea/pull/8092), [@hongliangl] [@cursoragent])
- Update CHANGELOG for v2.6.2 release. ([#8126](https://github.com/antrea-io/antrea/pull/8126), [@antrea-bot] [@luolanzone])

### Fixed

- Fix socket leak in NPL AddRule when iptables rule installation fails in agent. ([#8110](https://github.com/antrea-io/antrea/pull/8110), [@aneek22112007-tech])
- Fix netnat log format and newlines. ([#8121](https://github.com/antrea-io/antrea/pull/8121), [@hongliangl] [@cursoragent])
- Fix WireGuard Traceflow tunnel destination. ([#8090](https://github.com/antrea-io/antrea/pull/8090), [@xliuxu])
- Exclude host-network and terminated Pods from NetworkPolicyEvaluation. ([#8042](https://github.com/antrea-io/antrea/pull/8042), [@Dyanngg])
- Fix flaky secondary network test. ([#8105](https://github.com/antrea-io/antrea/pull/8105), [@luolanzone])
- Fix e2e test DeleteACNP to ignore NotFound errors. ([#8104](https://github.com/antrea-io/antrea/pull/8104), [@hongliangl] [@cursoragent])


---

[@Anand-240]: https://github.com/Anand-240
[@Dyanngg]: https://github.com/Dyanngg
[@aclfe]: https://github.com/aclfe
[@aneek22112007-tech]: https://github.com/aneek22112007-tech
[@antrea-bot]: https://github.com/antrea-bot
[@cursoragent]: https://github.com/cursoragent
[@hongliangl]: https://github.com/hongliangl
[@luolanzone]: https://github.com/luolanzone
[@petertran-avgo]: https://github.com/petertran-avgo
[@stroebs]: https://github.com/stroebs
[@xliuxu]: https://github.com/xliuxu