---
slug: /324301/argo-workflows
displayed_sidebar: "current"
category: "guides"
tags: ["argo", "kubernetes"]
authors: ["Kyle Penfound"]
date: "2023-08-22"
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Use Dagger with Argo Workflows

## Introduction

[Argo Workflows](https://argoproj.github.io/argo-workflows/) is an open source container-native workflow engine for orchestrating parallel jobs on Kubernetes. This guide explains how to run Dagger pipelines in Argo Workflows. 

## Step 1: Create a Kubernetes cluster

In this guide, Argo Workflows will be running on a [kind](https://kind.sigs.k8s.io/) cluster. If you already have a kubernetes cluster to use, skip ahead to the next section.

Install kind following the [kind quickstart guide](https://kind.sigs.k8s.io/docs/user/quick-start/). 

Next, create a configuration to initiate the kind cluster. Here's an example configuration to use:

`~/.kube/kind-config.yaml`

```yaml
# 2 node (one masters & one worker) cluster config
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
nodes:
- role: control-plane
- role: worker
  extraPortMappings:
  - containerPort: 80
    hostPort: 80
    listenAddress: "0.0.0.0"
  - containerPort: 443
    hostPort: 443
    listenAddress: "0.0.0.0"
```

Initiate the cluster:

`kind create cluster --name argo --config ~/.kube/kind-config.yaml`

## Step 2: Install Argo Workflows

The next step is to install [Argo Workflows](https://argoproj.github.io/argo-workflows/) in the Kubernetes cluster.

Follow the [Argo Workflows quickstart](https://github.com/argoproj/argo-workflows/blob/master/docs/quick-start.md) steps, adjusting them as needed to your own requirements. Once you've successfully installed Argo Workflows to your cluster, continue to the next step.

## Step 3: Configure the Dagger Daemonset

The next step is to configure a Dagger Engine running as a Daemonset in your Kubernetes cluster. There are many benefits to running the Dagger Engine as a Daemonset, but the most important one is that a Dagger Engine will always be available when you need it and that it will be able to persist cache between pipeline executions.

:::danger
This daemonset configuration is unofficial and should be modified to fit your use case
:::

Create a file called `daemonset.yaml` with the following content:

```yaml file=./snippets/argo-workflows/daemonset.yaml
```

- `registry.dagger.io/engine:{VERSION}` is specified twice. Ensure it has the appropriate version for your use case.
- `buildkit-v0.12.1.linux-amd64.tar.gz` is downloaded to use for the readinessProbe. If you're running on arm64, be sure to change this to `buildkit-v0.12.1.linux-arm64.tar.gz`.
- `/var/run/buildkit` is configured as a volume mount. This is how the workflow pods communicate with the engine.

When you're satisfied with the configuration, apply the daemonset

`kubectl apply -f ./daemonset.yaml`

## Run a sample workflow

The sample workflow will clone and run the CI for the [greetings-api](https://github.com/kpenfound/greetings-api) demo project. This project uses the Dagger Go SDK for CI.

Create a file called `workflow.yaml` with the following content:

```yaml file=./snippets/argo-workflows/workflow.yaml
```

- The workflow uses hardwired artifacts to clone the git repository and to install the Dagger CLI
- `/var/run/buildkit` is mounted and specified with the `_EXPERIMENTAL_DAGGER_RUNNER_HOST` environment variable
- The Dagger CLI `dagger_v0.8.4_linux_amd64.tar.gz` is downloaded and installed. Confirm the version and architecture are accurate for your cluster and project
- The image `golang:1.21.0-bookworm` is used as the runtime for the container because the example project requires Go
- The environment variable `_EXPERIMENTAL_DAGGER_CLOUD_TOKEN` is set from the kubernetes secret `dagger-cloud.token`. If you have a dagger cloud token, set this as a secret with `kubectl create secret generic dagger-cloud --from-literal=token={YOUR_TOKEN}`. If not, remove this variable.

The workflow uses a persistentVolumeClaim for the runtime dependencies of the pipeline. Even though the dependencies within the pipeline are cached through Dagger, we still have dependencies to run the pipeline. Create the persistentVolumeClaim configuration in a file called gomodcache.yaml:

```yaml file=./snippets/argo-workflows/gomodcache.yaml
```

and apply the persistentVolumeClaim:

 `kubectl apply -f ./gomodcache.yaml`

When you're satisfied with the workflow configuration, run it with Argo:

`argo submit -n argo --watch ./workflow.yaml`

The `--watch` argument provides an ongoing status feed of the workflow request in Argo. To see the logs from your workflow, note the pod name and in another terminal run `kubectl logs -f POD_NAME`

Once the workflow has successfully completed, run it again with `argo submit -n argo --watch ./workflow.yaml` and notice the caching this time!

## Conclusion

This example demonstrated a very basic way to run Dagger with Argo Workflows. In real world environments, Argo Workflows is often coupled with ArgoCD and Argo Events to create a fully featured CI/CD platform. Read more about that [here](https://medium.com/atlantbh/implementing-ci-cd-pipeline-using-argo-workflows-and-argo-events-6417dd157566).
