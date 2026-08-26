---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: DirectoryFilterOpts

> **DirectoryFilterOpts** = `object`

## Properties

### exclude?

> `optional` **exclude?**: `string`[]

If set, paths matching one of these glob patterns is excluded from the new snapshot. Example: ["node_modules/", ".git*", ".env"]

***

### gitignore?

> `optional` **gitignore?**: `boolean`

If set, apply .gitignore rules when filtering the directory.

***

### include?

> `optional` **include?**: `string`[]

If set, only paths matching one of these glob patterns is included in the new snapshot. Example: (e.g., ["app/", "package.*"]).
