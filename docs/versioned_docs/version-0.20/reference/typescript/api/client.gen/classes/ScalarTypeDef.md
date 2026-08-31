[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ScalarTypeDef

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

[`ScalarTypeDefID`](../type-aliases/ScalarTypeDefID.md)

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

> **id**(): `Promise`\<[`ScalarTypeDefID`](../type-aliases/ScalarTypeDefID.md)\>

A unique identifier for this ScalarTypeDef.

#### Returns

`Promise`\<[`ScalarTypeDefID`](../type-aliases/ScalarTypeDefID.md)\>

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
