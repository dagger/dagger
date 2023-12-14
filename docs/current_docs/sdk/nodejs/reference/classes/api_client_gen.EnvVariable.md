---
id: "api_client_gen.EnvVariable"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).EnvVariable

A simple key value object that represents an environment variable.

## Hierarchy

- `BaseClient`

  ↳ **`EnvVariable`**

## Constructors

### constructor

**new EnvVariable**(`parent?`, `_name?`, `_value?`): [`EnvVariable`](api_client_gen.EnvVariable.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_name?` | `string` |
| `_value?` | `string` |

#### Returns

[`EnvVariable`](api_client_gen.EnvVariable.md)

#### Overrides

BaseClient.constructor

## Properties

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_value

 `Private` `Optional` `Readonly` **\_value**: `string` = `undefined`

## Methods

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
