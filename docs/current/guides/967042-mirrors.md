---
slug: /967042/use-mirrors
displayed_sidebar: 'current'
category: "guides"
tags: ["docker"]
authors: ["Tom Chauveau"]
date: "2023-09-26"
---

# Customize Dagger Engine configuration

## Introduction

This guide explain how to configure Dagger Engine with a custom configuration file.

For example, this can be used to configure a mirror to pull docker image from it instead of docker hub.

## Approches

Dagger Engine is a custom configured Buildkit Engine that you can configure through the `engine.toml` file to [use a mirror](https://docs.docker.com/build/buildkit/configure/#registry-mirror).

### Configure a Dagger Engine to pull from a mirror

You can override the buildkit config and replace it with your own that include a mirror in it.

Create a file `engine.toml` with the configured mirror.

```toml
debug = true
insecure-entitlements = ["security.insecure"]

[registry."docker.io"]
  mirrors = ["mirror.gcr.io"]
```

:::tip
This file contains the minimum configure to run Dagger with a mirror, you can add extra configuration depending on your needs.
:::

Manually start a dagger engine and bind your configuration to use the mirror.

```shell
docker run --name dagger-engine --privileged -v ./engine.toml:/etc/dagger/engine.toml registry.dagger.io/engine:v0.8.7

# Export runner host to use it in further dagger command
export _EXPERIMENTAL_DAGGER_RUNNER_HOST=docker-container://dagger-engine
```

## Conclusion

This guide described on way to configure the Dagger Engine with a custom file.

To discover more about possible customization around the dagger engine, you can check [how to use an alternative runtime to run Dagger](541047-alternative-runtimes.md).
