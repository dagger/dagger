---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: ScalarTypeDef

A definition of a custom scalar defined in a Module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new ScalarTypeDef**(`ctx?`, `_id?`, `_description?`, `_name?`, `_sourceModuleName?`): `ScalarTypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_description?

`string`

##### \_name?

`string`

##### \_sourceModuleName?

`string`

#### Returns

`ScalarTypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### description()

> **description**(): `Promise`\<`string`\>

A doc string for the scalar, if any.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this ScalarTypeDef.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the scalar.

#### Returns

`Promise`\<`string`\>

***

### sourceModuleName()

> **sourceModuleName**(): `Promise`\<`string`\>

If this ScalarTypeDef is associated with a Module, the name of the module. Unset otherwise.

#### Returns

`Promise`\<`string`\>
