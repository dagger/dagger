---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ContainerWithMountedSecretOpts

> **ContainerWithMountedSecretOpts** = `object`

## Properties

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").

***

### mode?

> `optional` **mode?**: `number`

Permission given to the mounted secret (e.g., 0600).

This option requires an owner to be set to be active.

***

### owner?

> `optional` **owner?**: `string`

A user:group to set for the mounted secret.

The user and group can either be an ID (1000:1000) or a name (foo:bar).

If the group is omitted, it defaults to the same as the user.
