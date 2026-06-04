---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: \_ExportableClient

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new \_ExportableClient**(`ctx?`, `_id?`, `_export?`): `_ExportableClient`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_export?

`string`

#### Returns

`_ExportableClient`

#### Overrides

`BaseClient.constructor`

## Methods

### export()

> **export**(`path`): `Promise`\<`string`\>

#### Parameters

##### path

`string`

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
