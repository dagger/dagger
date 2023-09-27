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

## Requirements

This guide assumes that you have a basic understanding of Kubernetes and Argo Workflows, and that your Kubernetes cluster has been configured following the [Run Dagger on Kubernetes](https://docs.dagger.io/194031/kubernetes) guide.

## Step 1: Install Argo Workflows

The first step is to install [Argo Workflows](https://argoproj.github.io/argo-workflows/) in the Kubernetes cluster.

Follow the [Argo Workflows quickstart](https://github.com/argoproj/argo-workflows/blob/master/docs/quick-start.md) steps, adjusting them as needed to your own requirements. Once you've successfully installed Argo Workflows in your cluster, continue to the next step.

## Step 2: Run a sample workflow

The sample workflow will clone and run the CI for the [greetings-api](https://github.com/kpenfound/greetings-api) demo project. This project uses the Dagger Go SDK for CI.

Create a file called `workflow.yaml` with the following content:

```yaml file=./snippets/argo-workflows/workflow.yaml
```

A few important points to note:

- The workflow uses hardwired artifacts to clone the Git repository and to install the Dagger CLI.
- `unix:///var/run/dagger/buildkitd.sock` is mounted and specified with the `_EXPERIMENTAL_DAGGER_RUNNER_HOST` environment variable.
- The Dagger CLI `dagger_v0.8.7_linux_amd64.tar.gz` is downloaded and installed. Confirm the version and architecture are accurate for your cluster and project.
- The image `golang:1.21.0-bookworm` is used as the runtime for the pipeline because the example project requires Go.

The workflow uses a PersistentVolumeClaim for the runtime dependencies of the pipeline, such as the Dagger Go SDK.

Create the PersistentVolumeClaim configuration in a file called `gomodcache.yaml`:

```yaml file=./snippets/argo-workflows/gomodcache.yaml
```

Apply the configuration:

 `kubectl apply -n argo -f ./gomodcache.yaml`

When you're satisfied with the workflow configuration, run it with Argo:

`argo submit -n argo --watch ./workflow.yaml`

The `--watch` argument provides an ongoing status feed of the workflow request in Argo. To see the logs from your workflow, note the pod name and in another terminal run `kubectl logs -f POD_NAME`

Once the workflow has successfully completed, run it again with `argo submit -n argo --watch ./workflow.yaml`. Dagger's caching should result in a significantly faster second execution.

## Conclusion

This example demonstrated how to integrate Dagger with Argo Workflows. However, this is a basic example and it's likely that you will want to also integrate Argo Workflows and Argo Events into your CI/CD pipeline. These topics are outside the scope of this guide, but you can find numerous third-party tutorials on these topics, such as this [guide on implementing CI/CD pipeline using Argo Workflows and Argo Events](https://medium.com/atlantbh/implementing-ci-cd-pipeline-using-argo-workflows-and-argo-events-6417dd157566).

To learn more about Dagger, use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References. For more information on Argo Workflows, refer to the [official documentation](https://argoproj.github.io/argo-workflows/#documentation).
