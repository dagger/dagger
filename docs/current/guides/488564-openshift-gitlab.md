---
slug: /488564/openshift-gitlab
displayed_sidebar: "current"
category: "guides"
tags: ["kubernetes", "openshift", "gitlab", "dagger-cloud"]
authors: ["Christian Schlatter"]
date: "2023-10-27"
---

# Run Dagger on OpenShift with GitLab Runners

## Introduction

This guide outlines how to set up a Continuous Integration (CI) environment with the Dagger Engine on OpenShift in combination with GitLab Runners.

## Assumptions

This guide assumes that you have:

- A good understanding of how OpenShift works, and of key Kubernetes components and add-ons.
- A good understanding of how Dagger works. If not, [read the Dagger Quickstart](../quickstart/index.mdx).
- A functional OpenShift cluster with the [GitLab Runner Operator](https://docs.gitlab.com/runner/install/operator.html) installed.
- Helm v3.x installed on your local machine to deploy the Dagger Engine on Kubernetes. If not, [install Helm](https://helm.sh/docs/intro/install/).
- The OpenShift CLI (`oc`) installed on your local machine to communicate with the OpenShift cluster. If not, [install the OpenShift CLI](https://docs.openshift.com/container-platform/4.13/cli_reference/openshift_cli/getting-started-cli.html).
- A GitLab account or self-hosted GitLab instance. If not, [sign up for a free GitLab account](https://gitlab.com/signup).

## Step 1: Install the Dagger Engine

Follow the steps below:

1. Create a `values.yaml` file to configure the Dagger Helm deployment. This includes a set of labels for the pod affinity and the taints and tolerations for the nodes.

  ```yaml file=./snippets/openshift-gitlab/values.yaml
  ```

  This configuration uses the label `builder-node=true` to taint the nodes on which the Dagger Engine should be deployed.

1. Execute the following command for each node that is intended to host a Dagger Engine (replace the `NODE-NAME` placeholder with each node name):

  ```shell
  oc adm taint nodes NODE-NAME builder-node=true:NoSchedule
  ```

1. Install the Dagger Engine using the Dagger Helm chart:

  ```shell
  helm upgrade --create-namespace --install --namespace dagger dagger oci://registry.dagger.io/dagger-helm -f values.yaml
  ```

1. Grant the necessary permissions for the `default` service account in the `dagger` namespace.

  :::info
  Without this step, pod creation will fail due to insufficient permissions to execute privileged containers with fixed user IDs and host path volume mounts.
  :::

  ```shell
  oc adm policy add-scc-to-user privileged -z default -n dagger
  ```

## Step 2: Configure GitLab Runners

The next step is to configure a GitLab Runner. Follow these steps:

1. Create the configuration for the GitLab Runner as `runner-config.yaml` and `runner.yaml`. Replace the `YOUR-GITLAB-URL` placeholder with the URL of your GitLab instance.

  ```yaml title=runner-config.yaml file=./snippets/openshift-gitlab/runner-config.yaml
  ```

  ```yaml title=runner.yaml file=./snippets/openshift-gitlab/runner.yaml
  ```

  This configuration uses a similar configuration as that seen in Step 1 for the taints and tolerations and the pod affinity. This ensures that the GitLab builder pod only runs on nodes with Dagger Engines.

1. Apply the configuration and deploy the GitLab Runner:

  ```shell
  oc apply -f runner-config.yaml -n dagger
  oc apply -f runner.yaml -n dagger
  ```

## Step 3: Create a GitLab CI/CD pipeline

Create a new GitLab CI/CD pipeline configuration file in your repository at `.gitlab-ci.yml` with the following content:

```yaml title=.gitlab-ci.yml file=./snippets/openshift-gitlab/.gitlab-ci.yml
```

The most important section of this configuration are:

- The `tags` entry, which tells GitLab to use the GitLab Runner which is connected to the Dagger Engine; and
- The `_EXPERIMENTAL_DAGGER_RUNNER_HOST` variable, which specifies the socket for the Dagger CLI to use.

## Step 4: Run a GitLab CI job

TODO: Explain how to initiate a job, where to find the job log, etc

## Conclusion

Use the following resources to learn more about the topics discussed in this guide:

- [Dagger on Kubernetes](./194031-kubernetes.md) guide
- [Dagger GraphQL API](https://docs.dagger.io/api/975146/concepts)
- Dagger [Go](https://docs.dagger.io/sdk/go), [Node.js](https://docs.dagger.io/sdk/nodejs) and [Python](https://docs.dagger.io/sdk/python) SDKs
