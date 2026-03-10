# New Since Last Community Meeting

### Added

- Add NetworkInfo field to AntreaAgentInfo CRD. ([#7763](https://github.com/antrea-io/antrea/pull/7763), [@antoninbas])
- Add WireGuard validation to antctl check installation. ([#7809](https://github.com/antrea-io/antrea/pull/7809), [@antoninbas])
- Add IPsec validation to antctl check installation. ([#7757](https://github.com/antrea-io/antrea/pull/7757), [@antoninbas])

### Changed

- Upgrade Go to 1.26. ([#7795](https://github.com/antrea-io/antrea/pull/7795), [@antoninbas])
- Support IPv6 traffic over IPv4 IPsec tunnel. ([#7759](https://github.com/antrea-io/antrea/pull/7759), [@xliuxu])
- Rename parameter in InstallNodeFlows (OF Client). ([#7806](https://github.com/antrea-io/antrea/pull/7806), [@antoninbas])

### Fixed

- Fix default options for audit logging configuration. ([#7825](https://github.com/antrea-io/antrea/pull/7825), [@Denyme24])
- Fix host rules using fixed tunnel port even the port is configured. ([#7824](https://github.com/antrea-io/antrea/pull/7824), [@hongliangl])
- Fix concurrent map access in GetFQDNCache. ([#7794](https://github.com/antrea-io/antrea/pull/7794), [@Ady0333])
- Fix Traceflow with WireGuard enabled. ([#7634](https://github.com/antrea-io/antrea/pull/7634), [@xliuxu])
- Fix Egress with WireGuard encryption enabled. ([#7628](https://github.com/antrea-io/antrea/pull/7628), [@xliuxu])
- Fix race in ReassignFlowPriorities by adding missing locks. ([#7717](https://github.com/antrea-io/antrea/pull/7717), [@Ady0333])

### CI/Tests Improvements

- Fix cleanup logic in secondary network Kind script. ([#7802](https://github.com/antrea-io/antrea/pull/7802), [@wenqiq])
- Revisit probe implementation for e2e tests. ([#7788](https://github.com/antrea-io/antrea/pull/7788), [@antoninbas])
- Disable containerd image store for Kind CI jobs. ([#7792](https://github.com/antrea-io/antrea/pull/7792), [@antoninbas])
- Replace 'docker manifest create' in Github workflows. ([#7791](https://github.com/antrea-io/antrea/pull/7791), [@antoninbas])
- Use latest available Go patch release in Github workflows. ([#7790](https://github.com/antrea-io/antrea/pull/7790), [@antoninbas])
- Fix docker IP retrieval in kind CI script. ([#7787](https://github.com/antrea-io/antrea/pull/7787), [@antoninbas])
- Handle truncated probe logs in flaky tests. ([#7689](https://github.com/antrea-io/antrea/pull/7689), [@SharanRP])

### Dependencies Upgrade

- fix(deps): update golang.org/x (main). ([#7768](https://github.com/antrea-io/antrea/pull/7768), [@app/renovate])
- chore(deps): update aquasecurity/trivy-action action to v0.34.0 (main). ([#7779](https://github.com/antrea-io/antrea/pull/7779), [@app/renovate])

[@Ady0333]: https://github.com/Ady0333
[@Denyme24]: https://github.com/Denyme24
[@SharanRP]: https://github.com/SharanRP
[@antoninbas]: https://github.com/antoninbas
[@app/renovate]: https://github.com/apps/renovate
[@hongliangl]: https://github.com/hongliangl
[@wenqiq]: https://github.com/wenqiq
[@xliuxu]: https://github.com/xliuxu
