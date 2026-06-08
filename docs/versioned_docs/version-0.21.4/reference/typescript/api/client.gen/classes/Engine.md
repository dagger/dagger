---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

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

[`ID`](../type-aliases/ID.md)

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

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Engine.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### localCache()

> **localCache**(): [`EngineCache`](EngineCache.md)

The local engine cache state tracked by dagql

#### Returns

[`EngineCache`](EngineCache.md)

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the engine instance.

#### Returns

`Promise`\<`string`\>
