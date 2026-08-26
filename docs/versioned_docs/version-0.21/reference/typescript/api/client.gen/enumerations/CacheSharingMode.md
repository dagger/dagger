---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Enumeration: CacheSharingMode

Sharing mode of the cache volume.

## Enumeration Members

### Locked

> **Locked**: `"LOCKED"`

Shares the cache volume amongst many build pipelines, but will serialize the writes

***

### Private

> **Private**: `"PRIVATE"`

Keeps a cache volume for a single build pipeline

***

### Shared

> **Shared**: `"SHARED"`

Shares the cache volume amongst many build pipelines
