---
id: "api_client_gen.EnvVariable"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).EnvVariable

An environment variable name and value.

## Hierarchy

- `BaseClient`

  ↳ **`EnvVariable`**

## Constructors

### constructor

**new EnvVariable**(`parent?`, `_id?`, `_name?`, `_value?`): [`EnvVariable`](api_client_gen.EnvVariable.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`EnvVariableID`](../modules/api_client_gen.md#envvariableid) |
| `_name?` | `string` |
| `_value?` | `string` |

#### Returns

[`EnvVariable`](api_client_gen.EnvVariable.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`EnvVariableID`](../modules/api_client_gen.md#envvariableid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_value

 `Private` `Optional` `Readonly` **\_value**: `string` = `undefined`

## Methods

### id

**id**(): `Promise`\<[`EnvVariableID`](../modules/api_client_gen.md#envvariableid)\>

A unique identifier for this EnvVariable.

#### Returns

`Promise`\<[`EnvVariableID`](../modules/api_client_gen.md#envvariableid)\>

___

### name

**name**(): `Promise`\<`string`\>

The environment variable name.

#### Returns

`Promise`\<`string`\>

___

### value

**value**(): `Promise`\<`string`\>

The environment variable value.

#### Returns

`Promise`\<`string`\>
