---
id: "api_client_gen.ListTypeDef"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).ListTypeDef

A definition of a list type in a Module.

## Hierarchy

- `BaseClient`

  ↳ **`ListTypeDef`**

## Constructors

### constructor

**new ListTypeDef**(`parent?`, `_id?`): [`ListTypeDef`](api_client_gen.ListTypeDef.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`ListTypeDefID`](../modules/api_client_gen.md#listtypedefid) |

#### Returns

[`ListTypeDef`](api_client_gen.ListTypeDef.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`ListTypeDefID`](../modules/api_client_gen.md#listtypedefid) = `undefined`

## Methods

### elementTypeDef

**elementTypeDef**(): [`TypeDef`](api_client_gen.TypeDef.md)

The type of the elements in the list.

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### id

**id**(): `Promise`\<[`ListTypeDefID`](../modules/api_client_gen.md#listtypedefid)\>

A unique identifier for this ListTypeDef.

#### Returns

`Promise`\<[`ListTypeDefID`](../modules/api_client_gen.md#listtypedefid)\>
