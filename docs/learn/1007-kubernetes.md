---
slug: /1007/kubernetes/
---

# Deploy to Kubernetes with Dagger

This tutorial illustrates how to use Dagger to build, push and deploy Docker images to Kubernetes.

import Tabs from '@theme/Tabs'; import TabItem from '@theme/TabItem';

## Prerequisites

For this tutorial, you will need a Kubernetes cluster.

<Tabs defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'}, {label: 'GKE', value: 'gke'}, {label: 'EKS', value: 'eks'},
]}>

<TabItem value="kind">

[Kind](https://kind.sigs.k8s.io/docs/user/quick-start) is a tool for running local Kubernetes clusters using Docker.

1\. Install kind

Follow [these instructions](https://kind.sigs.k8s.io/docs/user/quick-start) to install Kind.

Alternatively, on macOS using [homebrew](https://brew.sh/):

```shell
brew install kind
```

2\. Start a local registry

```shell
docker run -d -p 5000:5000 --name registry registry:2
```

3\. Create a cluster with the local registry enabled in containerd

```shell
cat <<EOF | kind create cluster --config=-
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
containerdConfigPatches:
- |-
  [plugins."io.containerd.grpc.v1.cri".registry.mirrors."localhost:5000"]
    endpoint = ["http://registry:5000"]
EOF
```

4\. Connect the registry to the cluster network

```shell
docker network connect kind registry
```

  </TabItem>

  <TabItem value="gke">

This tutorial can be run against a [GCP GKE](https://cloud.google.com/kubernetes-engine) cluster
and [GCR](https://cloud.google.com/container-registry). You can follow
this [GCP documentation](https://cloud.google.com/kubernetes-engine/docs/quickstart) to create a GKE cluster. You will
also need to create
a [kubeconfig](https://cloud.google.com/kubernetes-engine/docs/quickstart#get_authentication_credentials_for_the_cluster)
.

  </TabItem>

  <TabItem value="eks">

This tutorial can be run against a [AWS EKS](https://aws.amazon.com/eks/) cluster and [ECR](https://aws.amazon.com/ecr/)
. You can follow this [AWS documentation](https://docs.aws.amazon.com/eks/latest/userguide/getting-started-console.html)
to create an EKS cluster. You will also need to create
a [kubeconfig](https://docs.aws.amazon.com/eks/latest/userguide/create-kubeconfig.html).

  </TabItem>
</Tabs>

## Initialize a Dagger Project and Environment

### (optional) Setup example app

You will need the local copy of the [Dagger examples repository](https://github.com/dagger/examples) used in previous
guides

```shell
git clone https://github.com/dagger/examples
```

Make sure that all commands are run from the todoapp directory:

```shell
cd examples/todoapp
```

### Organize your package

Let's create a new directory for our Cue package:

```shell
mkdir kube
```

### Deploy using Kubectl

Kubernetes objects are located inside the `k8s` folder:

```shell
ls -l k8s
# k8s
# ├── deployment.yaml
# └── service.yaml

# 0 directories, 2 files
```

As a starting point, let's deploy them manually with `kubectl`:

```shell
kubectl apply -f k8s/
# deployment.apps/todoapp created
# service/todoapp-service created
```

Verify that the deployment worked:

```shell
kubectl get deployments
# NAME      READY   UP-TO-DATE   AVAILABLE   AGE
# todoapp   1/1     1            1           10m

kubectl get service
# NAME              TYPE        CLUSTER-IP      EXTERNAL-IP   PORT(S)        AGE
# todoapp-service   NodePort    10.96.225.114   <none>        80:32658/TCP   11m
```

The next step is to transpose it in Cue. Before continuing, clean everything:

```shell
kubectl delete -f k8s/
# deployment.apps "todoapp" deleted
# service "todoapp-service" deleted
```

## Create a basic plan

Create a file named `todoapp.cue` and add the following configuration to it.

```cue file=tests/kube-kind/basic/todoapp.cue title="todoapp/kube/todoapp.cue"
```

This defines a `todoApp` variable containing the Kubernetes objects used to create a todoapp deployment. It also
references a `kubeconfig` value defined below:

<Tabs defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'}, {label: 'GKE', value: 'gke'}, {label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

The following `config.cue` defines:

- `kubeconfig` a generic value created to embed this string `kubeconfig` value

```cue file=tests/kube-kind/config.cue title="todoapp/kube/config.cue"
```

  </TabItem>

  <TabItem value="gke">

The below `config.cue` defines:

- `kubeconfig` a generic value created to embbed this `gke.#KubeConfig` value
- `gcpConfig`: connection to Google using `alpha.dagger.io/gcp`
- `gkeConfig`: transform a `gcpConfig` to a readable format for `kubernetes.#Resources.kubeconfig`
  using `alpha.dagger.io/gcp/gke`

```cue file=tests/kube-gcp/basic/config.cue title="todoapp/kube/config.cue"
```

  </TabItem>

  <TabItem value="eks">

The below `config.cue` defines:

- `kubeconfig`, a generic value created to embbed this `eksConfig.kubeconfig` value
- `awsConfig`, connection to Amazon using `alpha.dagger.io/aws`
- `eksConfig`, transform a `awsConfig` to a readable format for `kubernetes.#Resources.kubeconfig`
  using `alpha.dagger.io/aws/eks`

```cue file=tests/kube-aws/basic/config.cue title="todoapp/kube/config.cue"
```

  </TabItem>

</Tabs>

### Setup the environment

#### Create a new environment

Now that your Cue package is ready, let's create an environment to run it:

```shell
dagger new 'kube' -p kube
```

### Configure the environment

Before we can bring up the deployment, we need to provide the `kubeconfig` input declared in the configuration.
Otherwise, Dagger will complain about a missing input:

```shell
dagger up -e kube
# 5:05PM ERR system | required input is missing    input=kubeconfig
# 5:05PM ERR system | required input is missing    input=manifest
# 5:05PM FTL system | some required inputs are not set, please re-run with `--force` if you think it's a mistake    missing=0s
```

You can inspect the list of inputs (both required and optional) using `dagger input list`:

<Tabs defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'}, {label: 'GKE', value: 'gke'}, {label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

```shell
dagger input list -e kube
# Input              Value                Set by user  Description
# kubeconfig         string               false        set with `dagger input text kubeconfig -f "$HOME"/.kube/config -e kube`
# manifest           dagger.#Artifact     false        input: source code repository, must contain a Dockerfile set with `dagger input dir manifest ./k8s -e kube`
# todoApp.namespace  *"default" | string  false        Kubernetes Namespace to deploy to
# todoApp.version    *"v1.19.9" | string  false        Version of kubectl client
```

  </TabItem>

  <TabItem value="gke">

```shell
dagger input list -e kube
# Input                  Value                Set by user  Description
# gcpConfig.region       string               false        GCP region
# gcpConfig.project      string               false        GCP project
# gcpConfig.serviceKey   dagger.#Secret       false        GCP service key
# manifest               dagger.#Artifact     false        input: source code repository, must contain a Dockerfile set with `dagger input dir manifest ./k8s -e kube`
# gkeConfig.clusterName  string               false        GKE cluster name
# gkeConfig.version      *"v1.19.9" | string  false        Kubectl version
# todoApp.namespace      *"default" | string  false        Kubernetes Namespace to deploy to
# todoApp.version        *"v1.19.9" | string  false        Version of kubectl client
```

  </TabItem>

  <TabItem value="eks">

```shell
dagger input list -e kube
# Input                  Value                Set by user  Description
# awsConfig.region       string               false        AWS region
# awsConfig.accessKey    dagger.#Secret       false        AWS access key
# awsConfig.secretKey    dagger.#Secret       false        AWS secret key
# manifest               dagger.#Artifact     false        input: source code repository, must contain a Dockerfile set with `dagger input dir manifest ./k8s -e kube`
# eksConfig.clusterName  string               false        EKS cluster name
# eksConfig.version      *"v1.19.9" | string  false        Kubectl version
# todoApp.namespace      *"default" | string  false        Kubernetes Namespace to deploy to
# todoApp.version        *"v1.19.9" | string  false        Version of kubectl client
```

  </TabItem>
</Tabs>

Let's provide the missing inputs:

<Tabs defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'}, {label: 'GKE', value: 'gke'}, {label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

```shell
# we'll use the "$HOME"/.kube/config created by `kind`
dagger input text kubeconfig -f "$HOME"/.kube/config -e kube

# Add as an artifact the k8s folder
dagger input dir manifest ./k8s -e kube
```

  </TabItem>

  <TabItem value="gke">

```shell
# Add as an artifact the k8s folder
dagger input dir manifest ./k8s -e kube

# Add Google credentials
dagger input text gcpConfig.project <PROJECT> -e kube
dagger input text gcpConfig.region <REGION> -e kube
dagger input secret gcpConfig.serviceKey -f <PATH TO THE SERVICEKEY.json> -e kube

#  Add GKE clusterName
dagger input text gkeConfig.clusterName <GKE CLUSTER NAME> -e kube
```

  </TabItem>

  <TabItem value="eks">

```shell
# Add as an artifact the k8s folder
dagger input dir manifest ./k8s -e kube

# Add Amazon credentials
dagger input text awsConfig.region <REGION> -e kube
dagger input secret awsConfig.accessKey <ACCESS KEY> -e kube
dagger input secret awsConfig.secretKey <SECRET KEY> -e kube

# Add EKS clustername
dagger input text eksConfig.clusterName <EKS CLUSTER NAME> -e kube
```

  </TabItem>
</Tabs>

### Deploying

Now is time to deploy to Kubernetes.

```shell
dagger up -e kube
# deploy | computing
# deploy | #26 0.700 deployment.apps/todoapp created
# deploy | #27 0.705 service/todoapp-service created
# deploy | completed    duration=1.405s
```

Let's verify if the deployment worked:

```shell
kubectl get deployments
# NAME    READY   UP-TO-DATE   AVAILABLE   AGE
# todoapp   1/1     1            1           1m
```

Before continuing, cleanup deployment:

```shell
kubectl delete -f k8s/
# deployment.apps "todoapp" deleted
# service "todoapp-service" deleted
```

## Building, pushing, and deploying Docker images

Rather than deploying an existing (`todoapp`) image, we're going to build a Docker image from the source, push it to a
registry, and update the Kubernetes configuration.

### Update the plan

<Tabs defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'}, {label: 'GKE', value: 'gke'}, {label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

Let's see how to deploy an image locally and push it to the local cluster

`kube/todoapp.cue` faces these changes:

- `repository`, source code of the app to build. It needs to have a Dockerfile
- `registry`, URI of the registry to push to
- `image`, build of the image
- `remoteImage`, push an image to the registry
- `kustomization`, apply kustomization to image

```cue file=tests/kube-kind/deployment/todoapp.cue title="todoapp/kube/todoapp.cue"
```

  </TabItem>

  <TabItem value="gke">

Let's see how to leverage [GCR](https://github.com/dagger/dagger/tree/main/stdlib/gcp/gcr)
and [GKE](https://github.com/dagger/dagger/tree/main/stdlib/gcp/gke) packages.

The two files have to be edited to do so.

`kube/config.cue` configuration has following change:

- definition of a new `gcrCreds` value that contains ecr credentials for remote image push to GCR

```cue file=tests/kube-gcp/deployment/config.cue title="todoapp/kube/config.cue"
```

`kube/todoapp.cue`, on the other hand, faces these changes:

- `repository`, source code of the app to build. It needs to have a Dockerfile
- `registry`, URI of the registry to push to
- `image`, build of the image
- `remoteImage`, push an image to the registry
- `kustomization`, apply kustomization to image

```cue file=tests/kube-gcp/deployment/todoapp.cue title="todoapp/kube/todoapp.cue"
```

  </TabItem>
  <TabItem value="eks">

Let's see how to leverage [ECR](https://github.com/dagger/dagger/tree/main/stdlib/aws/ecr)
and [EKS](https://github.com/dagger/dagger/tree/main/stdlib/aws/eks) packages.

The two files have to be edited to do so.

`kube/config.cue` configuration has following change:

- definition of a new `ecrCreds` value that contains ecr credentials for remote image push to ECR

```cue file=tests/kube-aws/deployment/config.cue title="todoapp/kube/config.cue"
```

`kube/todoapp.cue`, on the other hand, faces these changes:

- `repository`, source code of the app to build. It needs to have a Dockerfile
- `registry`, URI of the registry to push to
- `image`, build of the image
- `remoteImage`, push an image to the registry
- `kustomization`, apply kustomization to image

```cue file=tests/kube-aws/deployment/todoapp.cue title="todoapp/kube/todoapp.cue"
```

  </TabItem>
</Tabs>

### Connect the Inputs

<Tabs defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'}, {label: 'GKE', value: 'gke'}, {label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

Next, we'll provide the two new inputs, `repository` and `registry`.

```shell
# A name after `localhost:5000/` is required to avoid error on push to the local registry
dagger input text registry "localhost:5000/kind" -e kube

# Add todoapp (current folder) to repository value
dagger input dir repository . -e kube
```

  </TabItem>
  <TabItem value="gke">

Next, we'll provide the two new inputs, `repository` and `registry`.

```shell
# Add registry to export built image to
dagger input text registry <URI> -e kube

# Add todoapp (current folder) to repository value
dagger input dir repository . -e kube
```

  </TabItem>
  <TabItem value="eks">

Next, we'll provide the two new inputs, `repository` and `registry`.

```shell
# Add registry to export built image to
dagger input text registry <URI> -e kube

# Add todoapp (current folder) to repository value
dagger input dir repository . -e kube
```

  </TabItem>
</Tabs>

### Bring up the changes

```shell
dagger up -e kube
# 4:09AM INF manifest | computing
# 4:09AM INF repository | computing
# ...
# 4:09AM INF todoApp.kubeSrc | #37 0.858 service/todoapp-service created
# 4:09AM INF todoApp.kubeSrc | #37 0.879 deployment.apps/todoapp created
# Output                      Value                                                                                                              Description
# todoApp.remoteImage.ref     "localhost:5000/kind:test-kind@sha256:cb8d92518b876a3fe15a23f7c071290dfbad50283ad976f3f5b93e9f20cefee6"            Image ref
# todoApp.remoteImage.digest  "sha256:cb8d92518b876a3fe15a23f7c071290dfbad50283ad976f3f5b93e9f20cefee6"                                          Image digest
```

Let's verify if the deployment worked:

```shell
kubectl get deployments
# NAME      READY   UP-TO-DATE   AVAILABLE   AGE
# todoapp   1/1     1            1           50s
```

Before continuing, cleanup deployment:

```shell
kubectl delete -f k8s/
# deployment.apps "todoapp" deleted
# service "todoapp-service" deleted
```

## CUE Kubernetes manifest

This section will convert Kubernetes YAML manifest from `k8s` directory to [CUE](https://cuelang.org/) to take advantage
of the language features.

> For a more advanced example, see the [official CUE Kubernetes tutorial](https://github.com/cue-lang/cue/blob/v0.4.0/doc/tutorial/kubernetes/README.md)

### Convert Kubernetes objects to CUE

First, let's create re-usable definitions for the `deployment` and the `service` to remove a lot of boilerplate and
repetition.

Let's define a re-usable `#Deployment` definition in `kube/deployment.cue`.

```cue file=tests/kube-kind/cue-manifest/deployment.cue title="todoapp/kube/deployment.cue"
```

Indeed, let's also define a re-usable `#Service` definition in `kube/service.cue`.

```cue file=tests/kube-kind/cue-manifest/service.cue title="todoapp/kube/service.cue"
```

### Generate Kubernetes manifest

Now that you have generic definitions for your Kubernetes objects. You can use them to get back your YAML definition
without having boilerplate nor repetition.

Create a new definition named `#AppManifest` that will generate the YAML in `kube/manifest.cue`.

```cue file=tests/kube-kind/cue-manifest/manifest.cue title="todoapp/kube/manifest.cue"
```

### Update manifest

You can now remove the `manifest` input in `kube/todoapp.cue` and instead use the manifest created by `#AppManifest`.

`kube/todoapp.cue` configuration has following changes:

- removal of unused imported `encoding/yaml` and `kustomize` packages.
- removal of `manifest` input that is doesn't need anymore.
- removal of `kustomization` to replace it with `#AppManifest` definition.
- Update `kubeSrc` to use `manifest` field instead of `source` because we don't send Kubernetes manifest
  of `dagger.#Artifact` type anymore.

<Tabs defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'}, {label: 'GKE', value: 'gke'}, {label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

```cue file=tests/kube-kind/cue-manifest/todoapp.cue title="todoapp/kube/todoapp.cue"
```

  </TabItem>
  <TabItem value="gke">

```cue file=tests/kube-gcp/cue-manifest/todoapp.cue title="todoapp/kube/todoapp.cue"
```

  </TabItem>
  <TabItem value="eks">

```cue file=tests/kube-aws/cue-manifest/todoapp.cue title="todoapp/kube/todoapp.cue"
```

  </TabItem>
</Tabs>

### Remove unused input

Now that we manage our Kubernetes manifest in CUE, we don't need `manifest` anymore.

```shell
# Remove `manifest` input
dagger input unset manifest -e kube
```

### Deployment

```shell
dagger up -e kube
# 4:09AM INF manifest | computing
# 4:09AM INF repository | computing
# ...
# 4:09AM INF todoApp.kubeSrc | #37 0.858 service/todoapp-service created
# 4:09AM INF todoApp.kubeSrc | #37 0.879 deployment.apps/todoapp created
# Output                      Value                                                                                                              Description
# todoApp.remoteImage.ref     "localhost:5000/kind:test-kind@sha256:cb8d91518b076a3fe15a33f7c171290dfbad50283ad976f3f5b93e9f33cefag7"            Image ref
# todoApp.remoteImage.digest  "sha256:cb8d91518b076a3fe15a33f7c171290dfbad50283ad976f3f5b93e9f33cefag7"                                          Image digest
```

Let's verify that the deployment worked:

```shell
kubectl get deployments
# NAME      READY   UP-TO-DATE   AVAILABLE   AGE
# todoapp   1/1     1            1           37s
```

## Next Steps

Integrate Helm with Dagger:

- [Helm](https://github.com/dagger/dagger/tree/main/stdlib/kubernetes/helm)
