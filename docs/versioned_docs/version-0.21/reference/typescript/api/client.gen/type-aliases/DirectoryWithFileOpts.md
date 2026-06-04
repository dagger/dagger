---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: DirectoryWithFileOpts

> **DirectoryWithFileOpts** = `object`

## Properties

### owner?

> `optional` **owner?**: `string`

A user:group to set for the copied directory and its contents.

The user and group can either be an ID (1000:1000) or a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

***

### permissions?

> `optional` **permissions?**: `number`

Permission given to the copied file (e.g., 0600).
