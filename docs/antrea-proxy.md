# Antrea Proxy

## Table of Contents

<!-- toc -->
- [Introduction](#introduction)
- [Antrea Proxy with proxyAll](#antrea-proxy-with-proxyall)
  - [Removing kube-proxy](#removing-kube-proxy)
    - [Windows Nodes](#windows-nodes)
  - [Configuring load balancer mode for external traffic](#configuring-load-balancer-mode-for-external-traffic)
- [Special use cases](#special-use-cases)
  - [When you are using NodeLocal DNSCache](#when-you-are-using-nodelocal-dnscache)
  - [When you want your external LoadBalancer to handle Pod traffic](#when-you-want-your-external-loadbalancer-to-handle-pod-traffic)
- [Known issues or limitations](#known-issues-or-limitations)
<!-- /toc -->

## Introduction

Antrea Proxy was first introduced in Antrea v0.8 and has been enabled by default
on all platforms since v0.11. Antrea Proxy enables some or all of the cluster's
Service traffic to be load-balanced as part of the OVS pipeline, instead of
depending on kube-proxy. We typically observe latency improvements for Service
traffic when Antrea Proxy is used.

While Antrea Proxy can be disabled on Linux Nodes by setting the `AntreaProxy`
Feature Gate to `false`, it should remain enabled on all Windows Nodes, as it is
needed for correct NetworkPolicy implementation for Pod-to-Service traffic.

By default, Antrea Proxy will only handle Service traffic originating from Pods
in the cluster, with no support for NodePort. However, starting with Antrea
v1.4, a new operating mode was introduced in which Antrea Proxy can handle all
Service traffic, including NodePort. See the following
[section](#antrea-proxy-with-proxyall) for more information.

## Antrea Proxy with proxyAll

The `proxyAll` configuration parameter can be enabled in the Antrea
configuration if you want Antrea Proxy to handle all Service traffic, with the
possibility to remove kube-proxy altogether and have one less DaemonSet running
in the cluster. This is particularly interesting on Windows Nodes, since until
the introduction of `proxyAll`, Antrea relied on userspace kube-proxy, which is
no longer actively maintained by the K8s community and is slower than other
kube-proxy backends.

Note that on Linux, before Antrea v2.1, when `proxyAll` is enabled, kube-proxy
will usually take priority over Antrea Proxy and will keep handling all kinds of
Service traffic (unless the source is a Pod, which is pretty unusual as Pods
typically access Services by ClusterIP). This is because kube-proxy rules typically
come before the rules installed by Antrea Proxy to redirect traffic to OVS. When
kube-proxy is not deployed or is removed from the cluster, Antrea Proxy will then
handle all Service traffic.

Starting with Antrea v2.1, when `proxyAll` is enabled, Antrea Proxy will handle
Service traffic destined to NodePort, LoadBalancerIP and ExternalIP, even if
kube-proxy is present. This benefits users who want to take advantage of
Antrea Proxy's advanced features, such as Direct Server Return (DSR) mode, but
lack control over kube-proxy's installation. This is accomplished by
prioritizing the rules installed by Antrea Proxy over those installed by
kube-proxy, thus it works only with kube-proxy iptables mode. Support for other
kube-proxy modes may be added in the future.

Note that running both kube-proxy and Antrea Proxy with `proxyAll` can trigger
some error logs for Services of type LoadBalancer with `externalTrafficPolicy`
set to `Local`. For such Services, the proxy is in charge of running a health
check server on each Node, in order to report the number of local Endpoints
which implement the Service. The server port is determined by the value of
[`.spec.healthCheckNodePort`](https://kubernetes.io/docs/tasks/access-application-cluster/create-external-load-balancer/#preserving-the-client-source-ip).
Because both kube-proxy and Antrea Proxy (with `proxyAll`) will try to run the
health check servers on the same ports, one of them will fail to bind to the
desired address. In practice, we typically observe that Antrea Proxy tries to
bind to the address first (and succeeds), while kube-proxy fails and logs the
following error message:

```text
E0117 19:38:17.586328       1 service_health.go:145] "Failed to start healthcheck" err="listen tcp 0.0.0.0:31653: bind: address already in use" node="kind-worker" service="default/nginx" port=31653
```

These log messages will keep repeating periodically, as kube-proxy handles
Service updates. While the messages are harmless, they can create a lot of
unnecessary noise. You may want to set `antreaProxy.disableServiceHealthCheckServer: true`
in the `antrea-config` ConfigMap to avoid such logs. It will instruct Antrea Proxy
to stop running health check servers and shift this responsibility to
kube-proxy. This is not a perfect solution as ideally the component responsible
for the proxy implementation (Antrea Proxy) should also be responsible for
providing health check information. We still recommend removing kube-proxy
whenever possible.

### Removing kube-proxy

In this section, we will provide steps to run a K8s cluster without kube-proxy,
with Antrea being responsible for all Service traffic.

You can create a K8s cluster without kube-proxy with kubeadm as follows:

```bash
kubeadm init --skip-phases=addon/kube-proxy
```

To remove kube-proxy from an existing cluster, you can use the following steps:

```bash
# Delete the kube-proxy DaemonSet
kubectl -n kube-system delete ds/kube-proxy
# Delete the kube-proxy ConfigMap to prevent kube-proxy from being re-deployed
# by kubeadm during "upgrade apply". This workaround will not take effect for
# kubeadm versions older than v1.19 as the following patch is required:
# https://github.com/kubernetes/kubernetes/pull/89593
kubectl -n kube-system delete cm/kube-proxy
# Delete existing kube-proxy rules; there are several options for doing that
# Option 1 (if using kube-proxy in iptables mode), run the following on each Node:
iptables-save | grep -v KUBE | iptables-restore
# Option 2 (any mode), restart all Nodes
# Option 3 (any mode), run the following on each Node:
kube-proxy --cleanup
# You can create a DaemonSet to easily run the above command on all Nodes, using
# the kube-proxy container image
```

You will then need to deploy [Antrea](getting-started.md), after making the
necessary changes to the `antrea-config` ConfigMap:

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: antrea-config
  namespace: kube-system
data:
  antrea-agent.conf: |
    kubeAPIServerOverride: "<kube-apiserver URL>"
    antreaProxy:
      proxyAll: true
```

The `kubeAPIServerOverride` option will enable the Antrea Agent to connect to
the K8s apiserver. This is required now that kube-proxy is no longer running and
that the Antrea Agent can no longer use the ClusterIP for the `kubernetes`
Service during initialization. If you are unsure about which values to use, take
a look at your Kubeconfig file, and look for a line like this one:

```yaml
apiVersion: v1
clusters:
- cluster:
    server: https://192.168.77.100:6443
```

Then use this value as is (e.g., `"https://192.168.77.100:6443"`) for
`kubeAPIServerOverride`.

And that's it! All you have to do now is make sure that the `antrea-agent` Pods
came up correctly and perhaps validate that NodePort Services can be accessed
correctly.

#### Windows Nodes

Assuming you are following the steps we [documented](windows.md) to add Windows
Nodes to your K8s cluster with Antrea, you will simply need to skip running
kube-proxy:

* Do not install or start the `kube-proxy` service [when using containerd as
  the container runtime](windows.md#installation-as-a-service-containerd-based-runtimes)
* Do not create the `kube-proxy-windows` DaemonSet [when using Docker as the
  container runtime](windows.md#installation-via-wins-docker-based-runtimes)

### Configuring load balancer mode for external traffic

Starting with Antrea v1.13, the `defaultLoadBalancerMode` configuration
parameter and the `service.antrea.io/load-balancer-mode` Service annotation
can be used to specify how you want Antrea Proxy to handle external traffic
destined to LoadBalancerIPs and ExternalIPs of Services. Specifically, the mode
determines how external traffic is processed when it's load balanced across
Nodes. Currently, it has two options: `nat` (default) and `dsr`.

* In NAT mode, external traffic is SNAT'd when it's load balanced across Nodes
to ensure symmetric paths. It's the default and the most general mode.

* In DSR mode, external traffic is never SNAT'd and backend Pods running on
Nodes that are not the ingress Node can reply to clients directly, bypassing
the ingress Node. Therefore, DSR mode can preserve the client IP of requests,
and usually has lower latency and higher throughput. Currently, it is only
applicable to Linux Nodes, encap mode, and IPv4 clusters. The feature gate
`LoadBalancerModeDSR` must be enabled to use this mode for any Service.

You can make the following changes to the `antrea-config` ConfigMap to specify
the default load balancer mode for all Services:

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: antrea-config
  namespace: kube-system
data:
  antrea-agent.conf: |
    kubeAPIServerOverride: "<kube-apiserver URL>"
    antreaProxy:
      proxyAll: true
      defaultLoadBalancerMode: <nat|dsr>
```

To configure a different load balancer mode for a particular Service, you can
annotate the Service in the following way:

```bash
kubectl annotate service my-service service.antrea.io/load-balancer-mode=<nat|dsr>
```

**Note**: Configuring the load balancer mode is only meaningful when `proxyAll`
is enabled and kube-proxy is not deployed, otherwise external traffic would be
processed by kube-proxy rules before it reaches Antrea's datapath. If
kube-proxy was ever deployed in the cluster, its rules must be deleted to avoid
interference. In particular, the following filter rule that drops packets in
INVALID conntrack state could prevent DSR mode from working:

```text
*filter
-A KUBE-FORWARD -m conntrack --ctstate INVALID -j DROP
```

## Special use cases

### When you are using NodeLocal DNSCache

[NodeLocal DNSCache](https://kubernetes.io/docs/tasks/administer-cluster/nodelocaldns/)
improves performance of DNS queries in a K8s cluster by running a DNS cache on
each Node. DNS queries are intercepted by a local instance of CoreDNS, which
forwards the requests to CoreDNS (cluster local queries) or the upstream DNS
server in case of a cache miss.

The way it works (normally) is by assigning the the kube-dns ClusterIP to a
local "dummy" interface, and installing iptables rules to disable connection
tracking for the queries and bypass kube-proxy. The local CoreDNS instance is
configured to bind to that address and can therefore intercept queries. In case
of a cache miss, queries can be sent to the cluster CoreDNS Pods thanks to a
"shadow" Service which will expose CoreDNS Pods via a new ClusterIP.

When Antrea Proxy is enabled (default), Pod DNS queries to the kube-dns ClusterIP
will be load-balanced directly by Antrea Proxy to a CoreDNS Pod endpoint. This
means that NodeLocal DNSCache is completely bypassed, which is probably not
acceptable for users who want to leverage this feature to improve DNS
performance in their clusters. While these users can update the Pod
configuration to use the local IP assigned by NodeLocal DNSCache to the "dummy"
interface, this is not always ideal in the context of CaaS, as it can require
everyone running Pods in the cluster to be aware of the situation.

This is the reason why we initially introduced the `skipServices` configuration
option for Antrea Proxy in Antrea v1.4. By adding the kube-dns Service (which
exposes CoreDNS) to the list, you can ensure that Antrea Proxy will "ignore" Pod
DNS queries, and that they will be forwarded to NodeLocal DNSCache. You can edit
the `antrea-config` ConfigMap as follows:

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: antrea-config
  namespace: kube-system
data:
  antrea-agent.conf: |
    antreaProxy:
      skipServices: ["kube-system/kube-dns"]
```

### When you want your external LoadBalancer to handle Pod traffic

In some cases, the external LoadBalancer for a cluster provides additional
capabilities (e.g., TLS termination) and it is desirable for Pods to access
in-cluster Services through the external LoadBalancer. By default, this is not
the case as both kube-proxy and Antrea Proxy will install rules to load-balance
this traffic directly at the source Node (even when the destination IP is set to
the external `loadBalancerIP`). To circumvent this behavior, we introduced the
`proxyLoadBalancerIPs` configuration option for Antrea Proxy in Antrea v1.5. This
option defaults to `true`, but when setting it to `false`, Antrea Proxy will no
longer load-balance traffic destined to external `loadBalancerIP`s, hence
ensuring that this traffic can go to the external LoadBalancer. You can set it
to `false` by editing the `antrea-config` ConfigMap as follows:

```yaml
kind: ConfigMap
apiVersion: v1
metadata:
  name: antrea-config
  namespace: kube-system
data:
  antrea-agent.conf: |
    antreaProxy:
      proxyLoadBalancerIPs: false
```

With the above configuration, Antrea Proxy will ignore all external `loadBalancerIP`s.
Starting with K8s v1.29, feature [LoadBalancerIPMode](https://kubernetes.io/docs/concepts/services-networking/service/#load-balancer-ip-mode)
was introduced, providing users with a more fine-grained mechanism to control how
every external `loadBalancerIP` behaves in a LoadBalancer Service.

- If the value of `LoadBalancerIPMode` is `LoadBalancerIPModeVIP` or nil, the
  traffic destined for the corresponding external `loadBalancerIP` should follow
  the default behavior and get load-balanced at the source Node.
- If the value of `LoadBalancerIPMode` is `LoadBalancerIPModeProxy`, the traffic
  destined for the corresponding external `loadBalancerIP` should be sent to the
  external LoadBalancer.

Starting with Antrea v2.0, Antrea Proxy will respect `LoadBalancerIPMode` in LoadBalancer
Services when the configuration option `proxyLoadBalancerIPs` is set to `true`
(default). In this case, Antrea Proxy will serve only the external `loadBalancerIP`s
configured with `LoadBalancerIPModeVIP`, and those configured with
`LoadBalancerIPModeProxy` will bypass Antrea Proxy. If the configuration option
`proxyLoadBalancerIPs` is set to `false`, Antrea Proxy will ignore the external
`loadBalancerIP`s even if configured with `LoadBalancerIPModeVIP`.

There are two important prerequisites for this feature:

* You must enable `proxyAll` and [remove kube-proxy](#removing-kube-proxy) from
  the cluster, otherwise kube-proxy will still load-balance the traffic and you
  will not achieve the desired behavior.
* Your external LoadBalancer must SNAT the traffic, in order for the reply
  traffic to go back through the external LoadBalancer.

## Known issues or limitations

* Due to some restrictions on the implementation of Services in Antrea, the
  maximum number of Endpoints that Antrea can support at the moment is 800. If
  the number of Endpoints for a given Service exceeds 800, extra Endpoints will
  be dropped (with non-local Endpoints being dropped in priority by each Antrea
  Agent). This will be fixed eventually.
* Due to some restrictions on the implementation of Services in Antrea, the
  maximum timeout value supported for ClientIP-based Service SessionAffinity is
  65535 seconds (the K8s Service specs allow values up to 86400 seconds). Values
  greater than 65535 seconds will be truncated and the Antrea Agent will log a
  warning. [We do not intend to address this
  limitation](https://github.com/antrea-io/antrea/issues/1578).
* Due to the use of the "learn" action in the implementation of DSR mode, the
  cost of processing the first packet of each connection is higher than NAT
  mode. Therefore, establishing connections may be slightly slower, and you may
  observe lower transaction rate if short-lived connections dominate your
  traffic. This may be improved in the future.
