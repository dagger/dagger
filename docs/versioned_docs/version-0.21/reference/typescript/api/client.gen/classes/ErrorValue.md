---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: ErrorValue

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new ErrorValue**(`ctx?`, `_id?`, `_name?`, `_value?`): `ErrorValue`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_name?

`string`

##### \_value?

[`JSON`](../type-aliases/JSON.md)

#### Returns

`ErrorValue`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this ErrorValue.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the value.

#### Returns

`Promise`\<`string`\>

***

### value()

> **value**(): `Promise`\<[`JSON`](../type-aliases/JSON.md)\>

The value.

#### Returns

`Promise`\<[`JSON`](../type-aliases/JSON.md)\>
