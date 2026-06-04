[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / InputTypeDef

# Class: InputTypeDef

A graphql input type, which is essentially just a group of named args.
This is currently only used to represent pre-existing usage of graphql input types
in the core API. It is not used by user modules and shouldn't ever be as user
module accept input objects via their id rather than graphql input types.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new InputTypeDef**(`ctx?`, `_id?`, `_name?`): `InputTypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`InputTypeDefID`](../type-aliases/InputTypeDefID.md)

##### \_name?

`string`

#### Returns

`InputTypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### fields()

> **fields**(): `Promise`\<[`FieldTypeDef`](FieldTypeDef.md)[]\>

Static fields defined on this input object, if any.

#### Returns

`Promise`\<[`FieldTypeDef`](FieldTypeDef.md)[]\>

***

### id()

> **id**(): `Promise`\<[`InputTypeDefID`](../type-aliases/InputTypeDefID.md)\>

A unique identifier for this InputTypeDef.

#### Returns

`Promise`\<[`InputTypeDefID`](../type-aliases/InputTypeDefID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the input object.

#### Returns

`Promise`\<`string`\>
