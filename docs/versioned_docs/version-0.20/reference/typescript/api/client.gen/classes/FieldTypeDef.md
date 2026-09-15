[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / FieldTypeDef

# Class: FieldTypeDef

A definition of a field on a custom object defined in a Module.

A field on an object has a static value, as opposed to a function on an object whose value is computed by invoking code (and can accept arguments).

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new FieldTypeDef**(`ctx?`, `_id?`, `_deprecated?`, `_description?`, `_name?`): `FieldTypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`FieldTypeDefID`](../type-aliases/FieldTypeDefID.md)

##### \_deprecated?

`string`

##### \_description?

`string`

##### \_name?

`string`

#### Returns

`FieldTypeDef`

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

A doc string for the field, if any.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`FieldTypeDefID`](../type-aliases/FieldTypeDefID.md)\>

A unique identifier for this FieldTypeDef.

#### Returns

`Promise`\<[`FieldTypeDefID`](../type-aliases/FieldTypeDefID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the field in lowerCamelCase format.

#### Returns

`Promise`\<`string`\>

***

### sourceMap()

> **sourceMap**(): [`SourceMap`](SourceMap.md)

The location of this field declaration.

#### Returns

[`SourceMap`](SourceMap.md)

***

### typeDef()

> **typeDef**(): [`TypeDef`](TypeDef.md)

The type of the field.

#### Returns

[`TypeDef`](TypeDef.md)
