---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: EngineCacheEntrySet

A set of cache entries returned by a query to a cache

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new EngineCacheEntrySet**(`ctx?`, `_id?`, `_diskSpaceBytes?`, `_entryCount?`): `EngineCacheEntrySet`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_diskSpaceBytes?

`number`

##### \_entryCount?

`number`

#### Returns

`EngineCacheEntrySet`

#### Overrides

`BaseClient.constructor`

## Methods

### diskSpaceBytes()

> **diskSpaceBytes**(): `Promise`\<`number`\>

The total disk space used by the cache entries in this set.

#### Returns

`Promise`\<`number`\>

***

### entries()

> **entries**(): `Promise`\<[`EngineCacheEntry`](EngineCacheEntry.md)[]\>

The list of individual cache entries in the set

#### Returns

`Promise`\<[`EngineCacheEntry`](EngineCacheEntry.md)[]\>

***

### entryCount()

> **entryCount**(): `Promise`\<`number`\>

The number of cache entries in this set.

#### Returns

`Promise`\<`number`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this EngineCacheEntrySet.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
