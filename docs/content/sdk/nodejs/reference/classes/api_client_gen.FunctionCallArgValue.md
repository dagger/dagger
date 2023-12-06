---
id: "api_client_gen.FunctionCallArgValue"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).FunctionCallArgValue

## Hierarchy

- `BaseClient`

  ↳ **`FunctionCallArgValue`**

## Constructors

### constructor

**new FunctionCallArgValue**(`parent?`, `_name?`, `_value?`): [`FunctionCallArgValue`](api_client_gen.FunctionCallArgValue.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_name?` | `string` |
| `_value?` | [`JSON`](../modules/api_client_gen.md#json) |

#### Returns

[`FunctionCallArgValue`](api_client_gen.FunctionCallArgValue.md)

#### Overrides

BaseClient.constructor

## Properties

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_value

 `Private` `Optional` `Readonly` **\_value**: [`JSON`](../modules/api_client_gen.md#json) = `undefined`

## Methods

### name

**name**(): `Promise`\<`string`\>

The name of the argument.

#### Returns

`Promise`\<`string`\>

___

### value

**value**(): `Promise`\<[`JSON`](../modules/api_client_gen.md#json)\>

The value of the argument represented as a string of the JSON serialization.

#### Returns

`Promise`\<[`JSON`](../modules/api_client_gen.md#json)\>
