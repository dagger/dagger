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

To resolve this error, you must stop and remove the Dagger Engine container and clear unused volumes and the cache.

1. Stop and remove the Dagger Engine container:

  ```shell
  DAGGER_CONTAINER_NETWORK_NAME=`docker ps --filter "name=^dagger-engine-*" --format '{{.Names}}'`
  docker stop $DAGGER_CONTAINER_NETWORK_NAME
  docker rm $DAGGER_CONTAINER_NETWORK_NAME
  ```

1. Clear unused volumes and data:

  ```shell
  docker volume prune
  docker system prune
  ```

:::tip
If you are using a different container runtime, replace the commands above with the correct equivalents for your runtime.
:::

You should now be able to re-run your Dagger pipeline successfully.
