[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / InterfaceTypeDef

# Class: InterfaceTypeDef

A definition of a custom interface defined in a Module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new InterfaceTypeDef**(`ctx?`, `_id?`, `_description?`, `_name?`, `_sourceModuleName?`): `InterfaceTypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`InterfaceTypeDefID`](../type-aliases/InterfaceTypeDefID.md)

##### \_description?

`string`

##### \_name?

`string`

##### \_sourceModuleName?

`string`

#### Returns

`InterfaceTypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### description()

> **description**(): `Promise`\<`string`\>

The doc string for the interface, if any.

#### Returns

`Promise`\<`string`\>

***

### functions()

> **functions**(): `Promise`\<[`Function_`](Function.md)[]\>

Functions defined on this interface, if any.

#### Returns

`Promise`\<[`Function_`](Function.md)[]\>

***

### id()

> **id**(): `Promise`\<[`InterfaceTypeDefID`](../type-aliases/InterfaceTypeDefID.md)\>

A unique identifier for this InterfaceTypeDef.

#### Returns

`Promise`\<[`InterfaceTypeDefID`](../type-aliases/InterfaceTypeDefID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the interface.

#### Returns

`Promise`\<`string`\>

***

### sourceMap()

> **sourceMap**(): [`SourceMap`](SourceMap.md)

The location of this interface declaration.

#### Returns

[`SourceMap`](SourceMap.md)

***

### sourceModuleName()

> **sourceModuleName**(): `Promise`\<`string`\>

If this InterfaceTypeDef is associated with a Module, the name of the module. Unset otherwise.

#### Returns

`Promise`\<`string`\>
