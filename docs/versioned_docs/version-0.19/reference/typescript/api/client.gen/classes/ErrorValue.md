[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ErrorValue

# Class: ErrorValue

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new ErrorValue**(`ctx?`, `_id?`, `_name?`, `_value?`): `ErrorValue`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ErrorValueID`](../type-aliases/ErrorValueID.md)

##### \_name?

`string`

##### \_value?

[`JSON`](../type-aliases/JSON.md)

#### Returns

`ErrorValue`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ErrorValueID`](../type-aliases/ErrorValueID.md)\>

A unique identifier for this ErrorValue.

#### Returns

`Promise`\<[`ErrorValueID`](../type-aliases/ErrorValueID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the value.

#### Returns

`Promise`\<`string`\>

***

### value()

> **value**(): `Promise`\<[`JSON`](../type-aliases/JSON.md)\>

The value.

#### Returns

`Promise`\<[`JSON`](../type-aliases/JSON.md)\>
