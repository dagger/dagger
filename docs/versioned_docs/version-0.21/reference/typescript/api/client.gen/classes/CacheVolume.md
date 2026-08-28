---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: CacheVolume

A directory whose contents persist across runs.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new CacheVolume**(`ctx?`, `_id?`): `CacheVolume`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

#### Returns

`CacheVolume`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this CacheVolume.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
