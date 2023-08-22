---
slug: /324301/argo-workflows
displayed_sidebar: "current"
category: "guides"
tags: ["python", "go", "nodejs", "argo", "kubernetes"]
authors: ["Kyle Penfound"]
date: "2023-08-22"
---

import Tabs from '@theme/Tabs';
import TabItem from '@theme/TabItem';

# Use Dagger with Argo Workflows

## Introduction

This guide explains how to run Dagger pipelines in Argo Workflows. According to their [site](https://argoproj.github.io/argo-workflows/), Argo Workflows is an open source container-native workflow engine for orchestrating parallel jobs on Kubernetes.

## Configuring Kubernetes

In this guide, Argo Workflows will be running on a [kind](https://kind.sigs.k8s.io/) cluster. If you already have a kubernetes cluster to use, skip ahead to the next section.

Install kind following the [kind quickstart guide](https://kind.sigs.k8s.io/docs/user/quick-start/). If you use brew, that looks like `brew install kind`.

Next, create a configuration to use to initiate the kind cluster. Here's an example configuration to use:

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

Now that you have a configuration file for kind, you can initiate the cluster:

`kind create cluster --name argo --config ~/.kube/kind-config.yaml`

## Install Argo Workflows

Next up you need to install [Argo Workflows](https://argoproj.github.io/argo-workflows/) in the Kubernetes cluster.

This guide will follow the [quickstart](https://github.com/argoproj/argo-workflows/blob/master/docs/quick-start.md) installation, however your own deployment may have different requirements. Once you've successfully installed argo workflows to your cluster, continue to the next step.

## Configure the Dagger Daemonset

Next, you need a Dagger Engine running as a Daemonset in your Kubernetes cluster. There are many benefits to running the engine as a Daemonset, but most importantly it means an engine will always be available when you need it and it will be able to persist cache between pipeline executions.

Clone the [demo repo](https://github.com/kpenfound/dagger-argo-workflows).

`git clone https://github.com/kpenfound/dagger-argo-workflows.git`

