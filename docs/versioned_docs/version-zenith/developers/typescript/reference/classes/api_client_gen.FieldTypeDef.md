---
id: "api_client_gen.FieldTypeDef"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).FieldTypeDef

A definition of a field on a custom object defined in a Module.

A field on an object has a static value, as opposed to a function on an object whose value is computed by invoking code (and can accept arguments).

## Hierarchy

- `BaseClient`

  ↳ **`FieldTypeDef`**

## Constructors

### constructor

**new FieldTypeDef**(`parent?`, `_id?`, `_description?`, `_name?`): [`FieldTypeDef`](api_client_gen.FieldTypeDef.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`FieldTypeDefID`](../modules/api_client_gen.md#fieldtypedefid) |
| `_description?` | `string` |
| `_name?` | `string` |

#### Returns

[`FieldTypeDef`](api_client_gen.FieldTypeDef.md)

#### Overrides

BaseClient.constructor

## Properties

### \_description

 `Private` `Optional` `Readonly` **\_description**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`FieldTypeDefID`](../modules/api_client_gen.md#fieldtypedefid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

## Methods

### description

**description**(): `Promise`\<`string`\>

A doc string for the field, if any.

#### Returns

`Promise`\<`string`\>

___

### id

**id**(): `Promise`\<[`FieldTypeDefID`](../modules/api_client_gen.md#fieldtypedefid)\>

A unique identifier for this FieldTypeDef.

#### Returns

`Promise`\<[`FieldTypeDefID`](../modules/api_client_gen.md#fieldtypedefid)\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the field in lowerCamelCase format.

#### Returns

`Promise`\<`string`\>

___

### typeDef

**typeDef**(): [`TypeDef`](api_client_gen.TypeDef.md)

The type of the field.

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)
