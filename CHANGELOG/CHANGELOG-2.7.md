# Changelog 2.7

## 2.7.0 - 2026-04-07

### Added

- Add `AntreaNodeConfig` CRD for node-level Antrea configuration. ([#7812](https://github.com/antrea-io/antrea/pull/7812), [@luolanzone])

### Changed

- Migrate YAML parsing from `gopkg.in/yaml.v3` to `go.yaml.in/yaml/v3`. ([#7956](https://github.com/antrea-io/antrea/pull/7956), [@SharanRP])
- Align Antrea NodeIPAM range allocator behavior with upstream Kubernetes. ([#7917](https://github.com/antrea-io/antrea/pull/7917), [@antoninbas])
- Update CNI plugins (`containernetworking/plugins`) to v1.9.1 to address CVEs. ([#7894](https://github.com/antrea-io/antrea/pull/7894), [@luolanzone])
- Pin Trivy GitHub Actions to immutable commit SHAs. ([#7905](https://github.com/antrea-io/antrea/pull/7905), [@luolanzone])

### Fixed

- Fix Antrea Controller panic in `NodeIPsIndexFunc` when a Node has no IP addresses. ([#7916](https://github.com/antrea-io/antrea/pull/7916), [@antoninbas])
- Fix Flow Aggregator panic when a connection record has a nil `EndTs`. ([#7929](https://github.com/antrea-io/antrea/pull/7929), [@OmAmbole009])
- Fix incorrect bitmap index in IP range `AllocateRange` that could allocate the same range twice. ([#7945](https://github.com/antrea-io/antrea/pull/7945), [@OmAmbole009])
- Fix Agent `Tracker` handling of Node delete events delivered as tombstones. ([#7949](https://github.com/antrea-io/antrea/pull/7949), [@OmAmbole009])
- Fix AntreaIPAM StatefulSet informer handling of delete events delivered as tombstones. ([#7958](https://github.com/antrea-io/antrea/pull/7958), [@OmAmbole009])
- Fix nil Pod IPs when `AntreaIPAMPodIP` annotation tokens are empty. ([#7930](https://github.com/antrea-io/antrea/pull/7930), [@wenqiq])
- Fix OpenAPI schema generation for Antrea APIs. ([#7901](https://github.com/antrea-io/antrea/pull/7901), [@antoninbas])
- Fix wrong error variable used when updating `SupportBundleCollection` status. ([#7923](https://github.com/antrea-io/antrea/pull/7923), [@Anujkumar9081])
- Implement `ExpirePriorityQueue.Clear` and correct `DeleteAllConnections` so connection cleanup and queue state stay consistent. ([#7935](https://github.com/antrea-io/antrea/pull/7935), [@Denyme24])
- Clamp negative per-session IPFIX delta counts to zero in FlowExporter instead of wrapping. ([#7883](https://github.com/antrea-io/antrea/pull/7883), [@Denyme24])
- Ignore user-managed VLAN sub-interfaces when the IP assigner enumerates host interfaces. ([#7898](https://github.com/antrea-io/antrea/pull/7898), [@antoninbas])

[@Anujkumar9081]: https://github.com/Anujkumar9081
[@Denyme24]: https://github.com/Denyme24
[@Meetjain1]: https://github.com/Meetjain1
[@OmAmbole009]: https://github.com/OmAmbole009
[@SharanRP]: https://github.com/SharanRP
[@antoninbas]: https://github.com/antoninbas
[@luolanzone]: https://github.com/luolanzone
[@renovatebot]: https://github.com/renovatebot
[@wenqiq]: https://github.com/wenqiq
