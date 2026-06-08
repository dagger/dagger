[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ListTypeDef

# Class: ListTypeDef

A definition of a list type in a Module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new ListTypeDef**(`ctx?`, `_id?`): `ListTypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ListTypeDefID`](../type-aliases/ListTypeDefID.md)

#### Returns

`ListTypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### elementTypeDef()

> **elementTypeDef**(): [`TypeDef`](TypeDef.md)

The type of the elements in the list.

#### Returns

[`TypeDef`](TypeDef.md)

***

### id()

> **id**(): `Promise`\<[`ListTypeDefID`](../type-aliases/ListTypeDefID.md)\>

A unique identifier for this ListTypeDef.

#### Returns

`Promise`\<[`ListTypeDefID`](../type-aliases/ListTypeDefID.md)\>
