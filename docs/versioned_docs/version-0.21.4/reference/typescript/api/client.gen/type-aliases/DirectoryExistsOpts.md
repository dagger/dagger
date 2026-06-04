---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: DirectoryExistsOpts

> **DirectoryExistsOpts** = `object`

## Properties

### doNotFollowSymlinks?

> `optional` **doNotFollowSymlinks?**: `boolean`

If specified, do not follow symlinks.

***

### expectedType?

> `optional` **expectedType?**: [`ExistsType`](../enumerations/ExistsType.md)

If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
