---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: ClientCurrentTypeDefsOpts

> **ClientCurrentTypeDefsOpts** = `object`

## Properties

### hideCore?

> `optional` **hideCore?**: `boolean`

Strip core API functions from the Query type, leaving only module-sourced functions (constructors, entrypoint proxies, etc.).

Core types (Container, Directory, etc.) are kept so return types and method chaining still work.

***

### returnAllTypes?

> `optional` **returnAllTypes?**: `boolean`

Return the full referenced typedef closure instead of only top-level served typedefs.
