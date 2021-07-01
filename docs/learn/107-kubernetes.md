---
slug: /learn/107-kubernetes
---

# Dagger 107: deploy to Kubernetes

This tutorial illustrates how to use Dagger to build, push and deploy Docker
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
install Kind.

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

This tutorial can be run against a [AWS EKS](https://aws.amazon.com/eks/) cluster and [ECR](https://aws.amazon.com/ecr/). You can follow this [AWS documentation](https://docs.aws.amazon.com/eks/latest/userguide/getting-started-console.html#gs-view-resources) to create an EKS cluster. You will also need to create a [kubeconfig](https://docs.aws.amazon.com/eks/latest/userguide/create-kubeconfig.html)

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

This guide will use the same directory as the root of the Dagger workspace and the root of the Cue module, but you can create your Cue module anywhere inside the Dagger workspace.

```shell
cue mod init
```

### Organize your package

Let's create a new directory for our Cue package:

```shell
mkdir cue.mod/kube
```

### Deploy using Kubectl

Kubernetes objects are located inside the `k8s` folder:

```shell
tree k8s
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

Next step is to transpose it in Cue. Before continuing, clean everything:

```shell
kubectl delete deploy/todoapp
# deployment.apps "todoapp" deleted

kubectl delete service/todoapp-service
# service "todoapp-service" deleted
```

## Create a basic plan

Create a file named `todoapp.cue` and add the
following configuration to it.

```cue title="todoapp/cue.mod/kube/todoapp.cue"
package main  
  
import (  
 "alpha.dagger.io/dagger"  
 "alpha.dagger.io/kubernetes"
)  

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input  
  
todoApp: kubernetes.#Resources & {  
 "kubeconfig": kubeconfig
 source: manifest  
}
```

This defines a `todoApp` variable containing the Kubernetes objects used to create a todoapp deployment.
It also references a `kubeconfig` value defined above:

<Tabs
defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'},
{label: 'GKE', value: 'gke'},
{label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

The above `config.cue` defines:

- `kubeconfig` a generic value created to embbed this string `kubeconfig` value

```cue title="todoapp/cue.mod/kube/config.cue"
package main

import (  
 "alpha.dagger.io/dagger"
)  

// set with `dagger input text kubeconfig -f ~/.kube/config -e kube`
kubeconfig: string & dagger.#Input
```

  </TabItem>

  <TabItem value="gke">

The below `config.cue` defines:

- `kubeconfig` a generic value created to embbed this `gke.#KubeConfig` value
- `gcpConfig`: connection to Google using `alpha.dagger.io/gcp`
- `gkeConfig`: transform a `gcpConfig` to a readable format for `kubernetes.#Resources.kubeconfig` using `alpha.dagger.io/gcp/gke`

```cue title="todoapp/cue.mod/kube/config.cue"
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
- `eksConfig`, transform a `awsConfig` to a readable format for `kubernetes.#Resources.kubeconfig` using `alpha.dagger.io/aws/eks`

```cue title="todoapp/cue.mod/kube/config.cue"
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
dagger new 'kube' -m cue.mod/kube
```

### Configure the environment

Before we can bring up the deployment, we need to provide the `kubeconfig` input
declared in the configuration. Otherwise, Dagger will complain about a missing input:

```shell
dagger up -e kube
# 5:05PM ERR system | required input is missing    input=kubeconfig
# 5:05PM ERR system | required input is missing    input=manifest
# 5:05PM FTL system | some required inputs are not set, please re-run with `--force` if you think it's a mistake    missing=0s
```

You can inspect the list of inputs (both required and optional) using `dagger input list`:

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
dagger input list -e kube
# Input              Value                Set by user  Description
# kubeconfig         string               false        set with `dagger input text kubeconfig -f ~/.kube/config -e kube`
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
dagger input text kubeconfig -f ~/.kube/config -e kube

# Add as artifact the k8s folder
dagger input dir manifest ./k8s -e kube
```

  </TabItem>

  <TabItem value="gke">

```shell
# Add as artifact the k8s folder
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
# Add as artifact the k8s folder
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
# deploy | #26 0.700 deployment.apps/nginx created
# deploy | completed    duration=900ms
```

Let's verify if the deployment worked:

```shell
kubectl get deployments
# NAME    READY   UP-TO-DATE   AVAILABLE   AGE
# nginx   1/1     1            1           1m
```

<!-- ## CUE Kubernetes manifests

This section will convert the inlined YAML manifest to CUE to take advantage of the language features.

For a more advanced example, see the
[official CUE Kubernetes tutorial](https://github.com/cuelang/cue/blob/v0.4.0/doc/tutorial/kubernetes/README.md)

First, let's replace `manifest.cue` with the following configuration. This is a
straightforward one-to-one conversion from YAML to CUE, only the syntax has changed.

```cue title="todoapp/cue.mod/k8s/manifest.cue"
package main

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
        "image": "nginx:1.14.2"
        ports: [{
          containerPort: "80"
        }]
      }]
    }
  }
}

manifest: yaml.Marshal(nginx)
```

We're using the built-in `yaml.Marshal` function to convert CUE back to YAML so
Kubernetes still receives the same manifest.

You need to copy the changes to the plan for dagger to reference them

```shell
cp kube/*.cue .dagger/env/kube/plan/
```

You can inspect the configuration using `dagger query -e kube` to verify it produces the
same manifest:

```shell
dagger query manifest -f text -e kube
# apiVersion: apps/v1
# kind: Deployment
...
```

Now that the manifest is defined in CUE, we can take advantage of the language
to remove a lot of boilerplate and repetition.

Let's define a re-usable `#Deployment` definition in `todoapp/cue.mod/k8s/deployment.cue"`:

```cue title="todoapp/cue.mod/k8s/deployment.cue"
package main

// Deployment template containing all the common boilerplate shared by
// deployments of this application.
#Deployment: {
  // name of the deployment. It will be used to label resources automatically
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

```cue title="todoapp/cue.mod/k8s/manifest.cue"
package main

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
dagger query deploy.manifest -f text -e kube
# apiVersion: apps/v1
# kind: Deployment
...
```

And we can now deploy it:

```shell
dagger up -e kube
# deploy | computing
# deploy | #26 0.700 deployment.apps/nginx unchanged
# deploy | completed    duration=900ms
``` -->

## Building, pushing, and deploying Docker images

Rather than deploying an existing (`todoapp`) image, we're going to build a Docker
image from the source, push it to a registry, and update the Kubernetes configuration.

### Update the plan

<Tabs
defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'},
{label: 'GKE', value: 'gke'},
{label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

Let's see how to deploy locally an image and push it to the local cluster

`kube/todoapp` faces these changes:

- `suffix`, a random string for unique tag name
- `repository`, source code of the app to build. Needs to have a Dockerfile
- `registry`, URI of the registry to push to
- `image`, build of the image
- `remoteImage`, push image to registry
- `kustomization`, apply kustomization to image

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "encoding/yaml"

  "alpha.dagger.io/dagger"
  "alpha.dagger.io/random"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
  "alpha.dagger.io/kubernetes/kustomize"
)

// Randrom string for tag
suffix: random.#String & {
  seed: ""
}

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// ECR registry to push images to
registry: string & dagger.#Input
tag:      "test-kind-\(suffix.out)"

manifest: dagger.#Artifact & dagger.#Input

// Declarative name
todoApp: {
  image: docker.#Build & {
    source: repository
  }

  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
  }

  kustomization: kustomize.#Kustomize & {
    source:        manifest
    kustomization: yaml.Marshal({
      resources: ["deployment.yaml", "service.yaml"]

      images: [{
        name:    "public.ecr.aws/j7f8d3t2/todoapp@sha256:6224c86267a798e98de9bfe5f98eaa3f55a1adfcd6757acc59e593f2ccdb37f2"
        newName: remoteImage.ref
      }]
    })
  }

  kubeSrc: kubernetes.#Resources & {
    "kubeconfig": kubeconfig
    source:     kustomization
  }
}
```

  </TabItem>

  <TabItem value="gke">

Let's see how to leverage [GCR](https://github.com/dagger/dagger/tree/main/stdlib/gcp/gcr) and [GKE](https://github.com/dagger/dagger/tree/main/stdlib/gcp/gke) packages.

The two files have to be edited in order to do so.

`kube/config.cue` configuration has following changes:

- removal of generic `kubeconfig` value as abstraction is not optimal for present use case
- definition of a new `ecrCreds` value that contains ecr credentials for remote image push to ECR

```cue title="todoapp/cue.mod/kube/config.cue"
package main
  
import (  
 "alpha.dagger.io/gcp"  
 "alpha.dagger.io/gcp/gcr"  
 "alpha.dagger.io/gcp/gke"
)

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

`kube/todoapp`, on the other hand, faces these changes:

- `suffix`, a random string for unique tag name
- `repository`, source code of the app to build. Needs to have a Dockerfile
- `registry`, URI of the registry to push to
- `image`, build of the image
- `remoteImage`, push image to registry
- `kustomization`, apply kustomization to image

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "encoding/yaml"

  "alpha.dagger.io/dagger"
  "alpha.dagger.io/random"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
  "alpha.dagger.io/kubernetes/kustomize"
)

// Randrom string for tag
suffix: random.#String & {
  seed: ""
}

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// GCP registry to push images to
registry: string & dagger.#Input
tag:      "test-gcr-\(suffix.out)"

// source of Kube config file. 
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

// Declarative name
todoApp: {
  image: docker.#Build & {
    source: repository
  }

  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
    auth: {
      username: gcrCreds.username
      secret:   gcrCreds.secret
    }
  }

  kustomization: kustomize.#Kustomize & {
    source:        manifest
    kustomization: yaml.Marshal({
      resources: ["deployment.yaml", "service.yaml"]

      images: [{
        name:    "public.ecr.aws/j7f8d3t2/todoapp@sha256:6224c86267a798e98de9bfe5f98eaa3f55a1adfcd6757acc59e593f2ccdb37f2"
        newName: remoteImage.ref
      }]
    })
  }

  kubeSrc: kubernetes.#Resources & {
    kubeconfig: gkeConfig.kubeconfig
    source:     kustomization
  }
}
```

  </TabItem>
  <TabItem value="eks">

Let's see how to leverage [ECR](https://github.com/dagger/dagger/tree/main/stdlib/aws/ecr) and [EKS](https://github.com/dagger/dagger/tree/main/stdlib/aws/eks) packages.

The two files have to be edited in order to do so.

`kube/config.cue` configuration has following changes:

- removal of generic `kubeconfig` value as abstraction is not optimal for present use case
- definition of a new `ecrCreds` value that contains ecr credentials for remote image push to ECR

```cue title="todoapp/kube/config.cue"
package main

import (
 "alpha.dagger.io/aws"
 "alpha.dagger.io/aws/eks"
 "alpha.dagger.io/aws/ecr"
)

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

`kube/todoapp`, on the other hand, faces these changes:

- `suffix`, a random string for unique tag name
- `repository`, source code of the app to build. Needs to have a Dockerfile
- `registry`, URI of the registry to push to
- `image`, build of the image
- `remoteImage`, push image to registry
- `kustomization`, apply kustomization to image

```cue title="todoapp/kube/todoapp.cue"
package main

import (
  "encoding/yaml"

  "alpha.dagger.io/dagger"
  "alpha.dagger.io/random"
  "alpha.dagger.io/docker"
  "alpha.dagger.io/kubernetes"
  "alpha.dagger.io/kubernetes/kustomize"
)

// Randrom string for tag
suffix: random.#String & {
  seed: ""
}

// input: source code repository, must contain a Dockerfile
// set with `dagger input dir repository . -e kube`
repository: dagger.#Artifact & dagger.#Input

// ECR registry to push images to
registry: string & dagger.#Input
tag:      "test-ecr-\(suffix.out)"

// source of Kube config file. 
// set with `dagger input dir manifest ./k8s -e kube`
manifest: dagger.#Artifact & dagger.#Input

// Declarative name
todoApp: {
  image: docker.#Build & {
    source: repository
  }

  remoteImage: docker.#Push & {
    target: "\(registry):\(tag)"
    source: image
    auth: {
      username: ecrCreds.username
      secret:   ecrCreds.secret
    }
  }

  kustomization: kustomize.#Kustomize & {
    source:        manifest
    kustomization: yaml.Marshal({
      resources: ["deployment.yaml", "service.yaml"]

      images: [{
        name:    "public.ecr.aws/j7f8d3t2/todoapp@sha256:6224c86267a798e98de9bfe5f98eaa3f55a1adfcd6757acc59e593f2ccdb37f2"
        newName: remoteImage.ref
      }]
    })
  }

  kubeSrc: kubernetes.#Resources & {
    kubeconfig: eksConfig.kubeconfig
    source:     kustomization
  }
}
```

  </TabItem>
</Tabs>

### Connect the Inputs

<Tabs
defaultValue="kind"
groupId="provider"
values={[
{label: 'kind', value: 'kind'},
{label: 'GKE', value: 'gke'},
{label: 'EKS', value: 'eks'},
]}>

  <TabItem value="kind">

Next, we'll provide the two new inputs, `repository` and `registry`.

```shell
# A name after `localhost:5000/` is required to avoid error on push to local registry
dagger input text registry "localhost:5000/kind" -e kube

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
  <TabItem value="gke">

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
# 4:09AM INF suffix.out | computing
# 4:09AM INF manifest | computing
# 4:09AM INF repository | computing
# ...
# 4:09AM INF todoApp.kubeSrc | #37 0.858 service/todoapp-service created
# 4:09AM INF todoApp.kubeSrc | #37 0.879 deployment.apps/todoapp created
# Output                      Value                                                                                                              Description
# suffix.out                  "azkestizysbx"                                                                                                     generated random string
# todoApp.remoteImage.ref     "localhost:5000/kind:test-kind-azkestizysbx@sha256:cb8d92518b876a3fe15a23f7c071290dfbad50283ad976f3f5b93e9f20cefee6"  Image ref
# todoApp.remoteImage.digest  "sha256:cb8d92518b876a3fe15a23f7c071290dfbad50283ad976f3f5b93e9f20cefee6"                                          Image digest
```

Let's verify if the deployment worked:

```shell
kubectl get deployments
# NAME      READY   UP-TO-DATE   AVAILABLE   AGE
# todoapp   1/1     1            1           50s
```

## Next Steps

Integrate kubernetes tools with Dagger:

- [Helm](https://github.com/dagger/dagger/tree/main/stdlib/kubernetes/helm)
- [Kustomize](https://github.com/dagger/dagger/tree/main/stdlib/kubernetes/kustomize)
