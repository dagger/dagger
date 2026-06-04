[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / EnumValueTypeDef

# Class: EnumValueTypeDef

A definition of a value in a custom enum defined in a Module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new EnumValueTypeDef**(`ctx?`, `_id?`, `_deprecated?`, `_description?`, `_name?`, `_value?`): `EnumValueTypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`EnumValueTypeDefID`](../type-aliases/EnumValueTypeDefID.md)

##### \_deprecated?

`string`

##### \_description?

`string`

##### \_name?

`string`

##### \_value?

`string`

#### Returns

`EnumValueTypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### deprecated()

> **deprecated**(): `Promise`\<`string`\>

The reason this enum member is deprecated, if any.

#### Returns

`Promise`\<`string`\>

***

### description()

> **description**(): `Promise`\<`string`\>

A doc string for the enum member, if any.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`EnumValueTypeDefID`](../type-aliases/EnumValueTypeDefID.md)\>

A unique identifier for this EnumValueTypeDef.

#### Returns

`Promise`\<[`EnumValueTypeDefID`](../type-aliases/EnumValueTypeDefID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the enum member.

#### Returns

`Promise`\<`string`\>

***

### sourceMap()

> **sourceMap**(): [`SourceMap`](SourceMap.md)

The location of this enum member declaration.

#### Returns

[`SourceMap`](SourceMap.md)

***

### value()

> **value**(): `Promise`\<`string`\>

The value of the enum member

#### Returns

`Promise`\<`string`\>
