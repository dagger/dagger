---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: ObjectTypeDef

A definition of a custom object defined in a Module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new ObjectTypeDef**(`ctx?`, `_id?`, `_deprecated?`, `_description?`, `_name?`, `_sourceModuleName?`): `ObjectTypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_deprecated?

`string`

##### \_description?

`string`

##### \_name?

`string`

##### \_sourceModuleName?

`string`

#### Returns

`ObjectTypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### constructor\_()

> **constructor\_**(): [`Function_`](Function.md)

The function used to construct new instances of this object, if any.

#### Returns

[`Function_`](Function.md)

***

### deprecated()

> **deprecated**(): `Promise`\<`string`\>

The reason this enum member is deprecated, if any.

#### Returns

`Promise`\<`string`\>

***

### description()

> **description**(): `Promise`\<`string`\>

The doc string for the object, if any.

#### Returns

`Promise`\<`string`\>

***

### fields()

> **fields**(): `Promise`\<[`FieldTypeDef`](FieldTypeDef.md)[]\>

Static fields defined on this object, if any.

#### Returns

`Promise`\<[`FieldTypeDef`](FieldTypeDef.md)[]\>

***

### functions()

> **functions**(): `Promise`\<[`Function_`](Function.md)[]\>

Functions defined on this object, if any.

#### Returns

`Promise`\<[`Function_`](Function.md)[]\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this ObjectTypeDef.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the object.

#### Returns

`Promise`\<`string`\>

***

### sourceMap()

> **sourceMap**(): [`SourceMap`](SourceMap.md)

The location of this object declaration.

#### Returns

[`SourceMap`](SourceMap.md)

***

### sourceModuleName()

> **sourceModuleName**(): `Promise`\<`string`\>

If this ObjectTypeDef is associated with a Module, the name of the module. Unset otherwise.

#### Returns

`Promise`\<`string`\>
