---
id: "api_client_gen.Label"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).Label

A simple key value object that represents a label.

## Hierarchy

- `BaseClient`

  ↳ **`Label`**

## Constructors

### constructor

**new Label**(`parent?`, `_id?`, `_name?`, `_value?`): [`Label`](api_client_gen.Label.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`LabelID`](../modules/api_client_gen.md#labelid) |
| `_name?` | `string` |
| `_value?` | `string` |

#### Returns

[`Label`](api_client_gen.Label.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`LabelID`](../modules/api_client_gen.md#labelid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_value

 `Private` `Optional` `Readonly` **\_value**: `string` = `undefined`

## Methods

### id

**id**(): `Promise`\<[`LabelID`](../modules/api_client_gen.md#labelid)\>

A unique identifier for this Label.

#### Returns

`Promise`\<[`LabelID`](../modules/api_client_gen.md#labelid)\>

___

### name

**name**(): `Promise`\<`string`\>

The label name.

#### Returns

`Promise`\<`string`\>

___

### value

**value**(): `Promise`\<`string`\>

The label value.

#### Returns

`Promise`\<`string`\>
