[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / FunctionCallArgValue

# Class: FunctionCallArgValue

A value passed as a named argument to a function call.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new FunctionCallArgValue**(`ctx?`, `_id?`, `_name?`, `_value?`): `FunctionCallArgValue`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`FunctionCallArgValueID`](../type-aliases/FunctionCallArgValueID.md)

##### \_name?

`string`

##### \_value?

[`JSON`](../type-aliases/JSON.md)

#### Returns

`FunctionCallArgValue`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`FunctionCallArgValueID`](../type-aliases/FunctionCallArgValueID.md)\>

A unique identifier for this FunctionCallArgValue.

#### Returns

`Promise`\<[`FunctionCallArgValueID`](../type-aliases/FunctionCallArgValueID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the argument.

#### Returns

`Promise`\<`string`\>

***

### value()

> **value**(): `Promise`\<[`JSON`](../type-aliases/JSON.md)\>

The value of the argument represented as a JSON serialized string.

#### Returns

`Promise`\<[`JSON`](../type-aliases/JSON.md)\>
