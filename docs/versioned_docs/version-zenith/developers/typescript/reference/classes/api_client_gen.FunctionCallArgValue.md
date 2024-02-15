---
id: "api_client_gen.FunctionCallArgValue"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).FunctionCallArgValue

A value passed as a named argument to a function call.

## Hierarchy

- `BaseClient`

  ↳ **`FunctionCallArgValue`**

## Constructors

### constructor

**new FunctionCallArgValue**(`parent?`, `_id?`, `_name?`, `_value?`): [`FunctionCallArgValue`](api_client_gen.FunctionCallArgValue.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`FunctionCallArgValueID`](../modules/api_client_gen.md#functioncallargvalueid) |
| `_name?` | `string` |
| `_value?` | [`JSON`](../modules/api_client_gen.md#json) |

#### Returns

[`FunctionCallArgValue`](api_client_gen.FunctionCallArgValue.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`FunctionCallArgValueID`](../modules/api_client_gen.md#functioncallargvalueid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_value

 `Private` `Optional` `Readonly` **\_value**: [`JSON`](../modules/api_client_gen.md#json) = `undefined`

## Methods

### id

**id**(): `Promise`\<[`FunctionCallArgValueID`](../modules/api_client_gen.md#functioncallargvalueid)\>

A unique identifier for this FunctionCallArgValue.

#### Returns

`Promise`\<[`FunctionCallArgValueID`](../modules/api_client_gen.md#functioncallargvalueid)\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the argument.

#### Returns

`Promise`\<`string`\>

___

### value

**value**(): `Promise`\<[`JSON`](../modules/api_client_gen.md#json)\>

The value of the argument represented as a JSON serialized string.

#### Returns

`Promise`\<[`JSON`](../modules/api_client_gen.md#json)\>
