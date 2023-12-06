---
id: "api_client_gen.Secret"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Secret

A reference to a secret value, which can be handled more safely than the value itself.

## Hierarchy

- `BaseClient`

  ↳ **`Secret`**

## Constructors

### constructor

**new Secret**(`parent?`, `_id?`, `_plaintext?`): [`Secret`](api_client_gen.Secret.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`SecretID`](../modules/api_client_gen.md#secretid) |
| `_plaintext?` | `string` |

#### Returns

[`Secret`](api_client_gen.Secret.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`SecretID`](../modules/api_client_gen.md#secretid) = `undefined`

___

### \_plaintext

 `Private` `Optional` `Readonly` **\_plaintext**: `string` = `undefined`

## Methods

### id

**id**(): `Promise`\<[`SecretID`](../modules/api_client_gen.md#secretid)\>

The identifier for this secret.

#### Returns

`Promise`\<[`SecretID`](../modules/api_client_gen.md#secretid)\>

___

### plaintext

**plaintext**(): `Promise`\<`string`\>

The value of this secret.

#### Returns

`Promise`\<`string`\>
