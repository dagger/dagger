---
id: "api_client_gen.CacheSharingMode"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).CacheSharingMode

Sharing mode of the cache volume.

## Enumeration Members

### Locked

 **Locked** = ``"LOCKED"``

Shares the cache volume amongst many build pipelines,
but will serialize the writes

___

### Private

 **Private** = ``"PRIVATE"``

Keeps a cache volume for a single build pipeline

___

### Shared

 **Shared** = ``"SHARED"``

Shares the cache volume amongst many build pipelines
