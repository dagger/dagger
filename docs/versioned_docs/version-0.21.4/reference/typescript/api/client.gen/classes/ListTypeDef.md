---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: ListTypeDef

A definition of a list type in a Module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new ListTypeDef**(`ctx?`, `_id?`): `ListTypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

#### Returns

`ListTypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### elementTypeDef()

> **elementTypeDef**(): [`TypeDef`](TypeDef.md)

The type of the elements in the list.

#### Returns

[`TypeDef`](TypeDef.md)

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this ListTypeDef.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
