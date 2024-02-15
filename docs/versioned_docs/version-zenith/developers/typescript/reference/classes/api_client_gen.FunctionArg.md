---
id: "api_client_gen.FunctionArg"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).FunctionArg

An argument accepted by a function.

This is a specification for an argument at function definition time, not an argument passed at function call time.

## Hierarchy

- `BaseClient`

  ↳ **`FunctionArg`**

## Constructors

### constructor

**new FunctionArg**(`parent?`, `_id?`, `_defaultValue?`, `_description?`, `_name?`): [`FunctionArg`](api_client_gen.FunctionArg.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`FunctionArgID`](../modules/api_client_gen.md#functionargid) |
| `_defaultValue?` | [`JSON`](../modules/api_client_gen.md#json) |
| `_description?` | `string` |
| `_name?` | `string` |

#### Returns

[`FunctionArg`](api_client_gen.FunctionArg.md)

#### Overrides

BaseClient.constructor

## Properties

### \_defaultValue

 `Private` `Optional` `Readonly` **\_defaultValue**: [`JSON`](../modules/api_client_gen.md#json) = `undefined`

___

### \_description

 `Private` `Optional` `Readonly` **\_description**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`FunctionArgID`](../modules/api_client_gen.md#functionargid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

## Methods

### defaultValue

**defaultValue**(): `Promise`\<[`JSON`](../modules/api_client_gen.md#json)\>

A default value to use for this argument when not explicitly set by the caller, if any.

#### Returns

`Promise`\<[`JSON`](../modules/api_client_gen.md#json)\>

___

### description

**description**(): `Promise`\<`string`\>

A doc string for the argument, if any.

#### Returns

`Promise`\<`string`\>

___

### id

**id**(): `Promise`\<[`FunctionArgID`](../modules/api_client_gen.md#functionargid)\>

A unique identifier for this FunctionArg.

#### Returns

`Promise`\<[`FunctionArgID`](../modules/api_client_gen.md#functionargid)\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the argument in lowerCamelCase format.

#### Returns

`Promise`\<`string`\>

___

### typeDef

**typeDef**(): [`TypeDef`](api_client_gen.TypeDef.md)

The type of the argument.

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)
