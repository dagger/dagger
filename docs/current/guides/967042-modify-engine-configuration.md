---
slug: /967042/modify-engine-configuration
displayed_sidebar: 'current'
category: "guides"
tags: ["docker"]
authors: ["Tom Chauveau"]
date: "2023-09-26"
---

# Modify the Dagger Engine's Configuration

## Introduction

This guide explains how to specify a custom configuration for the Dagger Engine using the `engine.toml` file. As an example, it demonstrates how to configure the Dagger Engine to use a different registry mirror for container images instead of the default (Docker Hub).

## Requirements

This guide assumes that:

- You have Docker installed and running on the host system. If not, [install Docker](https://docs.docker.com/engine/install/).
- You have the Dagger CLI installed in your development environment. If not, [install the Dagger CLI](../cli/465058-install.md).

## Example: Use a custom registry

1. Create a file named `engine.toml` that contains the registry mirror:

  ```toml
  debug = true
  insecure-entitlements = ["security.insecure"]

  [registry."docker.io"]
    mirrors = ["mirror.gcr.io"]
  ```

1. Manually start a Dagger Engine with the custom `engine.toml`:

  ```shell
  docker run --rm --name customized-dagger-engine --privileged --volume $PWD/engine.toml:/etc/dagger/engine.toml registry.dagger.io/engine:v0.8.7
  ```

1. Test the configuration:

  ```shell
  export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://customized-dagger-engine
  dagger query --progress=plain <<< '{ container { from(address:"hello-world") { stdout } } }'
  ```

  You should see the specified `hello-world` container being pulled from the mirror instead of from Docker Hub.

:::tip
[See all Dagger Engine configuration options](https://docs.docker.com/build/buildkit/toml-configuration/).
:::

## Conclusion

This guide described how to customize the Dagger Engine's configuration. Use the [API Key Concepts](../api/975146-concepts.mdx) page and the [Go](https://pkg.go.dev/dagger.io/dagger), [Node.js](../sdk/nodejs/reference/modules.md) and [Python](https://dagger-io.readthedocs.org/) SDK References to learn more about Dagger. You can also learn how to [use alternative runtimes for the Dagger Engine](541047-alternative-runtimes.md).
