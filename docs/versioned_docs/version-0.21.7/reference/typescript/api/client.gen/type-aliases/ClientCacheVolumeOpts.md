---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ClientCacheVolumeOpts

> **ClientCacheVolumeOpts** = `object`

## Properties

### owner?

> `optional` **owner?**: `string`

A user:group to set for the cache volume root.

The user and group can either be an ID (1000:1000) or a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

***

### sharing?

> `optional` **sharing?**: [`CacheSharingMode`](../enumerations/CacheSharingMode.md)

Sharing mode of the cache volume.

***

### source?

> `optional` **source?**: [`Directory`](../classes/Directory.md)

Identifier of the directory to use as the cache volume's root.
