---
id: "api_client_gen.Label"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Label

A simple key value object that represents a label.

## Hierarchy

- `BaseClient`

  ↳ **`Label`**

## Constructors

### constructor

**new Label**(`parent?`, `_name?`, `_value?`): [`Label`](api_client_gen.Label.md)

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

[`Label`](api_client_gen.Label.md)

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

The label name.

#### Returns

`Promise`\<`string`\>

___

### value

**value**(): `Promise`\<`string`\>

The label value.

#### Returns

`Promise`\<`string`\>
