---
slug: /194031/kubernetes
displayed_sidebar: "current"
category: "guides"
tags: ["kubernetes"]
authors: ["Gerhard Lazu", "Vikram Vaswani"]
date: "2023-11-30"
---

# Run Dagger on Kubernetes

## Introduction

This guide outlines how to run, and connect to, the Dagger Engine on Kubernetes.

## Assumptions

This guide assumes that you have:

- A good understanding of how Kubernetes works, and of key Kubernetes components and add-ons.
- A good understanding of how Dagger works. If not, [read the Dagger Quickstart](../quickstart/index.mdx).
- A Kubernetes cluster with [Helm](https://helm.sh) 3.x installed.

## Step 1: Deploy a Dagger Engine DaemonSet with Helm

Create a Dagger Engine DaemonSet on the cluster with our Helm chart:

```shell
helm upgrade --create-namespace --install --namespace dagger dagger oci://registry.dagger.io/dagger-helm
```

## Step 2:

TODO with Gerhard

## Conclusion

TODO

:::info
If you need help troubleshooting your Dagger deployment on Kubernetes, let us know in [Discord](https://discord.com/invite/dagger-io) or create a [GitHub issue](https://github.com/dagger/dagger/issues/new/choose).
:::
