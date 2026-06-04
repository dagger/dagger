---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Type Alias: EngineCachePruneOpts

> **EngineCachePruneOpts** = `object`

## Properties

### maxUsedSpace?

> `optional` **maxUsedSpace?**: `string`

Override the maximum disk space to keep before pruning (e.g. "200GB" or "80%").

***

### minFreeSpace?

> `optional` **minFreeSpace?**: `string`

Override the minimum free disk space target during pruning (e.g. "20GB" or "20%").

***

### reservedSpace?

> `optional` **reservedSpace?**: `string`

Override the minimum disk space to retain during pruning (e.g. "500GB" or "10%").

***

### targetSpace?

> `optional` **targetSpace?**: `string`

Override the target disk space to keep after pruning (e.g. "200GB" or "50%").

***

### useDefaultPolicy?

> `optional` **useDefaultPolicy?**: `boolean`

Use the engine-wide default pruning policy if true, otherwise prune the whole cache of any releasable entries.
