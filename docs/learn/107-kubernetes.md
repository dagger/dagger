---
slug: /learn/107-kubernetes
---

# Dagger 107: deploy to Kubernetes

This tutorial illustrates how to use dagger to build, push and deploy Docker
images to Kubernetes.

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

## Prerequisites

For this tutorial, you will need a Kubernetes cluster.

<Tabs
defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'},
{label: 'GKE', value: 'gke'},
{label: 'EKS', value: 'eks'},
]}>

<TabItem value="kind">

[Kind](https://kind.sigs.k8s.io/docs/user/quick-start) is a tool for running local Kubernetes clusters using Docker.

1\. Install kind

Follow [these instructions](https://kind.sigs.k8s.io/docs/user/quick-start) to
install kind.

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

This tutorial can be run against a [GCP GKE](https://cloud.google.com/kubernetes-engine) cluster and [GCR](https://cloud.google.com/container-registry)

  </TabItem>

  <TabItem value="eks">

This tutorial can be run against a [AWS EKS](https://aws.amazon.com/eks/) cluster and [ECR](https://aws.amazon.com/ecr/)

  </TabItem>
</Tabs>

## Initialize a Dagger Workspace and Environment

### (optional) Setup example app

You will need the local copy of the [Dagger examples repository](https://github.com/dagger/examples) used in previous guides

```shell
git clone https://github.com/dagger/examples
```

Make sure that all commands are run from the todoapp directory:

```shell
cd examples/todoapp
```

### (optional) Initialize a Cue module

In this guide we will use the same directory as the root of the Dagger workspace and the root of the Cue module; but you can create your Cue module anywhere inside the Dagger workspace.

```shell
cue mod init
```

### Organize your package

Let's create a new directory for our Cue package:

```shell
mkdir kube
```

## Create a basic plan

Create a file named `manifest.cue` and add the
following configuration to it.

```cue title="todoapp/kube/manifest.cue"
package kube

// inlined kubernetes manifest as a string
manifest: """
  apiVersion: apps/v1
  kind: Deployment
  metadata:
    name: nginx
    labels:
      app: nginx
  spec:
    replicas: 1
    selector:
      matchLabels:
        app: nginx
    template:
      metadata:
        labels:
          app: nginx
      spec:
        containers:
          - name: nginx
            image: nginx:1.14.2
            ports:
              - containerPort: 80
  """
```

This will define a `manifest` variable containing the inlined Kubernetes YAML
used to create a _nginx_ deployment.

Next, create `source.cue`.

<Tabs
defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'},
{label: 'GKE', value: 'gke'},
{label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

```cue title="todoapp/kube/source.cue"
package kube

import (
  "dagger.io/dagger"
  "dagger.io/kubernetes"
)

// input: ~/.kube/config file used for deployment
// set with `dagger input secret kubeconfig -f ~/.kube/config`
kubeconfig: dagger.#Secret @dagger(input)

// deploy uses the `dagger.io/kubernetes` package to apply a manifest to a
// Kubernetes cluster.
deploy: kubernetes.#Resources & {
  // reference the `kubeconfig` input above
  "kubeconfig": kubeconfig

  // reference to the manifest defined in `manifest.cue`
  "manifest": manifest
}
```

  </TabItem>

  <TabItem value="gke">

```cue title="todoapp/kube/source.cue"
package kube

import (
  "dagger.io/kubernetes"
  "dagger.io/gcp/gke"
)

// gkeConfig used for deployment
gkeConfig: gke.#KubeConfig @dagger(input)

kubeconfig: gkeConfig.kubeconfig

// deploy uses the `dagger.io/kubernetes` package to apply a manifest to a
// Kubernetes cluster.
deploy: kubernetes.#Resources & {
  // reference the `kubeconfig` input above
  "kubeconfig": kubeconfig

  // reference to the manifest defined in `manifest.cue`
  "manifest": manifest
}
```

  </TabItem>

  <TabItem value="eks">

```cue title="todoapp/kube/source.cue"
package kube

import (
  "dagger.io/kubernetes"
  "dagger.io/aws/eks"
)

// eksConfig used for deployment
eksConfig: eks.#KubeConfig @dagger(input)

kubeconfig: eksConfig.kubeconfig

// deploy uses the `dagger.io/kubernetes` package to apply a manifest to a
// Kubernetes cluster.
deploy: kubernetes.#Resources & {
  // reference the `kubeconfig` input above
  "kubeconfig": kubeconfig

  // reference to the manifest defined in `manifest.cue`
  "manifest": manifest
}
```

  </TabItem>
</Tabs>

This defines:

- `kubeconfig` a _string_ **input**: kubernetes configuration (`~/.kube/config`)
  used for `kubectl`
- `deploy`: Deployment step using the package `dagger.io/kubernetes`. It takes
  the `manifest` defined earlier and deploys it to the Kubernetes cluster specified in `kubeconfig`.

### Setup the environment

#### Create a new environment

Now that your Cue package is ready, let's create an environment to run it:

```shell
dagger new 'kube'
```

#### Load the plan into the environment

Now let's configure the new environment to use our package as its plan:

```shell
cp kube/*.cue .dagger/env/kube/plan/
```

Note: you need to copy the files from your package into the environment, as shown above. If you make more changes to your package, you will need to copy the new version, or it will not be used. In the future, we will add the ability to reference your Cue package directory, making this manual copy unnecessary.

### Configure the environment

Before we can bring up the deployment, we need to provide the `kubeconfig` input
declared in the configuration. Otherwise, dagger will complain about a missing input:

```shell
$ dagger up
6:53PM ERR system | required input is missing    input=kubeconfig
```

You can inspect the list of inputs (both required and optional) using `dagger input list`:

<!--
<Tabs
  defaultValue="kind"
  groupId="provider"
  values={[
    {label: 'kind', value: 'kind'},
    {label: 'GKE', value: 'gke'},
    {label: 'EKS', value: 'eks'},
  ]}>

  <TabItem value="kind">
  </TabItem>

  <TabItem value="gke">
  </TabItem>

  <TabItem value="eks">
  </TabItem>
</Tabs>
-->

<Tabs
defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'},
{label: 'GKE', value: 'gke'},
{label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

```shell
$ dagger input list
Input             Type              Description
kubeconfig        string            ~/.kube/config file used for deployment
deploy.namespace  string            Kubernetes Namespace to deploy to
```

  </TabItem>

  <TabItem value="gke">

```shell
$ dagger input list
Input                        Type              Description
deploy.namespace             string            Kubernetes Namespace to deploy to
gkeConfig.config.region      string            GCP region
gkeConfig.config.project     string            GCP project
gkeConfig.config.serviceKey  dagger.#Secret    GCP service key
gkeConfig.clusterName        string            GKE cluster name
```

  </TabItem>

  <TabItem value="eks">

```shell
$ dagger input list
Input                       Type              Description
deploy.namespace            string            Kubernetes Namespace to deploy to
eksConfig.config.region     string            AWS region
eksConfig.config.accessKey  dagger.#Secret    AWS access key
eksConfig.config.secretKey  dagger.#Secret    AWS secret key
eksConfig.clusterName       string            EKS cluster name
```

  </TabItem>
</Tabs>

Let's provide the missing inputs:

<Tabs
defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'},
{label: 'GKE', value: 'gke'},
{label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

```shell
# we'll use the ~/.kube/config created by `kind`
dagger input text kubeconfig -f ~/.kube/config
```

  </TabItem>

  <TabItem value="gke">

```shell
dagger input text gkeConfig.config.project <PROJECT>
dagger input text gkeConfig.config.region <REGION>
dagger input text gkeConfig.clusterName <GKE CLUSTER NAME>
dagger input secret gkeConfig.config.serviceKey -f <PATH TO THE SERVICEKEY.json>
```

  </TabItem>

  <TabItem value="eks">

```shell
dagger input text eksConfig.config.region <REGION>
dagger input text eksConfig.clusterName <EKS CLUSTER NAME>
dagger input secret eksConfig.config.accessKey <ACCESS KEY>
dagger input secret eksConfig.config.secretKey <SECRET KEY>
```

  </TabItem>
</Tabs>

### Deploying

Now is time to deploy to kubernetes.

```shell
$ dagger up
deploy | computing
deploy | #26 0.700 deployment.apps/nginx created
deploy | completed    duration=900ms
```

Let's verify the deployment worked:

```shell
$ kubectl get deployments
NAME    READY   UP-TO-DATE   AVAILABLE   AGE
nginx   1/1     1            1           1m
```

## CUE Kubernetes manifests

In this section we will convert the inlined YAML manifest to CUE to take advantage of the language features.

For a more advanced example, see the
[official CUE Kubernetes tutorial](https://github.com/cuelang/cue/blob/v0.4.0/doc/tutorial/kubernetes/README.md)

First, let's replace `manifest.cue` with the following configuration. This is a
straightforward one-to-one conversion from YAML to CUE, only the syntax has changed.

```cue title="todoapp/kube/manifest.cue"
package kube

import (
  "encoding/yaml"
)

nginx: {
  apiVersion: "apps/v1"
  kind:       "Deployment"
  metadata: {
    "name": "nginx"
    labels: app: "nginx"
  }
  spec: {
    replicas: 1
    selector: matchLabels: app: "nginx"
    template: {
      metadata: labels: app: "nginx"
      spec: containers: [{
        "name":  "nginx"
        "image": image
        ports: [{
          containerPort: port
        }]
      }]
    }
  }
}

manifest: yaml.Marshal(nginx)
```

We're using the built-in `yaml.Marshal` function to convert CUE back to YAML so
Kubernetes still receives the same manifest.

You need to copy the changes to the plan in order for Dagger to reference them

```shell
cp kube/*.cue .dagger/env/kube/plan/
```

You can inspect the configuration using `dagger query` to verify it produces the
same manifest:

```shell
$ dagger query manifest -f text
apiVersion: apps/v1
kind: Deployment
...
```

Now that the manifest is defined in CUE, we can take advantage of the language
to remove a lot of boilerplate and repetition.

Let's define a re-usable `#Deployment` definition in `todoapp/kube/deployment.cue"`:

```cue title="todoapp/kube/deployment.cue"
package kube

// Deployment template containing all the common boilerplate shared by
// deployments of this application.
#Deployment: {
  // name of the deployment. This will be used to automatically label resouces
  // and generate selectors.
  name: string

  // container image
  image: string

  // 80 is the default port
  port: *80 | int

  // 1 is the default, but we allow any number
  replicas: *1 | int

  // Deployment manifest. Uses the name, image, port and replicas above to
  // generate the resource manifest.
  manifest: {
    apiVersion: "apps/v1"
    kind:       "Deployment"
    metadata: {
      "name": name
      labels: app: name
    }
    spec: {
      "replicas": replicas
      selector: matchLabels: app: name
      template: {
        metadata: labels: app: name
        spec: containers: [{
          "name":  name
          "image": image
          ports: [{
            containerPort: port
          }]
        }]
      }
    }
  }
}
```

`manifest.cue` can be rewritten as follows:

```cue title="todoapp/kube/manifest.cue"
import (
  "encoding/yaml"
)

nginx: #Deployment & {
  name:  "nginx"
  image: "nginx:1.14.2"
}

manifest: yaml.Marshal(nginx.manifest)
```

Update the plan

```shell
cp kube/*.cue .dagger/env/kube/plan/
```

Let's make sure it yields the same result:

```shell
$ dagger query deploy.manifest -f text
apiVersion: apps/v1
kind: Deployment
...
```

And we can now deploy it:

```shell
$ dagger up
deploy | computing
deploy | #26 0.700 deployment.apps/nginx unchanged
deploy | completed    duration=900ms
```

## Building, pushing and deploying Docker images

Rather than deploying an existing (`nginx`) image, we're going to build a Docker
image from source, push it to a registry and update the kubernetes configuration.

### Update the plan

The following configuration will:

- Declare a `repository` input as a `dagger.#Artifact`. This will be mapped to
  the source code directory.
- Declare a `registry` input. This is the address used for docker push
- Use `dagger.io/docker` to build and push the image
- Use the registry image reference (`push.ref`) as the image for the deployment.

```cue title="todoapp/kube/manifest.cue"
package kube

import (
  "encoding/yaml"

  "dagger.io/dagger"
  "dagger.io/docker"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository ./app`
repository: dagger.#Artifact @dagger(input)

// registry to push images to
registry: string @dagger(input)

// docker build the `repository` directory
image: docker.#Build & {
    source: repository
}

// push the `image` to the `registry`
push: docker.#Push & {
  source: image
  ref: registry
}

// use the `#Deployment` template to generate the kubernetes manifest
app: #Deployment & {
  name: "test"

  // use the reference of the image we just pushed
  // this creates a dependency: `app` will only be deployed after the image is
  // built and pushed.
  "image": push.ref
}

manifest: yaml.Marshal(app.manifest)
```

Update the plan

```shell
cp kube/*.cue .dagger/env/kube/plan/
```

### Connect the Inputs

Next, we'll provide the two new inputs, `repository` and `registry`.

For the purpose of this tutorial we'll be using
[hello-go](https://github.com/aluzzardi/hello-go) as example source code.

```shell
$ git clone https://github.com/aluzzardi/hello-go.git
dagger input dir repository ./hello-go
dagger input text registry "localhost:5000/image"
```

### Bring up the changes

```shell
$ dagger up
repository | computing
repository | completed    duration=0s
image | computing
image | completed    duration=1s
deploy | computing
deploy | #26 0.709 deployment.apps/hello created
deploy | completed    duration=900ms
```

Let's verify the deployment worked:

```shell
$ kubectl get deployments
NAME    READY   UP-TO-DATE   AVAILABLE   AGE
nginx   1/1     1            1           1m
hello   1/1     1            1           1m
```

## Next Steps

Deploy on a hosted Kubernetes cluster:

- [GKE](https://github.com/dagger/dagger/tree/main/stdlib/gcp/gke)
- [EKS](https://github.com/dagger/dagger/tree/main/stdlib/aws/eks)

Authenticate to a remote registry:

- [ECR](https://github.com/dagger/dagger/tree/main/stdlib/aws/ecr)
- [GCR](https://github.com/dagger/dagger/tree/main/stdlib/gcp/gcr)

Integrate kubernetes tools with Dagger:

- [Helm](https://github.com/dagger/dagger/tree/main/stdlib/kubernetes/helm)
- [Kustomize](https://github.com/dagger/dagger/tree/main/stdlib/kubernetes/kustomize)
