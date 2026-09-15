---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: DirectoryWithDirectoryOpts

> **DirectoryWithDirectoryOpts** = `object`

## Properties

### exclude?

> `optional` **exclude?**: `string`[]

Exclude artifacts that match the given pattern (e.g., ["node_modules/", ".git*"]).

***

### gitignore?

> `optional` **gitignore?**: `boolean`

Apply .gitignore filter rules inside the directory

***

### include?

> `optional` **include?**: `string`[]

Include only artifacts that match the given pattern (e.g., ["app/", "package.*"]).

***

### owner?

> `optional` **owner?**: `string`

A user:group to set for the copied directory and its contents.

The user and group can either be an ID (1000:1000) or a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

***

### permissions?

> `optional` **permissions?**: `number`

Permission given to the copied directory and contents (e.g., 0755).
