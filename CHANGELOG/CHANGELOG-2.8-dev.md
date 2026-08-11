# Changelog 2.8 (2026-09-30 to 2026-10-26)

## 2.8.0 - dev

### Added

- Add readiness and liveness probes for FlowStreamService on the existing Flow Aggregator apiserver. ([#8292](https://github.com/antrea-io/antrea/pull/8292), [@salv-orlando])

### Changed

- Disclose peer endpoints in full in FlowStreamService when the client holds authorization on flows in the peer's Namespace, with cluster-wide SAR caching for clients with ClusterRole bindings. ([#8471](https://github.com/antrea-io/antrea/pull/8471), [@Dyanngg])
- Optimize FlowStreamService authorization checks by caching the cluster-wide floor disclosure tier for peer Namespaces. ([#8473](https://github.com/antrea-io/antrea/pull/8473), [@Dyanngg])
- Disable FlowStreamService by default when Flow Aggregator is running in Proxy mode, and reject enabling it at startup. ([#8502](https://github.com/antrea-io/antrea/pull/8502), [@Dyanngg])
- Refine flow disclosure rules in FlowStreamService based on connection direction and Namespace boundary to ensure appropriate visibility into denied connections. ([#8491](https://github.com/antrea-io/antrea/pull/8491), [@Dyanngg])
- Reject unsupported authentication types other than BasicAuthentication in the SupportBundleCollection validating webhook at admission time. ([#8492](https://github.com/antrea-io/antrea/pull/8492), [@wenyingd])
- Support wildcard matching paths (/mutate/*, /validate/*, /convert/*) for webhook authorization in the Antrea API server. ([#8498](https://github.com/antrea-io/antrea/pull/8498), [@luolanzone])
- Drop legacy support for installing and running Antrea Agent as a Windows service in favor of containerized Pod deployments on Windows Nodes. ([#8455](https://github.com/antrea-io/antrea/pull/8455), [@luolanzone])
- Migrate from third-party UUID packages (gofrs/uuid and google/uuid) to the Go 1.27 standard library uuid package. ([#8476](https://github.com/antrea-io/antrea/pull/8476), [@antoninbas])

### Fixed

- Fix antrea-agent crash loop caused by a nil pointer dereference when a SupportBundleCollection is configured with missing or non-basic authentication. ([#8429](https://github.com/antrea-io/antrea/pull/8429), [@wenyingd])
- Fix 100% CPU utilization hot loop in NDP and ARP responders by removing non-blocking select defaults on socket read errors. ([#8315](https://github.com/antrea-io/antrea/pull/8315), [@hangyan])
- Fix panics in the label identity group index when encountering malformed label strings, and add CEL validation for LabelIdentitySpec.Label. ([#8421](https://github.com/antrea-io/antrea/pull/8421), [@Dyanngg])
- Dynamically rotate the FlowStreamService TLS certificate without restarting the gRPC server or disrupting existing active streams. ([#8509](https://github.com/antrea-io/antrea/pull/8509), [@Dyanngg])
- Fix missing hairpin SNAT flows for static Endpoints without nodeName whose IPs belong to the local PodCIDR. ([#8517](https://github.com/antrea-io/antrea/pull/8517), [@hongliangl])
- Add OpenAPI schema and admission validation for ExternalIPPool node selectors to prevent Agent crashes and hash ring build failures on malformed selectors. ([#8518](https://github.com/antrea-io/antrea/pull/8518), [@luolanzone])

[@Dyanngg]: https://github.com/Dyanngg
[@antoninbas]: https://github.com/antoninbas
[@hangyan]: https://github.com/hangyan
[@hongliangl]: https://github.com/hongliangl
[@luolanzone]: https://github.com/luolanzone
[@salv-orlando]: https://github.com/salv-orlando
[@wenyingd]: https://github.com/wenyingd
