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

Wait for the pod to become ready:

```shell
kubectl wait --for condition=Ready --timeout=60s pod --selector=name=dagger-dagger-helm-engine --namespace=
dagger
```

Obtain the name of the pod:

```shell
DAGGER_POD_NAME=`kubectl get pods -n dagger -o=jsonpath='{range .items..metadata}{.name}{"\n"}{end}' | grep dagger-dagger-helm-engine`
```

## Step 2: Connect the Dagger CLI with the Dagger Engine Daemonset

Next, set the `_EXPERIMENTAL_DAGGER_RUNNER_HOST` variable to tell the Dagger CLI where to look for the Dagger Engine, and then use the Dagger CLI as usual. Here's an example:

```shell
_EXPERIMENTAL_DAGGER_RUNNER_HOST="kube-pod://$DAGGER_POD_NAME?namespace=dagger" dagger query <<EOF | jq -r .container.from.withExec.stdout
{
  container {
    from(address:"alpine:latest") {
      withExec(args:["uname", "-nrio"]) {
        stdout
      }
    }
  }
}
EOF
```

You can confirm that the operations are running on the Kubernetes cluster by watching the pod logs in a separate terminal:

```shell
kubectl logs $DAGGER_POD_NAME -n dagger
```

## Conclusion

This guide demonstrated a very simplistic approach to using the Dagger Engine on Kubernetes. For more complex scenarios, such as setting up a Continuous Integration (CI) environment with the Dagger Engine on Kubernetes, use the following resources:

- Understand [CI architecture patterns on Kubernetes with Dagger](./237420-ci-architecture-kubernetes.md)
- See an example of [running Dagger on Amazon EKS with GitHub Actions Runner and Karpenter](./934191-eks-github-karpenter.md)
- [Dagger Cloud](https://docs.dagger.io/cloud)
- [Dagger GraphQL API](https://docs.dagger.io/api/975146/concepts)
- Dagger [Go](https://docs.dagger.io/sdk/go), [Node.js](https://docs.dagger.io/sdk/nodejs) and [Python](https://docs.dagger.io/sdk/python) SDKs

:::info
If you need help troubleshooting your Dagger deployment on Kubernetes, let us know in [Discord](https://discord.com/invite/dagger-io) or create a [GitHub issue](https://github.com/dagger/dagger/issues/new/choose).
:::
