---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: EnumTypeDef

A definition of a custom enum defined in a Module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new EnumTypeDef**(`ctx?`, `_id?`, `_description?`, `_name?`, `_sourceModuleName?`): `EnumTypeDef`

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

`EnumTypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### description()

> **description**(): `Promise`\<`string`\>

A doc string for the enum, if any.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this EnumTypeDef.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### members()

> **members**(): `Promise`\<[`EnumValueTypeDef`](EnumValueTypeDef.md)[]\>

The members of the enum.

#### Returns

`Promise`\<[`EnumValueTypeDef`](EnumValueTypeDef.md)[]\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the enum.

#### Returns

`Promise`\<`string`\>

***

### sourceMap()

> **sourceMap**(): [`SourceMap`](SourceMap.md)

The location of this enum declaration.

#### Returns

[`SourceMap`](SourceMap.md)

***

### sourceModuleName()

> **sourceModuleName**(): `Promise`\<`string`\>

If this EnumTypeDef is associated with a Module, the name of the module. Unset otherwise.

#### Returns

`Promise`\<`string`\>

***

### ~~values()~~

> **values**(): `Promise`\<[`EnumValueTypeDef`](EnumValueTypeDef.md)[]\>

The members of the enum.

#### Returns

`Promise`\<[`EnumValueTypeDef`](EnumValueTypeDef.md)[]\>

#### Deprecated

use members instead
