---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ClientHttpOpts

> **ClientHttpOpts** = `object`

## Properties

### authHeader?

> `optional` **authHeader?**: [`Secret`](../classes/Secret.md)

Secret used to populate the Authorization HTTP header

***

### checksum?

> `optional` **checksum?**: `string`

Expected digest of the downloaded content (e.g., "sha256:...").

***

### experimentalServiceHost?

> `optional` **experimentalServiceHost?**: [`Service`](../classes/Service.md)

A service which must be started before the URL is fetched.

***

### name?

> `optional` **name?**: `string`

File name to use for the file. Defaults to the last part of the URL.

***

### permissions?

> `optional` **permissions?**: `number`

Permissions to set on the file.
