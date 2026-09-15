---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: UpGroup

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new UpGroup**(`ctx?`, `_id?`): `UpGroup`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

#### Returns

`UpGroup`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this UpGroup.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### list()

> **list**(): `Promise`\<[`Up`](Up.md)[]\>

Return a list of individual services and their details

#### Returns

`Promise`\<[`Up`](Up.md)[]\>

***

### run()

> **run**(): `UpGroup`

Execute all selected service functions

#### Returns

`UpGroup`

***

### with()

> **with**(`arg`): `UpGroup`

Call the provided function with current UpGroup.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `UpGroup`

#### Returns

`UpGroup`
