[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Engine

# Class: Engine

The Dagger engine configuration and state

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Engine**(`ctx?`, `_id?`, `_name?`): `Engine`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`EngineID`](../type-aliases/EngineID.md)

##### \_name?

`string`

#### Returns

`Engine`

#### Overrides

`BaseClient.constructor`

## Methods

### clients()

> **clients**(): `Promise`\<`string`[]\>

The list of connected client IDs

#### Returns

`Promise`\<`string`[]\>

***

### id()

> **id**(): `Promise`\<[`EngineID`](../type-aliases/EngineID.md)\>

A unique identifier for this Engine.

#### Returns

`Promise`\<[`EngineID`](../type-aliases/EngineID.md)\>

***

### localCache()

> **localCache**(): [`EngineCache`](EngineCache.md)

The local (on-disk) cache for the Dagger engine

#### Returns

[`EngineCache`](EngineCache.md)

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the engine instance.

#### Returns

`Promise`\<`string`\>
