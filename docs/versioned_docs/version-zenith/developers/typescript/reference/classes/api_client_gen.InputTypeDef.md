---
id: "api_client_gen.InputTypeDef"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).InputTypeDef

A graphql input type, which is essentially just a group of named args.
This is currently only used to represent pre-existing usage of graphql input types
in the core API. It is not used by user modules and shouldn't ever be as user
module accept input objects via their id rather than graphql input types.

## Hierarchy

- `BaseClient`

  ↳ **`InputTypeDef`**

## Constructors

### constructor

**new InputTypeDef**(`parent?`, `_id?`, `_name?`): [`InputTypeDef`](api_client_gen.InputTypeDef.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`InputTypeDefID`](../modules/api_client_gen.md#inputtypedefid) |
| `_name?` | `string` |

#### Returns

[`InputTypeDef`](api_client_gen.InputTypeDef.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`InputTypeDefID`](../modules/api_client_gen.md#inputtypedefid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

## Methods

### fields

**fields**(): `Promise`\<[`FieldTypeDef`](api_client_gen.FieldTypeDef.md)[]\>

Static fields defined on this input object, if any.

#### Returns

`Promise`\<[`FieldTypeDef`](api_client_gen.FieldTypeDef.md)[]\>

___

### id

**id**(): `Promise`\<[`InputTypeDefID`](../modules/api_client_gen.md#inputtypedefid)\>

A unique identifier for this InputTypeDef.

#### Returns

`Promise`\<[`InputTypeDefID`](../modules/api_client_gen.md#inputtypedefid)\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the input object.

#### Returns

`Promise`\<`string`\>
