[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / EngineCache

# Class: EngineCache

A cache storage for the Dagger engine

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new EngineCache**(`ctx?`, `_id?`, `_maxUsedSpace?`, `_minFreeSpace?`, `_prune?`, `_reservedSpace?`, `_targetSpace?`): `EngineCache`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`EngineCacheID`](../type-aliases/EngineCacheID.md)

##### \_maxUsedSpace?

`number`

##### \_minFreeSpace?

`number`

##### \_prune?

[`Void`](../type-aliases/Void.md)

##### \_reservedSpace?

`number`

##### \_targetSpace?

`number`

#### Returns

`EngineCache`

#### Overrides

`BaseClient.constructor`

## Methods

### entrySet()

> **entrySet**(`opts?`): [`EngineCacheEntrySet`](EngineCacheEntrySet.md)

The current set of entries in the cache

#### Parameters

##### opts?

[`EngineCacheEntrySetOpts`](../type-aliases/EngineCacheEntrySetOpts.md)

#### Returns

[`EngineCacheEntrySet`](EngineCacheEntrySet.md)

***

### id()

> **id**(): `Promise`\<[`EngineCacheID`](../type-aliases/EngineCacheID.md)\>

A unique identifier for this EngineCache.

#### Returns

`Promise`\<[`EngineCacheID`](../type-aliases/EngineCacheID.md)\>

***

### maxUsedSpace()

> **maxUsedSpace**(): `Promise`\<`number`\>

The maximum bytes to keep in the cache without pruning.

#### Returns

`Promise`\<`number`\>

***

### minFreeSpace()

> **minFreeSpace**(): `Promise`\<`number`\>

The target amount of free disk space the garbage collector will attempt to leave.

#### Returns

`Promise`\<`number`\>

***

### prune()

> **prune**(`opts?`): `Promise`\<`void`\>

Prune the cache of releaseable entries

#### Parameters

##### opts?

[`EngineCachePruneOpts`](../type-aliases/EngineCachePruneOpts.md)

#### Returns

`Promise`\<`void`\>

***

### reservedSpace()

> **reservedSpace**(): `Promise`\<`number`\>

The minimum amount of disk space this policy is guaranteed to retain.

#### Returns

`Promise`\<`number`\>

***

### targetSpace()

> **targetSpace**(): `Promise`\<`number`\>

The target number of bytes to keep when pruning.

#### Returns

`Promise`\<`number`\>
