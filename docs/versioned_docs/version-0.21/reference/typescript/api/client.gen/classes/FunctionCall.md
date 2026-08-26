---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: FunctionCall

An active function call.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new FunctionCall**(`ctx?`, `_id?`, `_name?`, `_parent?`, `_parentName?`, `_returnError?`, `_returnValue?`): `FunctionCall`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_name?

`string`

##### \_parent?

[`JSON`](../type-aliases/JSON.md)

##### \_parentName?

`string`

##### \_returnError?

[`Void`](../type-aliases/Void.md)

##### \_returnValue?

[`Void`](../type-aliases/Void.md)

#### Returns

`FunctionCall`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this FunctionCall.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### inputArgs()

> **inputArgs**(): `Promise`\<[`FunctionCallArgValue`](FunctionCallArgValue.md)[]\>

The argument values the function is being invoked with.

#### Returns

`Promise`\<[`FunctionCallArgValue`](FunctionCallArgValue.md)[]\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the function being called.

#### Returns

`Promise`\<`string`\>

***

### parent()

> **parent**(): `Promise`\<[`JSON`](../type-aliases/JSON.md)\>

The value of the parent object of the function being called. If the function is top-level to the module, this is always an empty object.

#### Returns

`Promise`\<[`JSON`](../type-aliases/JSON.md)\>

***

### parentName()

> **parentName**(): `Promise`\<`string`\>

The name of the parent object of the function being called. If the function is top-level to the module, this is the name of the module.

#### Returns

`Promise`\<`string`\>

***

### returnError()

> **returnError**(`error`): `Promise`\<`void`\>

Return an error from the function.

#### Parameters

##### error

[`Error`](Error.md)

The error to return.

#### Returns

`Promise`\<`void`\>

***

### returnValue()

> **returnValue**(`value`): `Promise`\<`void`\>

Set the return value of the function call to the provided value.

#### Parameters

##### value

[`JSON`](../type-aliases/JSON.md)

JSON serialization of the return value.

#### Returns

`Promise`\<`void`\>
