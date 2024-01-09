---
id: "api_client_gen.TypeDef"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).TypeDef

A definition of a parameter or return type in a Module.

## Hierarchy

- `BaseClient`

  ↳ **`TypeDef`**

## Constructors

### constructor

**new TypeDef**(`parent?`, `_id?`, `_kind?`, `_optional?`): [`TypeDef`](api_client_gen.TypeDef.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`TypeDefID`](../modules/api_client_gen.md#typedefid) |
| `_kind?` | [`TypeDefKind`](../enums/api_client_gen.TypeDefKind.md) |
| `_optional?` | `boolean` |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`TypeDefID`](../modules/api_client_gen.md#typedefid) = `undefined`

___

### \_kind

 `Private` `Optional` `Readonly` **\_kind**: [`TypeDefKind`](../enums/api_client_gen.TypeDefKind.md) = `undefined`

___

### \_optional

 `Private` `Optional` `Readonly` **\_optional**: `boolean` = `undefined`

## Methods

### asInterface

**asInterface**(): [`InterfaceTypeDef`](api_client_gen.InterfaceTypeDef.md)

If kind is INTERFACE, the interface-specific type definition.
If kind is not INTERFACE, this will be null.

#### Returns

[`InterfaceTypeDef`](api_client_gen.InterfaceTypeDef.md)

___

### asList

**asList**(): [`ListTypeDef`](api_client_gen.ListTypeDef.md)

If kind is LIST, the list-specific type definition.
If kind is not LIST, this will be null.

#### Returns

[`ListTypeDef`](api_client_gen.ListTypeDef.md)

___

### asObject

**asObject**(): [`ObjectTypeDef`](api_client_gen.ObjectTypeDef.md)

If kind is OBJECT, the object-specific type definition.
If kind is not OBJECT, this will be null.

#### Returns

[`ObjectTypeDef`](api_client_gen.ObjectTypeDef.md)

___

### id

**id**(): `Promise`\<[`TypeDefID`](../modules/api_client_gen.md#typedefid)\>

#### Returns

`Promise`\<[`TypeDefID`](../modules/api_client_gen.md#typedefid)\>

___

### kind

**kind**(): `Promise`\<[`TypeDefKind`](../enums/api_client_gen.TypeDefKind.md)\>

The kind of type this is (e.g. primitive, list, object)

#### Returns

`Promise`\<[`TypeDefKind`](../enums/api_client_gen.TypeDefKind.md)\>

___

### optional

**optional**(): `Promise`\<`boolean`\>

Whether this type can be set to null. Defaults to false.

#### Returns

`Promise`\<`boolean`\>

___

### with

**with**(`arg`): [`TypeDef`](api_client_gen.TypeDef.md)

Call the provided function with current TypeDef.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`TypeDef`](api_client_gen.TypeDef.md)) => [`TypeDef`](api_client_gen.TypeDef.md) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### withConstructor

**withConstructor**(`function_`): [`TypeDef`](api_client_gen.TypeDef.md)

Adds a function for constructing a new instance of an Object TypeDef, failing if the type is not an object.

#### Parameters

| Name | Type |
| :------ | :------ |
| `function_` | [`Function_`](api_client_gen.Function_.md) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### withField

**withField**(`name`, `typeDef`, `opts?`): [`TypeDef`](api_client_gen.TypeDef.md)

Adds a static field for an Object TypeDef, failing if the type is not an object.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name of the field in the object |
| `typeDef` | [`TypeDef`](api_client_gen.TypeDef.md) | The type of the field |
| `opts?` | [`TypeDefWithFieldOpts`](../modules/api_client_gen.md#typedefwithfieldopts) | - |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### withFunction

**withFunction**(`function_`): [`TypeDef`](api_client_gen.TypeDef.md)

Adds a function for an Object or Interface TypeDef, failing if the type is not one of those kinds.

#### Parameters

| Name | Type |
| :------ | :------ |
| `function_` | [`Function_`](api_client_gen.Function_.md) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### withInterface

**withInterface**(`name`, `opts?`): [`TypeDef`](api_client_gen.TypeDef.md)

Returns a TypeDef of kind Interface with the provided name.

#### Parameters

| Name | Type |
| :------ | :------ |
| `name` | `string` |
| `opts?` | [`TypeDefWithInterfaceOpts`](../modules/api_client_gen.md#typedefwithinterfaceopts) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### withKind

**withKind**(`kind`): [`TypeDef`](api_client_gen.TypeDef.md)

Sets the kind of the type.

#### Parameters

| Name | Type |
| :------ | :------ |
| `kind` | [`TypeDefKind`](../enums/api_client_gen.TypeDefKind.md) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### withListOf

**withListOf**(`elementType`): [`TypeDef`](api_client_gen.TypeDef.md)

Returns a TypeDef of kind List with the provided type for its elements.

#### Parameters

| Name | Type |
| :------ | :------ |
| `elementType` | [`TypeDef`](api_client_gen.TypeDef.md) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### withObject

**withObject**(`name`, `opts?`): [`TypeDef`](api_client_gen.TypeDef.md)

Returns a TypeDef of kind Object with the provided name.

Note that an object's fields and functions may be omitted if the intent is
only to refer to an object. This is how functions are able to return their
own object, or any other circular reference.

#### Parameters

| Name | Type |
| :------ | :------ |
| `name` | `string` |
| `opts?` | [`TypeDefWithObjectOpts`](../modules/api_client_gen.md#typedefwithobjectopts) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### withOptional

**withOptional**(`optional`): [`TypeDef`](api_client_gen.TypeDef.md)

Sets whether this type can be set to null.

#### Parameters

| Name | Type |
| :------ | :------ |
| `optional` | `boolean` |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)
