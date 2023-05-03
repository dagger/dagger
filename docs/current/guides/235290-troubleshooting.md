---
slug: /235290/troubleshooting
displayed_sidebar: 'current'
category: "guides"
tags: []
authors: ["Vikram Vaswani"]
date: "2023-04-27"
---

import Tabs from "@theme/Tabs";
import TabItem from "@theme/TabItem";

# Troubleshooting Dagger

This page describes problems you may encounter when using Dagger, and their solutions.

## Dagger pipeline is unresponsive with a BuildKit error

A Dagger pipeline may hang or become unresponsive, eventually generating a BuildKit error such as `buildkit failed to respond` or `container state improper`.

To resolve this error, you must stop and remove the Dagger Engine container and (optionally) clear the container state.

1. Stop and remove the Dagger Engine container:

  ```shell
  DAGGER_ENGINE_DOCKER_CONTAINER="$(docker container list --all --filter 'name=^dagger-engine-*' --format '{{.Names}}')"
  docker container stop "$DAGGER_ENGINE_DOCKER_CONTAINER"
  docker container rm "$DAGGER_ENGINE_DOCKER_CONTAINER"
  ```

1. Clear unused volumes and data:

  :::info
  This step is optional. It will remove the cache and result in a slow first run when the container is re-provisioned.
  :::

  ```shell
  docker volume prune
  docker system prune
  ```

You should now be able to re-run your Dagger pipeline successfully.

:::tip
If you have custom-provisioned the Dagger Engine, please adjust the above commands to your environment.
:::