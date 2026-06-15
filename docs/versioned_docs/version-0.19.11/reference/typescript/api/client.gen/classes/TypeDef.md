[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / TypeDef

# Class: TypeDef

A definition of a parameter or return type in a Module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new TypeDef**(`ctx?`, `_id?`, `_kind?`, `_optional?`): `TypeDef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`TypeDefID`](../type-aliases/TypeDefID.md)

##### \_kind?

[`TypeDefKind`](../enumerations/TypeDefKind.md)

##### \_optional?

`boolean`

#### Returns

`TypeDef`

#### Overrides

`BaseClient.constructor`

## Methods

### asEnum()

> **asEnum**(): [`EnumTypeDef`](EnumTypeDef.md)

If kind is ENUM, the enum-specific type definition. If kind is not ENUM, this will be null.

#### Returns

[`EnumTypeDef`](EnumTypeDef.md)

***

### asInput()

> **asInput**(): [`InputTypeDef`](InputTypeDef.md)

If kind is INPUT, the input-specific type definition. If kind is not INPUT, this will be null.

#### Returns

[`InputTypeDef`](InputTypeDef.md)

***

### asInterface()

> **asInterface**(): [`InterfaceTypeDef`](InterfaceTypeDef.md)

If kind is INTERFACE, the interface-specific type definition. If kind is not INTERFACE, this will be null.

#### Returns

[`InterfaceTypeDef`](InterfaceTypeDef.md)

***

### asList()

> **asList**(): [`ListTypeDef`](ListTypeDef.md)

If kind is LIST, the list-specific type definition. If kind is not LIST, this will be null.

#### Returns

[`ListTypeDef`](ListTypeDef.md)

***

### asObject()

> **asObject**(): [`ObjectTypeDef`](ObjectTypeDef.md)

If kind is OBJECT, the object-specific type definition. If kind is not OBJECT, this will be null.

#### Returns

[`ObjectTypeDef`](ObjectTypeDef.md)

***

### asScalar()

> **asScalar**(): [`ScalarTypeDef`](ScalarTypeDef.md)

If kind is SCALAR, the scalar-specific type definition. If kind is not SCALAR, this will be null.

#### Returns

[`ScalarTypeDef`](ScalarTypeDef.md)

***

### id()

> **id**(): `Promise`\<[`TypeDefID`](../type-aliases/TypeDefID.md)\>

A unique identifier for this TypeDef.

#### Returns

`Promise`\<[`TypeDefID`](../type-aliases/TypeDefID.md)\>

***

### kind()

> **kind**(): `Promise`\<[`TypeDefKind`](../enumerations/TypeDefKind.md)\>

The kind of type this is (e.g. primitive, list, object).

#### Returns

`Promise`\<[`TypeDefKind`](../enumerations/TypeDefKind.md)\>

***

### optional()

> **optional**(): `Promise`\<`boolean`\>

Whether this type can be set to null. Defaults to false.

#### Returns

`Promise`\<`boolean`\>

***

### with()

> **with**(`arg`): `TypeDef`

Call the provided function with current TypeDef.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `TypeDef`

#### Returns

`TypeDef`

***

### withConstructor()

> **withConstructor**(`function_`): `TypeDef`

Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.

#### Parameters

##### function\_

[`Function_`](Function.md)

#### Returns

`TypeDef`

***

### withEnum()

> **withEnum**(`name`, `opts?`): `TypeDef`

Returns a TypeDef of kind Enum with the provided name.

Note that an enum's values may be omitted if the intent is only to refer to an enum. This is how functions are able to return their own, or any other circular reference.

#### Parameters

##### name

`string`

The name of the enum

##### opts?

[`TypeDefWithEnumOpts`](../type-aliases/TypeDefWithEnumOpts.md)

#### Returns

`TypeDef`

***

### withEnumMember()

> **withEnumMember**(`name`, `opts?`): `TypeDef`

Adds a static value for an Enum TypeDef, failing if the type is not an enum.

#### Parameters

##### name

`string`

The name of the member in the enum

##### opts?

[`TypeDefWithEnumMemberOpts`](../type-aliases/TypeDefWithEnumMemberOpts.md)

#### Returns

`TypeDef`

***

### ~~withEnumValue()~~

> **withEnumValue**(`value`, `opts?`): `TypeDef`

Adds a static value for an Enum TypeDef, failing if the type is not an enum.

#### Parameters

##### value

`string`

The name of the value in the enum

##### opts?

[`TypeDefWithEnumValueOpts`](../type-aliases/TypeDefWithEnumValueOpts.md)

#### Returns

`TypeDef`

#### Deprecated

Use withEnumMember instead

***

### withField()

> **withField**(`name`, `typeDef`, `opts?`): `TypeDef`

Adds a static field for an Object TypeDef, failing if the type is not an object.

#### Parameters

##### name

`string`

The name of the field in the object

##### typeDef

`TypeDef`

The type of the field

##### opts?

[`TypeDefWithFieldOpts`](../type-aliases/TypeDefWithFieldOpts.md)

#### Returns

`TypeDef`

***

### withFunction()

> **withFunction**(`function_`): `TypeDef`

Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.

#### Parameters

##### function\_

[`Function_`](Function.md)

#### Returns

`TypeDef`

***

### withInterface()

> **withInterface**(`name`, `opts?`): `TypeDef`

Returns a TypeDef of kind Interface with the provided name.

#### Parameters

##### name

`string`

##### opts?

[`TypeDefWithInterfaceOpts`](../type-aliases/TypeDefWithInterfaceOpts.md)

#### Returns

`TypeDef`

***

### withKind()

> **withKind**(`kind`): `TypeDef`

Sets the kind of the type.

#### Parameters

##### kind

[`TypeDefKind`](../enumerations/TypeDefKind.md)

#### Returns

`TypeDef`

***

### withListOf()

> **withListOf**(`elementType`): `TypeDef`

Returns a TypeDef of kind List with the provided type for its elements.

#### Parameters

##### elementType

`TypeDef`

#### Returns

`TypeDef`

***

### withObject()

> **withObject**(`name`, `opts?`): `TypeDef`

Returns a TypeDef of kind Object with the provided name.

Note that an object's fields and functions may be omitted if the intent is only to refer to an object. This is how functions are able to return their own object, or any other circular reference.

#### Parameters

##### name

`string`

##### opts?

[`TypeDefWithObjectOpts`](../type-aliases/TypeDefWithObjectOpts.md)

#### Returns

`TypeDef`

***

### withOptional()

> **withOptional**(`optional`): `TypeDef`

Sets whether this type can be set to null.

#### Parameters

##### optional

`boolean`

#### Returns

`TypeDef`

***

### withScalar()

> **withScalar**(`name`, `opts?`): `TypeDef`

Returns a TypeDef of kind Scalar with the provided name.

#### Parameters

##### name

`string`

##### opts?

[`TypeDefWithScalarOpts`](../type-aliases/TypeDefWithScalarOpts.md)

#### Returns

`TypeDef`
