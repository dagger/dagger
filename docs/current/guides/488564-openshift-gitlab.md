---
slug: /488564/openshift-gitlab
displayed_sidebar: "current"
category: "guides"
tags: ["kubernetes", "openshift", "gitlab", "dagger-cloud"]
authors: ["Christian Schlatter"]
date: "2023-09-22"
---

# Run Dagger on OpenShift with GitLab Runners

## Introduction

This guide outlines how to set up a Continuous Integration (CI) environment with the Dagger Engine on OpenShift in combination with GitLab runners.

## Assumptions

This guide assumes that you have:

- A good understanding of how OpenShift works, and of key Kubernetes components and add-ons.
- A good understanding of how Dagger works. If not, [read the Dagger Quickstart](../quickstart/index.mdx).
- Helm v3 installed on your local machine to deploy the Dagger engine
- OC CLI installed on your local machine to communicate with the OpenShift cluster

Readers wishing to implement the recommended architecture pattern with GitLab CI Runners, as described in the later section of this guide, should additionally have:

- An functional OpenShift cluster with [Gitlab Runner Operator](https://docs.gitlab.com/runner/install/operator.html) installed.
- A GitLab account or self hosted instance. If not, [sign up for a free GitLab account](https://gitlab.com/signup).



## Install Dagger engine


First create a `values.yaml` to configure our Dagger Helm deployment.
Then create a set of labels for the pod affinity and the taints & tolerations.
In this example we choose the label `builder-node=true` to taint the nodes, on which the Dagger engine should be deployed on.   

```yaml file=./snippets/openshift-gitlab/values.yaml
```

Before deploy the engine, execute following command for each node you want to run an engine on it.

```bash
oc adm taint nodes <node_name> builder-node=true:NoSchedule
```

Next execute the Helm upgrade command to install the Dagger engine

```bash
helm upgrade --create-namespace --install --namespace dagger dagger oci://registry.dagger.io/dagger-helm -f values.yaml
```

Next grant the permissions for to the `default` Service Account in the dagger Namespace.
This is necessary, otherwise OpenShift will prevent the Pod from creating due the lack of permissions to execute privileged containers with fixed user IDs and host path volume mounts.

```bash
oc adm policy add-scc-to-user privileged -z default -n dagger
```

## Setup a GitLab Runner

If you are not familliar with configuring GitLab Runners, then please read the documentation about how to [Configuring GitLab Runner on OpenShift](https://docs.gitlab.com/runner/configuration/configuring_runner_operator.html) first.

First create the configuration for the Dagger GitLab Runner. Make sure to replace `<your_gitlab_url>` with your GitLab url (either with your self hosted instance or gitlab.com).
In this file is also a matching configuration for the taint & tolerations and the pod affinity. This ensures that the GitLab builder pod is running on nodes with a dagger eninge on it only.

```yaml file=./snippets/openshift-gitlab/runner-config.yaml
```

Apply the the ConfigMap with following command:

```bash
oc apply -f runner-config.yaml -n dagger
```


```yaml file=./snippets/openshift-gitlab/runner.yaml
```

Then deploy the GitLab Runner itself. The Runner will take the ConfigMap with the name `dagger-custom-config-toml` for its configuration.

```bash
oc apply -f runner.yaml -n dagger
```


### Run a GitLab CI job 

The last step is to configure an `.gitlab-ci.yml` file which makes use of the deployed Dagger engine.
The most important parts in this file are:
* `tags: [dagger]` this tells GitLab to use the GitLab Runner which is connectec to th Dagger engine
* `"_EXPERIMENTAL_DAGGER_RUNNER_HOST": "unix:///var/run/dagger/buildkitd.sock"` in the variable section. With this env var the Dagger CLI will connect to the socket, which connects the GitLab runner to the Dagger engine. 

```yaml file=./snippets/openshift-gitlab/.gitlab-ci.yml
```



Use the following resources to learn more about the topics discussed in this guide:

- [Dagger Cloud](https://docs.dagger.io/cloud)
- [Dagger GraphQL API](https://docs.dagger.io/api/975146/concepts)
- Dagger [Go](https://docs.dagger.io/sdk/go), [Node.js](https://docs.dagger.io/sdk/nodejs) and [Python](https://docs.dagger.io/sdk/python) SDKs

:::info
If you need help troubleshooting your Dagger deployment on Kubernetes, let us know in [Discord](https://discord.com/invite/dagger-io) or create a [GitHub issue](https://github.com/dagger/dagger/issues/new/choose).
:::
 