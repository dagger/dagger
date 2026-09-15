[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / EngineCacheEntry

# Class: EngineCacheEntry

An individual cache entry in a cache entry set

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new EngineCacheEntry**(`ctx?`, `_id?`, `_activelyUsed?`, `_createdTimeUnixNano?`, `_description?`, `_diskSpaceBytes?`, `_mostRecentUseTimeUnixNano?`): `EngineCacheEntry`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`EngineCacheEntryID`](../type-aliases/EngineCacheEntryID.md)

##### \_activelyUsed?

`boolean`

##### \_createdTimeUnixNano?

`number`

##### \_description?

`string`

##### \_diskSpaceBytes?

`number`

##### \_mostRecentUseTimeUnixNano?

`number`

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

> **id**(): `Promise`\<[`EngineCacheEntryID`](../type-aliases/EngineCacheEntryID.md)\>

A unique identifier for this EngineCacheEntry.

#### Returns

`Promise`\<[`EngineCacheEntryID`](../type-aliases/EngineCacheEntryID.md)\>

***

### mostRecentUseTimeUnixNano()

> **mostRecentUseTimeUnixNano**(): `Promise`\<`number`\>

The most recent time the cache entry was used, in Unix nanoseconds.

#### Returns

`Promise`\<`number`\>
