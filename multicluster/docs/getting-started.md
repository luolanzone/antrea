# Antrea Mulit-Cluster Controller Installation

## Prepare Docker Image

For Antrea multi-cluster, there will be only one image `antrea/antrea-multicluster-controller:latest` 
for all controllers, you need to prepare a docker image before setup MCS component,
you can follow below steps to get the image ready on your local cluster.

1. Go to `antrea/multi-cluster` folder, run `make docker-build`, you will get a new image
  named `antrea/antrea-multicluster-controller:latest` locally.
2. Run `docker save antrea/antrea-multicluster-controller:latest > antrea-mcs.tar` to save the image.
3. Copy the image file `antrea-mcs.tar` to the nodes of your local cluster
4. Run `docker load < antrea-mcs.tar` in each node of your local cluster.

## Install Mulit-Cluster Controller

### Installation in Leader Cluster

Run below command to apply global CRDs in leader cluster:

```
kubectl apply -f build/yamls/antrea-multicluster-leader-global.yml
```

Install MCS controller in leader cluster, since MCS controller is running as namespaced
deployment, you should create a namespace first, then apply the manifest with new namespace.
below are sample commands.

```
kubectl create ns antrea-mcs-ns
hack/generate-manifest.sh -l antrea-mcs-ns | kubectl apply -f -
```

You will see a `ServiceAccount` named `antrea-multicluster-member-access-sa` in lead cluster after
apply the MCS controller manifest, then create a secret associated with this `ServiceAccount` so
the secret can be exported and used in member cluster later.

```yml
apiVersion: v1
kind: Secret
metadata:
  name: leader-access-token
  namespace: antrea-mcs-ns
  annotations:
    kubernetes.io/service-account.name: antrea-multicluster-member-access-sa
type: kubernetes.io/service-account-token
```

Get secret data via `kubectl get secret leader-access-token -n antrea-mcs-ns -o yaml > leader-access-token.yml`
### Installation in Member Cluster

You can simply run below command to install MCS controller to member cluster.
```
kubectl apply -f build/yamls/antrea-multicluster-member-only.yml
```

Copy file `leader-access-token.yml` from leader cluster, update the namespace to `default`
where MCS controller is deployed in member cluster and create it via `kubectl create -f leader-access-token.yml`

## Setup ClusterSet

In an Antrea multi-cluster cluster set, there will be at least one leader cluster and two
member clusters. At first, all clusters in the cluster set need to use `ClusterClaim` to
claim itself as a member of a cluster set. A leader cluster will define `ClusterSet` which
includes leader and member clusters. below is a sample to create a cluster set with a cluster
set id `test-clusterset` which has two member clusters with cluster id `test-cluster-east`
and `test-cluster-west`, one leader cluster with id `test-cluster-leader`.

* Create below `ClusterClaim` and `ClusterSet` in the member cluster `test-cluster-east`.

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterClaim
metadata:
  name: east-membercluster-id
  namespace: default
name: id.k8s.io
value: test-cluster-east
---
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterClaim
metadata:
  name: clusterset-id
  namespace: default
name: clusterSet.k8s.io
value: test-clusterset
---
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterSet
metadata:
    name: test-clusterset
    namespace: antrea-mcs-ns
spec:
    leaders:
      - clusterID: test-cluster-leader
        secret: "leader-access-token"
        server: "https://172.18.0.2:6443"
    members:
      - clusterID: test-cluster-east
    namespace: antrea-mcs-ns
```

* Create below `ClusterClaim` and `ClusterSet` in the member cluster `test-cluster-west`.

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterClaim
metadata:
  name: west-membercluster-id
  namespace: default
name: id.k8s.io
value: test-cluster-west
---
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterClaim
metadata:
  name: clusterset-id
  namespace: default
name: clusterSet.k8s.io
value: test-clusterset
---
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterSet
metadata:
    name: test-clusterset
    namespace: antrea-mcs-ns
spec:
    leaders:
      - clusterID: test-cluster-leader
        secret: "leader-access-token"
        server: "https://172.18.0.2:6443"
    members:
      - clusterID: test-cluster-west
    namespace: antrea-mcs-ns
```

* Create below `ClusterClaim` in the leader cluster `test-cluster-leader`.
 
```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterClaim
metadata:
  name: leadercluster-id
  namespace: antrea-mcs-ns
name: id.k8s.io
value: test-cluster-leader
---
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterClaim
metadata:
  name: clusterset-id
  namespace: antrea-mcs-ns
name: clusterSet.k8s.io
value: test-clusterset
```

* Create below `ClusterSet` in the leader cluster `test-cluster-leader`.

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ClusterSet
metadata:
    name: test-clusterset
    namespace: antrea-mcs-ns
spec:
    leaders:
      - clusterID: test-cluster-leader
    members:
      - clusterID: test-cluster-east
        serviceAccount: "member-east-account"
      - clusterID: test-cluster-west
        serviceAccount: "member-west-account"
    namespace: antrea-mcs-ns
```

## Use MCS Custom Resource

After you set up a clusterset properly, you can simply add a `ServiceExport` resource
as below to export a `Service` from one member cluster to other members in the 
clusterset, you can update the name and namespace according to your local K8s Service.

```yaml
apiVersion: multicluster.x-k8s.io/v1alpha1
kind: ServiceExport
metadata:
  name: nginx
  namespace: kube-system
```

For example, once you export the `kube-system/nginx` Service in member cluster `test-cluster-west`,
Antrea multi-cluster controller in member cluster will create two corresponding `ResourceExport` 
in the leader cluster, and the controller in leader cluster will do some computations and create
two `ResourceImport` contains all exported Service and Endpoints' information. you can check 
resources as below in leader cluster:

```sh
$kubectl get resourceexport
NAME                                        AGE
test-cluster-west-default-nginx-endpoints   7s
test-cluster-west-default-nginx-service     7s

$kubectl get resourceimport
NAME                      AGE
default-nginx-endpoints   99s
default-nginx-service     99s
```

then you can go to member cluster `test-cluster-east` to check new created 
`kube-system/nginx` Service and Endpoints by multi-cluster controller.

## Manual Test

You can also create some MCS resource manually if you need to do some testing
again `ResourceExport`, `ResourceImport` etc. below lists a few sample yamls 
for you to create some MCS custom resources.

* A `ServiceExport` example which will expose a Service named `nginx` in namespace
  `kube-system` in a member cluster, let's create it in both `test-cluster-west` 
  and `test-cluster-east` clusters.

```yaml
apiVersion: multicluster.x-k8s.io/v1alpha1
kind: ServiceExport
metadata:
  name: nginx
  namespace: kube-system
```

* Create two `ResourceExports` examples which wrap a `ServiceExport` named `nginx`
  to Service type of `ResourceExport` and Endpoint type of `ResourceExport` to 
  represent the exposed `nginx` service for both `test-cluster-west` and `test-cluster-east`
  in the leader cluster `test-cluster-leader`.

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ResourceExport
metadata:
  name: test-cluster-west-nginx-kube-system-service
  namespace: antrea-mcs-ns
spec:
  clusterID: test-cluster-west
  name: nginx
  namespace: kube-system
  kind: Service
  service:
    serviceSpec:
      ports: 
      - name: tcp80
        port: 80
        protocol: TCP
```

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ResourceExport
metadata:
  name: test-cluster-west-nginx-kube-system-endpoints
  namespace: antrea-mcs-ns
spec:
  clusterID: test-cluster-west
  name: nginx
  namespace: kube-system
  kind: Endpoints
  endpoints:
    subsets:
    - addresses:
      - ip: 192.168.225.49
        nodeName: node-1
      - ip: 192.168.225.51
        nodeName: node-2
      ports:
      - name: tcp8080
        port: 8080
        protocol: TCP
```

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ResourceExport
metadata:
  name: test-cluster-east-nginx-kube-system-service
  namespace: antrea-mcs-ns
spec:
  clusterID: test-cluster-east
  name: nginx
  namespace: kube-system
  kind: Service
  service:
    serviceSpec:
      ports: 
      - name: tcp80
        port: 80
        protocol: TCP
```

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ResourceExport
metadata:
  name: test-cluster-east-nginx-kube-system-endpoints
  namespace: antrea-mcs-ns
spec:
  clusterID: test-cluster-east
  name: nginx
  namespace: kube-system
  kind: Endpoints
  endpoints:
    subsets:
    - addresses:
      - ip: 192.168.224.21
        nodeName: node-one
      - ip: 192.168.226.11
        nodeName: node-two
      ports:
      - name: tcp8080
        port: 8080
        protocol: TCP
```

* Create two `ResourceImport` examples which represent the `nginx` service in 
  namespace `kube-system` in the leader cluster `test-cluster-leader`.

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ResourceImport
metadata:
  name: nginx-kube-system-service
  namespace: antrea-mcs-ns
spec:
  name: nginx
  namespace: kube-system
  kind: ServiceImport
  serviceImport:
    spec:
      ports: 
      - name: tcp80
        port: 80
        protocol: TCP
      type: ClusterSetIP
```

```yaml
apiVersion: multicluster.crd.antrea.io/v1alpha1
kind: ResourceImport
metadata:
  name: nginx-kube-system-endpoints
  namespace: antrea-mcs-ns
spec:
  name: nginx
  namespace: kube-system
  kind: EndPoints
  endpoints:
    subsets:
    - addresses:
      - ip: 192.168.225.49
        nodeName: node-1
      - ip: 192.168.225.51
        nodeName: node-2
      ports:
      - name: tcp8080
        port: 8080
        protocol: TCP
    - addresses:
      - ip: 192.168.224.21
        nodeName: node-one
      - ip: 192.168.226.11
        nodeName: node-two
      ports:
      - name: tcp8080
        port: 8080
        protocol: TCP
```