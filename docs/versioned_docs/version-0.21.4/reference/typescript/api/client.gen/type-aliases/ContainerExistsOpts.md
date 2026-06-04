---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ContainerExistsOpts

> **ContainerExistsOpts** = `object`

## Properties

### doNotFollowSymlinks?

> `optional` **doNotFollowSymlinks?**: `boolean`

If specified, do not follow symlinks.

***

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").

***

### expectedType?

> `optional` **expectedType?**: [`ExistsType`](../enumerations/ExistsType.md)

If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
