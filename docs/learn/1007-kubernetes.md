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

## Initialize a Dagger Workspace and Environment

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

### (optional) Initialize a Cue module

This guide will use the same directory as the root of the Dagger workspace and the root of the Cue module, but you can
create your Cue module anywhere inside the Dagger workspace.

```shell
cue mod init
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

```cue title="todoapp/kube/todoapp.cue"
package main

import (
 "alpha.dagger.io/dagger"
 "alpha.dagger.io/kubernetes"
)

// input: kubernetes objects directory to deploy to
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

// Deploy the manifest to a kubernetes cluster
todoApp: kubernetes.#Resources & {
 "kubeconfig": kubeconfig
 source: manifest
}
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

```cue title="todoapp/kube/config.cue"
package main

import (
 "alpha.dagger.io/dagger"
)

// set with `dagger input text kubeconfig -f "$HOME"/.kube/config -e kube`
kubeconfig: string & dagger.#Input
```

  </TabItem>

  <TabItem value="gke">

The below `config.cue` defines:

- `kubeconfig` a generic value created to embbed this `gke.#KubeConfig` value
- `gcpConfig`: connection to Google using `alpha.dagger.io/gcp`
- `gkeConfig`: transform a `gcpConfig` to a readable format for `kubernetes.#Resources.kubeconfig`
  using `alpha.dagger.io/gcp/gke`

```cue title="todoapp/kube/config.cue"
package main

import (
 "alpha.dagger.io/gcp"
 "alpha.dagger.io/gcp/gke"
)

// Value created for generic reference of `kubeconfig` in `todoapp.cue`
kubeconfig: gkeConfig.kubeconfig

// gcpConfig used for Google connection
gcpConfig: gcp.#Config

// gkeConfig used for deployment
gkeConfig: gke.#KubeConfig & {
 // config field references `gkeConfig` value to set in once
 config: gcpConfig
}
```

  </TabItem>

  <TabItem value="eks">

The below `config.cue` defines:

- `kubeconfig`, a generic value created to embbed this `eksConfig.kubeconfig` value
- `awsConfig`, connection to Amazon using `alpha.dagger.io/aws`
- `eksConfig`, transform a `awsConfig` to a readable format for `kubernetes.#Resources.kubeconfig`
  using `alpha.dagger.io/aws/eks`

```cue title="todoapp/kube/config.cue"
package main

import (
 "alpha.dagger.io/aws"
 "alpha.dagger.io/aws/eks"
)

// Value created for generic reference of `kubeconfig` in `todoapp.cue`
kubeconfig: eksConfig.kubeconfig

// awsConfig for Amazon connection
awsConfig: aws.#Config

// eksConfig used for deployment
eksConfig: eks.#KubeConfig & {
 // config field references `gkeConfig` value to set in once
 config: awsConfig
}
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

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "encoding/yaml"

  "alpha.dagger.io/dagger"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
  "alpha.dagger.io/kubernetes/kustomize"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// Registry to push images to
registry: string & dagger.#Input
tag:      "test-kind"

// input: kubernetes objects directory to deploy to
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

// Todoapp deployment pipeline
todoApp: {
  // Build the image from repositoru artifact
  image: docker.#Build & {
    source: repository
  }

  // Push image to registry
  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
  }

  // Update the image from manifest to use the deployed one
  kustomization: kustomize.#Kustomize & {
    source:        manifest

    // Convert CUE to YAML.
    kustomization: yaml.Marshal({
      resources: ["deployment.yaml", "service.yaml"]

      images: [{
        name:    "public.ecr.aws/j7f8d3t2/todoapp"
        newName: remoteImage.ref
      }]
    })
  }

  // Deploy the customized manifest to a kubernetes cluster
  kubeSrc: kubernetes.#Resources & {
    "kubeconfig": kubeconfig
    source:     kustomization
  }
}
```

  </TabItem>

  <TabItem value="gke">

Let's see how to leverage [GCR](https://github.com/dagger/dagger/tree/main/stdlib/gcp/gcr)
and [GKE](https://github.com/dagger/dagger/tree/main/stdlib/gcp/gke) packages.

The two files have to be edited to do so.

`kube/config.cue` configuration has following change:

- definition of a new `ecrCreds` value that contains ecr credentials for remote image push to GCR

```cue title="todoapp/kube/config.cue"
package main

import (
 "alpha.dagger.io/gcp"
 "alpha.dagger.io/gcp/gcr"
 "alpha.dagger.io/gcp/gke"
)

// Value created for generic reference of `kubeconfig` in `todoapp.cue`
kubeconfig: gkeConfig.kubeconfig

// gcpConfig used for Google connection
gcpConfig: gcp.#Config

// gkeConfig used for deployment
gkeConfig: gke.#KubeConfig & {
 // config field references `gkeConfig` value to set in once
 config: gcpConfig
}

// gcrCreds used for remote image push
gcrCreds: gcr.#Credentials & {
 // config field references `gcpConfig` value to set in once
  config: gcpConfig
}
```

`kube/todoapp.cue`, on the other hand, faces these changes:

- `repository`, source code of the app to build. It needs to have a Dockerfile
- `registry`, URI of the registry to push to
- `image`, build of the image
- `remoteImage`, push an image to the registry
- `kustomization`, apply kustomization to image

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "encoding/yaml"

  "alpha.dagger.io/dagger"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
  "alpha.dagger.io/kubernetes/kustomize"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// GCR registry to push images to
registry: string & dagger.#Input
tag:      "test-gcr"

// source of Kube config file.
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

// Declarative name
todoApp: {
  // Build an image from the project repository
  image: docker.#Build & {
    source: repository
  }

  // Push the image to a remote registry
  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
    auth: {
      username: gcrCreds.username
      secret:   gcrCreds.secret
    }
  }

  // Update the image of the deployment to the deployed image
  kustomization: kustomize.#Kustomize & {
    source:        manifest

    // Convert CUE to YAML.
    kustomization: yaml.Marshal({
      resources: ["deployment.yaml", "service.yaml"]

      images: [{
        name:    "public.ecr.aws/j7f8d3t2/todoapp"
        newName: remoteImage.ref
      }]
    })
  }

  // Value created for generic reference of `kubeconfig` in `todoapp.cue`
  kubeSrc: kubernetes.#Resources & {
    "kubeconfig": kubeconfig
    source:     kustomization
  }
}
```

  </TabItem>
  <TabItem value="eks">

Let's see how to leverage [ECR](https://github.com/dagger/dagger/tree/main/stdlib/aws/ecr)
and [EKS](https://github.com/dagger/dagger/tree/main/stdlib/aws/eks) packages.

The two files have to be edited to do so.

`kube/config.cue` configuration has following change:

- definition of a new `ecrCreds` value that contains ecr credentials for remote image push to ECR

```cue title="todoapp/kube/config.cue"
package main

import (
 "alpha.dagger.io/aws"
 "alpha.dagger.io/aws/eks"
 "alpha.dagger.io/aws/ecr"
)

// Value created for generic reference of `kubeconfig` in `todoapp.cue`
kubeconfig: eksConfig.kubeconfig

// awsConfig for Amazon connection
awsConfig: aws.#Config

// eksConfig used for deployment
eksConfig: eks.#KubeConfig & {
 // config field references `awsConfig` value to set in once
 config: awsConfig
}

// ecrCreds used for remote image push
ecrCreds: ecr.#Credentials & {
 // config field references `awsConfig` value to set in once
  config: awsConfig
}
```

`kube/todoapp.cue`, on the other hand, faces these changes:

- `repository`, source code of the app to build. It needs to have a Dockerfile
- `registry`, URI of the registry to push to
- `image`, build of the image
- `remoteImage`, push an image to the registry
- `kustomization`, apply kustomization to image

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "encoding/yaml"

  "alpha.dagger.io/dagger"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
  "alpha.dagger.io/kubernetes/kustomize"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// ECR registry to push images to
registry: string & dagger.#Input
tag:      "test-ecr"

// source of Kube config file.
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

todoApp: {
  // Build an image from the project repository
  image: docker.#Build & {
    source: repository
  }

  // Push the image to a remote registry
  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
    auth: {
      username: ecrCreds.username
      secret:   ecrCreds.secret
    }
  }

  // Update the image of the deployment to the deployed image
  kustomization: kustomize.#Kustomize & {
    source:        manifest

    // Convert CUE to YAML.
    kustomization: yaml.Marshal({
      resources: ["deployment.yaml", "service.yaml"]

      images: [{
        name:    "public.ecr.aws/j7f8d3t2/todoapp"
        newName: remoteImage.ref
      }]
    })
  }

  // Value created for generic reference of `kubeconfig` in `todoapp.cue`
  kubeSrc: kubernetes.#Resources & {
    "kubeconfig": kubeconfig
    source:     kustomization
  }
}
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

> For a more advanced example, see the [official CUE Kubernetes tutorial](https://github.com/cuelang/cue/blob/v0.4.0/doc/tutorial/kubernetes/README.md)

### Convert Kubernetes objects to CUE

First, let's create re-usable definitions for the `deployment` and the `service` to remove a lot of boilerplate
and repetition.

Let's define a re-usable `#Deployment` definition in `kube/deployment.cue`.

```cue title="todoapp/kube/deployment.cue"
package main

// Deployment template containing all the common boilerplate shared by
// deployments of this application.
#Deployment: {
  // Name of the deployment. This will be used to label resources automatically
  // and generate selectors.
  name: string

  // Container image.
  image: string

  // 80 is the default port.
  port: *80 | int

  // 1 is the default, but we allow any number.
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

Indeed, let's also define a re-usable `#Service` definition in `kube/service.cue`.

```cue title="todoapp/kube/service.cue"
package main

// Service template containing all the common boilerplate shared by
// services of this application.
#Service: {
  // Name of the service. This will be used to label resources automatically
  // and generate selector.
  name: string

  // NodePort is the default service type.
  type: *"NodePort" | "LoadBalancer" | "ClusterIP" | "ExternalName"

  // Ports where the service should listen
  ports: [string]: number

  // Service manifest. Uses the name, type and ports above to
  // generate the resource manifest.
  manifest: {
    apiVersion: "v1"
    kind:       "Service"
    metadata: {
      "name": "\(name)-service"
      labels: app: name
    }
    spec: {
      "type": type
        "ports": [
          for k, v in ports {
            "name": k
            port:   v
          },
        ]
        selector: app: name
    }
  }
}
```

### Generate Kubernetes manifest

Now that you have generic definitions for your Kubernetes objects. You can use them to get back your YAML definition
without having boilerplate nor repetition.

Create a new definition named `#AppManifest` that will generate the YAML in `kube/manifest.cue`.

```cue title="todoapp/kube/manifest.cue"
package main

import (
  "encoding/yaml"
)

// Define and generate kubernetes deployment to deploy to kubernetes cluster
#AppManifest: {
  // Name of the application
  name: string

  // Image to deploy to
  image: string

  // Define a kubernetes deployment object
  deployment: #Deployment & {
    "name": name
    "image": image
  }

  // Define a kubernetes service object
  service: #Service & {
    "name": name
    ports: "http": deployment.port
  }

  // Merge definitions and convert them back from CUE to YAML
  manifest: yaml.MarshalStream([deployment.manifest, service.manifest])
}
```

### Update manifest

You can now remove the `manifest` input in `kube/todoapp.cue` and instead use the manifest created by `#AppManifest`.

`kube/todoapp.cue` configuration has following changes:

- removal of unused imported `encoding/yaml` and `kustomize` packages.
- removal of `manifest` input that is doesn't need anymore.
- removal of `kustomization` to replace it with `#AppManifest` definition.
- Update `kubeSrc` to use `manifest` field instead of `source` because we don't send Kubernetes manifest of `dagger.#Artifact` type anymore.

<Tabs defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'}, {label: 'GKE', value: 'gke'}, {label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "alpha.dagger.io/dagger"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// Registry to push images to
registry: string & dagger.#Input
tag:      "test-kind"

// Todoapp deployment pipeline
todoApp: {
  // Build the image from repositoru artifact
  image: docker.#Build & {
    source: repository
  }

  // Push image to registry
  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
  }

  // Generate deployment manifest
  deployment: #AppManifest & {
    name:  "todoapp"
    image: remoteImage.ref
  }

  // Deploy the customized manifest to a kubernetes cluster
  kubeSrc: kubernetes.#Resources & {
    "kubeconfig": kubeconfig
    manifest:     deployment.manifest
  }
}
```

  </TabItem>
  <TabItem value="gke">

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "alpha.dagger.io/dagger"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// GCR registry to push images to
registry: string & dagger.#Input
tag:      "test-gcr"

// Todoapp deployment pipeline
todoApp: {
  // Build the image from repositoru artifact
  image: docker.#Build & {
    source: repository
  }

  // Push image to registry
  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
    auth: {
      username: gcrCreds.username
      secret:   gcrCreds.secret
    }
  }

  // Generate deployment manifest
  deployment: #AppManifest & {
    name:  "todoapp"
    image: remoteImage.ref
  }

  // Deploy the customized manifest to a kubernetes cluster
  kubeSrc: kubernetes.#Resources & {
    "kubeconfig": kubeconfig
    manifest:     deployment.manifest
  }
}
```

  </TabItem>
  <TabItem value="eks">

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "alpha.dagger.io/dagger"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
)

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// ECR registry to push images to
registry: string & dagger.#Input
tag:      "test-ecr"

// Todoapp deployment pipeline
todoApp: {
  // Build the image from repositoru artifact
  image: docker.#Build & {
    source: repository
  }

  // Push image to registry
  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
    auth: {
      username: ecrCreds.username
      secret:   ecrCreds.secret
    }
  }

  // Generate deployment manifest
  deployment: #AppManifest & {
    name:  "todoapp"
    image: remoteImage.ref
  }

  // Deploy the customized manifest to a kubernetes cluster
  kubeSrc: kubernetes.#Resources & {
    "kubeconfig": kubeconfig
    manifest:     deployment.manifest
  }
}
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
