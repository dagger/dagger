---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: EngineCacheEntry

An individual cache entry in a cache entry set

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new EngineCacheEntry**(`ctx?`, `_id?`, `_activelyUsed?`, `_createdTimeUnixNano?`, `_dagqlCall?`, `_description?`, `_diskSpaceBytes?`, `_mostRecentUseTimeUnixNano?`, `_recordType?`): `EngineCacheEntry`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_activelyUsed?

`boolean`

##### \_createdTimeUnixNano?

`number`

##### \_dagqlCall?

`string`

##### \_description?

`string`

##### \_diskSpaceBytes?

`number`

##### \_mostRecentUseTimeUnixNano?

`number`

##### \_recordType?

`string`

#### Returns

`EngineCacheEntry`

#### Overrides

`BaseClient.constructor`

## Methods

### activelyUsed()

> **activelyUsed**(): `Promise`\<`boolean`\>

Whether the cache entry is actively being used.

#### Returns

`Promise`\<`boolean`\>

***

### createdTimeUnixNano()

> **createdTimeUnixNano**(): `Promise`\<`number`\>

The time the cache entry was created, in Unix nanoseconds.

#### Returns

`Promise`\<`number`\>

***

### dagqlCall()

> **dagqlCall**(): `Promise`\<`string`\>

The DagQL call that produced this cache entry.

#### Returns

`Promise`\<`string`\>

***

### description()

> **description**(): `Promise`\<`string`\>

The description of the cache entry.

#### Returns

`Promise`\<`string`\>

***

### diskSpaceBytes()

> **diskSpaceBytes**(): `Promise`\<`number`\>

The disk space used by the cache entry.

#### Returns

`Promise`\<`number`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this EngineCacheEntry.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### mostRecentUseTimeUnixNano()

> **mostRecentUseTimeUnixNano**(): `Promise`\<`number`\>

The most recent time the cache entry was used, in Unix nanoseconds.

#### Returns

`Promise`\<`number`\>

***

### recordType()

> **recordType**(): `Promise`\<`string`\>

The type of the cache record (e.g. regular, internal, frontend, source.local, source.git.checkout, exec.cachemount).

#### Returns

`Promise`\<`string`\>

***

### recordTypes()

> **recordTypes**(): `Promise`\<`string`[]\>

The storage record types represented by this cache entry.

#### Returns

`Promise`\<`string`[]\>
