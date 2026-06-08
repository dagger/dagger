---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: \_SyncerClient

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new \_SyncerClient**(`ctx?`, `_id?`, `_sync?`): `_SyncerClient`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_sync?

[`ID`](../type-aliases/ID.md)

#### Returns

`_SyncerClient`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### sync()

> **sync**(): `Promise`\<[`Syncer`](../interfaces/Syncer.md)\>

#### Returns

`Promise`\<[`Syncer`](../interfaces/Syncer.md)\>
