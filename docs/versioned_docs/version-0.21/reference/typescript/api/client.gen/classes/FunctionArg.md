---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: FunctionArg

An argument accepted by a function.

This is a specification for an argument at function definition time, not an argument passed at function call time.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new FunctionArg**(`ctx?`, `_id?`, `_defaultAddress?`, `_defaultPath?`, `_defaultValue?`, `_deprecated?`, `_description?`, `_name?`): `FunctionArg`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_defaultAddress?

`string`

##### \_defaultPath?

`string`

##### \_defaultValue?

[`JSON`](../type-aliases/JSON.md)

##### \_deprecated?

`string`

##### \_description?

`string`

##### \_name?

`string`

#### Returns

`FunctionArg`

#### Overrides

`BaseClient.constructor`

## Methods

### defaultAddress()

> **defaultAddress**(): `Promise`\<`string`\>

Only applies to arguments of type Container. If the argument is not set, load it from the given address (e.g. alpine:latest)

#### Returns

`Promise`\<`string`\>

***

### defaultPath()

> **defaultPath**(): `Promise`\<`string`\>

Only applies to arguments of type File or Directory. If the argument is not set, load it from the given path in the context directory

#### Returns

`Promise`\<`string`\>

***

### defaultValue()

> **defaultValue**(): `Promise`\<[`JSON`](../type-aliases/JSON.md)\>

A default value to use for this argument when not explicitly set by the caller, if any.

#### Returns

`Promise`\<[`JSON`](../type-aliases/JSON.md)\>

***

### deprecated()

> **deprecated**(): `Promise`\<`string`\>

The reason this function is deprecated, if any.

#### Returns

`Promise`\<`string`\>

***

### description()

> **description**(): `Promise`\<`string`\>

A doc string for the argument, if any.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this FunctionArg.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### ignore()

> **ignore**(): `Promise`\<`string`[]\>

Only applies to arguments of type Directory. The ignore patterns are applied to the input directory, and matching entries are filtered out, in a cache-efficient manner.

#### Returns

`Promise`\<`string`[]\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the argument in lowerCamelCase format.

#### Returns

`Promise`\<`string`\>

***

### sourceMap()

> **sourceMap**(): [`SourceMap`](SourceMap.md)

The location of this arg declaration.

#### Returns

[`SourceMap`](SourceMap.md)

***

### typeDef()

> **typeDef**(): [`TypeDef`](TypeDef.md)

The type of the argument.

#### Returns

[`TypeDef`](TypeDef.md)
