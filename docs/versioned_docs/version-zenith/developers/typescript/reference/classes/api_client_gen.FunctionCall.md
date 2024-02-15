---
id: "api_client_gen.FunctionCall"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).FunctionCall

An active function call.

## Hierarchy

- `BaseClient`

  ↳ **`FunctionCall`**

## Constructors

### constructor

**new FunctionCall**(`parent?`, `_id?`, `_name?`, `_parent?`, `_parentName?`, `_returnValue?`): [`FunctionCall`](api_client_gen.FunctionCall.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`FunctionCallID`](../modules/api_client_gen.md#functioncallid) |
| `_name?` | `string` |
| `_parent?` | [`JSON`](../modules/api_client_gen.md#json) |
| `_parentName?` | `string` |
| `_returnValue?` | [`Void`](../modules/api_client_gen.md#void) |

#### Returns

[`FunctionCall`](api_client_gen.FunctionCall.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`FunctionCallID`](../modules/api_client_gen.md#functioncallid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_parent

 `Private` `Optional` `Readonly` **\_parent**: [`JSON`](../modules/api_client_gen.md#json) = `undefined`

___

### \_parentName

 `Private` `Optional` `Readonly` **\_parentName**: `string` = `undefined`

___

### \_returnValue

 `Private` `Optional` `Readonly` **\_returnValue**: [`Void`](../modules/api_client_gen.md#void) = `undefined`

## Methods

### id

**id**(): `Promise`\<[`FunctionCallID`](../modules/api_client_gen.md#functioncallid)\>

A unique identifier for this FunctionCall.

#### Returns

`Promise`\<[`FunctionCallID`](../modules/api_client_gen.md#functioncallid)\>

___

### inputArgs

**inputArgs**(): `Promise`\<[`FunctionCallArgValue`](api_client_gen.FunctionCallArgValue.md)[]\>

The argument values the function is being invoked with.

#### Returns

`Promise`\<[`FunctionCallArgValue`](api_client_gen.FunctionCallArgValue.md)[]\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the function being called.

#### Returns

`Promise`\<`string`\>

___

### parent

**parent**(): `Promise`\<[`JSON`](../modules/api_client_gen.md#json)\>

The value of the parent object of the function being called. If the function is top-level to the module, this is always an empty object.

#### Returns

`Promise`\<[`JSON`](../modules/api_client_gen.md#json)\>

___

### parentName

**parentName**(): `Promise`\<`string`\>

The name of the parent object of the function being called. If the function is top-level to the module, this is the name of the module.

#### Returns

`Promise`\<`string`\>

___

### returnValue

**returnValue**(`value`): `Promise`\<[`Void`](../modules/api_client_gen.md#void)\>

Set the return value of the function call to the provided value.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `value` | [`JSON`](../modules/api_client_gen.md#json) | JSON serialization of the return value. |

#### Returns

`Promise`\<[`Void`](../modules/api_client_gen.md#void)\>
