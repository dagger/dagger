---
slug: /967042/customize-engine-configuration
displayed_sidebar: 'current'
category: "guides"
tags: ["docker"]
authors: ["Tom Chauveau"]
date: "2023-09-26"
---

# Customize Dagger Engine configuration

## Introduction

This guide explains how to configure Dagger Engine through the custom `engine.toml` file.

For example, this can be used to configure a registry mirror for pulling container images instead of DockerHub.

## Examples

### Registry mirror

Create a file `engine.toml` that contains the registry mirror, e.g.:

```toml
debug = true
insecure-entitlements = ["security.insecure"]

[registry."docker.io"]
  mirrors = ["mirror.gcr.io"]
```

Manually start a Dagger Engine with this custom `engine.toml`:

```shell
# Requires Docker CLI & Daemon to be running on the same host:
docker run --rm --name customized-dagger-engine --privileged --volume $PWD/engine.toml:/etc/dagger/engine.toml registry.dagger.io/engine:v0.8.7
```

Use this customised Dagger Engine:

```shell
export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://customized-dagger-engine
dagger query --progress=plain <<< '{ container { from(address:"hello-world") { stdout } } }'
```

## Conclusion

This guide described one way to customize the Dagger Engine configuration.
Learn how to use [alternative runtimes for Dagger Engine](541047-alternative-runtimes.md), e.g. Podman, Kubernetes, etc.
