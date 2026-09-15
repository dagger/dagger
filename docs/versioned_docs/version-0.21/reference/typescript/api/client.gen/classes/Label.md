---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Label

A simple key value object that represents a label.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Label**(`ctx?`, `_id?`, `_name?`, `_value?`): `Label`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_name?

`string`

##### \_value?

`string`

#### Returns

`Label`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Label.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The label name.

#### Returns

`Promise`\<`string`\>

***

### value()

> **value**(): `Promise`\<`string`\>

The label value.

#### Returns

`Promise`\<`string`\>
