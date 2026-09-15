---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Error

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Error**(`ctx?`, `_id?`, `_message?`): `Error`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_message?

`string`

#### Returns

`Error`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Error.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### message()

> **message**(): `Promise`\<`string`\>

A description of the error.

#### Returns

`Promise`\<`string`\>

***

### values()

> **values**(): `Promise`\<[`ErrorValue`](ErrorValue.md)[]\>

The extensions of the error.

#### Returns

`Promise`\<[`ErrorValue`](ErrorValue.md)[]\>

***

### with()

> **with**(`arg`): `Error`

Call the provided function with current Error.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Error`

#### Returns

`Error`

***

### withValue()

> **withValue**(`name`, `value`): `Error`

Add a value to the error.

#### Parameters

##### name

`string`

The name of the value.

##### value

[`JSON`](../type-aliases/JSON.md)

The value to store on the error.

#### Returns

`Error`
