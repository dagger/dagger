---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: EnvVariable

An environment variable name and value.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new EnvVariable**(`ctx?`, `_id?`, `_name?`, `_value?`): `EnvVariable`

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

`EnvVariable`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this EnvVariable.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The environment variable name.

#### Returns

`Promise`\<`string`\>

***

### value()

> **value**(): `Promise`\<`string`\>

The environment variable value.

#### Returns

`Promise`\<`string`\>
