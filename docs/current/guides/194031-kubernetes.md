---
slug: /194031/kubernetes
displayed_sidebar: "current"
category: "guides"
tags: ["kubernetes", "github", "eks", "dagger-cloud"]
authors: ["Joel Longtine", "Gerhard Lazu", "Vikram Vaswani"]
date: "2023-09-22"
---

# Run Dagger on Kubernetes

## Introduction

This guide outlines how to set up a Continuous Integration (CI) environment with the Dagger ENgine on Kubernetes. It describes and explains the recommended architecture pattern and components, together with optional optimizations. It also describes a specific implementation using GitHub Actions, Amazon Elastic Kubernetes Service (EKS) and Karpenter.

## Assumptions

This guide assumes that you have:

- A good understanding of how Kubernetes works, and of key Kubernetes components and add-ons.
- A good understanding of how Dagger works. If not, [read the Dagger Quickstart](../quickstart/index.mdx).

Readers wishing to implement the recommended architecture pattern with GitHub Actions and Amazon EKS, as described in the latter section of this guide, should additionally have:

- An Amazon EKS Kubernetes cluster with [cert-manager](https://cert-manager.io/), [Karpenter](https://karpenter.sh/), [Helm](https://helm.sh) and [GitHub Actions Runner Controller (ARC)](https://github.com/actions/actions-runner-controller) installed.
- A GitHub account. If not, [sign up for a free GitHub account](https://github.com/signup).
- A GitHub repository with a GitHub Actions workflow incorporating one or more Dagger pipelines. If not, [learn about GitHub Actions](https://docs.github.com/en/actions/learn-github-actions).
- A Dagger Cloud account. If not, [sign up for Dagger Cloud](https://dagger.io/pricing).

## Architecture Patterns

### Base pattern

The base pattern consists of persistent Kubernetes nodes with ephemeral CI runners

The minimum required components are:

- A Kubernetes cluster, consisting of support nodes and runner nodes.
  - Runner nodes host CI runners and Dagger Engines
  - Support nodes host support and management tools, such as for cluster certificate management, node auto-provisioning, proxying and other functions.
- A runner controller, which is responsible for creating CI runners on runner nodes to handle incoming job requests (and destroying them on job completion).
  - Runners are the workhorses of a CI/CD system. They execute the jobs that are defined in the CI/CD pipeline.
- A Dagger Engine on each runner node, running alongside one or more CI runners. The Dagger Engine is responsible for running Dagger pipelines and caching intermediate and final build artifacts.

In this architecture:

- Kubernetes Nodes are persistent, CI runners are ephemeral.
- Each CI runner has access only to the cache of the local Dagger Engine.
- The Dagger Engine is deployed as a DaemonSet, to use resources in the most efficient manner and enable reuse of the local Dagger Engine cache to the greatest extent possible.

![Kubernetes base architecture](/img/current/guides/kubernetes/pattern-base.png)

### Addition: Ephemeral, auto-scaled nodes

The base architecture pattern described previously can be optimized by the addition of a node auto-scaler. A node auto-scaler can automatically adjust the size of node groups based on the current workload. If there are a lot of CI jobs running, the auto-scaler can automatically add more runner nodes to the cluster to handle the increased workload. Conversely, if there are few jobs running, it can remove unnecessary runner nodes, reducing the total compute cost.

In this architecture:

- Kubernetes nodes provisioned on-demand start with a "clean" Dagger Engine containing no cached data.
- Cached build artifacts from subsequent runs will persist only for the lifetime of the runner node.

![Kubernetes architecture with ephmeral nodes](/img/current/guides/kubernetes/pattern-ephemeral.png)

### Addition: Shared cloud cache

The previous pattern makes it possible to scale the Dagger deployment, but comes with two problems:

- Runner nodes are automatically de-provisioned when they are not needed. During de-provisioning, the Dagger Engines get deleted too. As a result, data and operations cached in previous runs will be deleted and subsequent runs will not benefit from previous runs. To resolve this, the cached data and operations are stored in a cloud caching service and made available to new Dagger Engines when they are provisioned.
- The deployment will only scale to a certain point, given that each Dagger Engine can only scale vertically to provide better performance. In order to make the system horizontally scalable, a caching service orchestrates a pool of Dagger Engines so that jobs can be scheduled independently across them.

In this architecture:

- A shared cloud cache stores data from all Dagger Engines running in the cluster.
- Auto-provisioned nodes start with access to cached data of previous runs.

![Kubernetes architecture with shared cache](/img/current/guides/kubernetes/pattern-cache.png)

## Example: Dagger on Amazon EKS with GitHub Actions and Karpenter

This section explains how to implement the base pattern and improvements described above using GitHub Actions, Amazon Elastic Kubernetes Service (EKS), Karpenter and Dagger Cloud.

:::note
These steps may vary depending on your specific setup and requirements. Always refer to the official documentation of the tools and services listed for the most accurate and up-to-date information.
:::

### Step 1: Understand the architecture

![Kubernetes implementation](/img/current/guides/kubernetes/implementation.png)

Here is a brief description of the architecture and components:

- The application source code is hosted in a GitHub repository.
- The runner nodes are part of an Amazon EKS cluster.
- GitHub provides the [GitHub Actions Runner Controller (ARC)](https://docs.github.com/en/actions/hosting-your-own-runners/managing-self-hosted-runners-with-actions-runner-controller), a Kubernetes controller that manages deploying and scaling GitHub Actions runners as Pods in a Kubernetes cluster.
- A Dagger Engine runs on every Kubernetes node where a GitHub Actions Runner is deployed.
- Based on GitHub Actions jobs queued, ARC creates on-demand runners.
- Dagger Engines communicate with the Dagger Cloud to read from, and write to, the shared cache.
- Karpenter, a Kubernetes-native node auto-scaler, uses the AWS EKS API to dynamically add or remove runner nodes depending on workload requirements.

### Step 2: Set up services and components

The next step is to set up the required services and components. Ensure that you have:

- An Amazon EKS Kubernetes cluster with [cert-manager](https://cert-manager.io/), [Karpenter](https://karpenter.sh/), [Helm](https://helm.sh) and [GitHub Actions Runner Controller (ARC)](https://github.com/actions/actions-runner-controller) installed.
- A GitHub account and a GitHub repository with your application source code.
- A GitHub Actions workflow for your repository incorporating one or more Dagger pipelines.
- A [Dagger Cloud account](https://www.dagger.io/pricing), which provides distributed caching, pipeline visibility, and operational insights.

### Step 3: Create a set of taints and tolerations for the GitHub Actions runners

[Taints and tolerations](https://kubernetes.io/docs/concepts/scheduling-eviction/taint-and-toleration/) in Kubernetes are a way to ensure that certain nodes are reserved for specific tasks. By setting up taints on runner nodes, you can prevent other workloads from being scheduled on them. Tolerations are then added to the runners so that they can be scheduled on these tainted nodes. This ensures that the runners have dedicated resources for their tasks.

A sample deployment configuration is shown below. Replace the `YOUR_GITHUB_ORGANIZATION` placeholder with your GitHub organization name.

```yaml
apiVersion: actions.summerwind.dev/v1alpha1
kind: RunnerDeployment
metadata:
  name: dagger-runner-deployment-2c-8g
  namespace: dagger
spec:
  replicas: 0
  template:
    spec:
      affinity:
        nodeAffinity:
          requiredDuringSchedulingIgnoredDuringExecution:
            nodeSelectorTerms:
            - matchExpressions:
              - key: actions-runner
                operator: Exists
      dockerEnabled: true
      dockerdContainerResources: {}
      dockerdWithinRunnerContainer: true
      image: summerwind/actions-runner-dind
      labels:
      - dagger-runner
      - dagger-runner-2c-8g
      organization: YOUR_GITHUB_ORGANIZATION
      resources:
        requests:
          cpu: "2"
          memory: 8Gi
      securityContext:
        fsGroup: 1001
        fsGroupChangePolicy: OnRootMismatch
      tolerations:
      - effect: NoSchedule
        key: actions-runner
        operator: Exists
	env:
- name: _EXPERIMENTAL_DAGGER_RUNNER_HOST
  value: unix:///var/run/dagger/buildkitd.sock
      volumeMounts:
      - mountPath: /var/run/dagger
        name: dagger-socket
      volumes:
      - hostPath:
          path: /var/run/dagger
        name: dagger-socket
```

### Step 4: Deploy the Dagger Engine DaemonSet with Helm

A Dagger Engine is required on each of the GitHub Actions runner nodes. A DaemonSet ensures that all matching nodes run an instance of Dagger Engine. To ensure that the Dagger Engines are co-located with the GitHub Actions runners, the Dagger Engine Daemonset should be configured with the same taints and tolerations as the GitHub Actions runners.

Use our Helm chart to create the Dagger Engine DaemonSet on the cluster:

```shell
helm upgrade --create-namespace --install --namespace dagger dagger oci://registry.dagger.io/dagger-helm
```

This Dagger Engine DaemonSet configuration is designed to:

- best utilize the NVMe hard drives of the worker nodes;
- reduce the amount of network latency and bandwidth requirements;
- simplify routing of Dagger SDK and CLI requests.

:::note
This configuration uses the `_EXPERIMENTAL_DAGGER_RUNNER_HOST` environment variable to point to the Dagger Engine DaemonSet socket that is mounted into the GitHub Actions runners. This ensures that the runners will use the local Dagger Engine for pipeline execution.
:::

### Step 5: Test the deployment

At this point, the deployment is configured and ready for use. Test it by triggering the GitHub Actions workflow, by committing a new change to the source code repository. Your CI pipelines will be now connected to your Dagger Engines.

:::tip
To validate that your Dagger Engines are working as expected, you can check your Dagger Cloud dashboard, which shows detailed information about your pipeline executions.
:::

### Recommendations

When deploying Dagger on a Kubernetes cluster, it's important to understand the design constraints you're operating under, so you can optimize your configuration to suit your workload requirements. Here are two key recommendations:

- Choose nodes with moderate to large NVMe drives for the Dagger Engine cache. The Dagger Engine cache is used to store intermediate build artifacts, which can significantly speed up your CI jobs. However, this cache can grow very large over time. By choosing nodes with large NVMe drives, you ensure that there is plenty of space for this cache. NVMe drives are also much faster than traditional SSDs, which can further improve performance. These types of drives are usually ephemeral to the node and much less expensive relative to EBS-type volumes.

- Choose an appropriate size for your runner nodes based on the types of jobs you expect to run. Although this will obviously vary based on workloads, a minimum of 2 vCPUs and 8GB of RAM is a good place to start. One approach is to set up the GitHub Actions runners with various sizes so that the Dagger Engine can consume resources from the runners on the same node if needed.

### Conclusion

This guide described how to set up a Continuous Integration (CI) environment using Dagger on Kubernetes. It described the recommended architecture pattern and then described a specific implementation of that pattern using GitHub Actions, Amazon Elastic Kubernetes Service (EKS), Karpenter and Dagger Cloud.

Use the following resources to learn more about the topics discussed in this guide:

- [Dagger Cloud](https://docs.dagger.io/cloud)
- [Dagger GraphQL API](https://docs.dagger.io/api/975146/concepts)
- Dagger [Go](https://docs.dagger.io/sdk/go), [Node.js](https://docs.dagger.io/sdk/nodejs) and [Python](https://docs.dagger.io/sdk/python) SDKs

If you need help troubleshooting your Dagger deployment on Kubernetes, let us know in [Discord](https://discord.com/invite/dagger-io) or create a [GitHub issue](https://github.com/dagger/dagger/issues/new/choose).
