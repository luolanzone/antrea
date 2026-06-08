# Changelog 2.6

## 2.6.2 - 2026-06-07

### Added

- Add `FlowStreamService` gRPC streaming service to FlowAggregator, enabling clients to consume historical flow records from the ring buffer and follow new flow records with filtering support. ([#7937](https://github.com/antrea-io/antrea/pull/7937), [@Dyanngg])
- Add `ip rule` and `ip route show table all` output to Agent supportbundle collection to improve troubleshooting for Egress, SNAT, `EgressSeparateSubnet`, and custom routing tables. ([#8015](https://github.com/antrea-io/antrea/pull/8015), [@mail2sudheerobbu-oss])
- Add PacketCapture BPF equivalence tests against `tcpdump -ddd` output for IPv4, IPv6, transport protocols, ports, TCP flags, ICMP, ICMPv6, numeric protocols, and capture directions. ([#7865](https://github.com/antrea-io/antrea/pull/7865), [@SharanRP])
- Add IPv6-only, dual-stack, Kubernetes conformance, and Multi-cluster e2e coverage to GitHub Actions workflows. ([#8047](https://github.com/antrea-io/antrea/pull/8047) [#8064](https://github.com/antrea-io/antrea/pull/8064) [#8065](https://github.com/antrea-io/antrea/pull/8065), [@XinShuYang] [@luolanzone])

### Changed

- Tighten `AntreaNodeConfig` CRD validation rules for bridge names, physical interface names, and the maximum number of physical interfaces. ([#8039](https://github.com/antrea-io/antrea/pull/8039), [@luolanzone])
- Update Kubernetes dependencies to v1.36.1, update `controller-runtime` to v0.24.1, upgrade code generation tools, and regenerate CRDs, OpenAPI, protobuf, client, and informer code. ([#8057](https://github.com/antrea-io/antrea/pull/8057), [@antoninbas])
- Update `sigs.k8s.io/mcs-api` to v0.5.0 and adapt Multi-cluster ServiceExport condition handling to use `metav1.Condition`. ([#8054](https://github.com/antrea-io/antrea/pull/8054), [@luolanzone])
- Remove or replace several unmaintained dependencies, including `blang/semver`, `google/uuid`, selected `mdlayher` modules, and `lumberjack`. ([#8031](https://github.com/antrea-io/antrea/pull/8031) [#8032](https://github.com/antrea-io/antrea/pull/8032) [#8046](https://github.com/antrea-io/antrea/pull/8046) [#8053](https://github.com/antrea-io/antrea/pull/8053) [#8055](https://github.com/antrea-io/antrea/pull/8055), [@luolanzone] [@hangyan])
- Update dependencies including AWS SDK Go v2, `google.golang.org/grpc`, `golang.org/x`, Prometheus modules, `github.com/gopacket/gopacket`, and `github.com/Microsoft/hcsshim`. ([#7989](https://github.com/antrea-io/antrea/pull/7989) [#8035](https://github.com/antrea-io/antrea/pull/8035) [#8036](https://github.com/antrea-io/antrea/pull/8036) [#8060](https://github.com/antrea-io/antrea/pull/8060) [#8062](https://github.com/antrea-io/antrea/pull/8062) [#8063](https://github.com/antrea-io/antrea/pull/8063) [#8073](https://github.com/antrea-io/antrea/pull/8073) [#8081](https://github.com/antrea-io/antrea/pull/8081) [#8085](https://github.com/antrea-io/antrea/pull/8085) [#8093](https://github.com/antrea-io/antrea/pull/8093), [@luolanzone] [@renovatebot])
- Update Trivy Actions and Renovate configuration for AWS package updates. ([#8072](https://github.com/antrea-io/antrea/pull/8072) [#8084](https://github.com/antrea-io/antrea/pull/8084) [#8088](https://github.com/antrea-io/antrea/pull/8088), [@luolanzone] [@renovatebot])
- Clarify why container access locking is not required when removing stale interfaces during CNI server initialization. ([#7719](https://github.com/antrea-io/antrea/pull/7719), [@goyalpalak18])
- Remove redundant loop-variable shadowing in test files. ([#8033](https://github.com/antrea-io/antrea/pull/8033), [@wenqiq])

### Fixed

- Strengthen IPPool status update retries to reduce allocation and release failures when multiple Nodes update the same IPPool concurrently. ([#7996](https://github.com/antrea-io/antrea/pull/7996), [@wenqiq])
- Exclude terminated Pods from the FlowAggregator Pod store indexer to avoid associating flow records with the wrong Pod when Pod IPs are reused. ([#8043](https://github.com/antrea-io/antrea/pull/8043), [@Dyanngg])
- Fix flaky timing-sensitive unit tests by using `testing/synctest`. ([#8050](https://github.com/antrea-io/antrea/pull/8050), [@antoninbas])
- Fix `make test-unit` on Windows CI runners. ([#8049](https://github.com/antrea-io/antrea/pull/8049), [@antoninbas])

