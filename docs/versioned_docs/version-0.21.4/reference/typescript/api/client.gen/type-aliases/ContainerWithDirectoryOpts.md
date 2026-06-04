---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ContainerWithDirectoryOpts

> **ContainerWithDirectoryOpts** = `object`

## Properties

### exclude?

> `optional` **exclude?**: `string`[]

Patterns to exclude in the written directory (e.g. ["node_modules/**", ".gitignore", ".git/"]).

***

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").

***

### gitignore?

> `optional` **gitignore?**: `boolean`

Apply .gitignore rules when writing the directory.

***

### include?

> `optional` **include?**: `string`[]

Patterns to include in the written directory (e.g. ["*.go", "go.mod", "go.sum"]).

***

### owner?

> `optional` **owner?**: `string`

A user:group to set for the directory and its contents.

The user and group can either be an ID (1000:1000) or a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

***

### permissions?

> `optional` **permissions?**: `number`
