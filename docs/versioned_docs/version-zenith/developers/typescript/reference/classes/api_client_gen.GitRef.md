---
id: "api_client_gen.GitRef"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).GitRef

A git ref (tag, branch, or commit).

## Hierarchy

- `BaseClient`

  ↳ **`GitRef`**

## Constructors

### constructor

**new GitRef**(`parent?`, `_id?`, `_commit?`): [`GitRef`](api_client_gen.GitRef.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`GitRefID`](../modules/api_client_gen.md#gitrefid) |
| `_commit?` | `string` |

#### Returns

[`GitRef`](api_client_gen.GitRef.md)

#### Overrides

BaseClient.constructor

## Properties

### \_commit

 `Private` `Optional` `Readonly` **\_commit**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`GitRefID`](../modules/api_client_gen.md#gitrefid) = `undefined`

## Methods

### commit

**commit**(): `Promise`\<`string`\>

The resolved commit id at this ref.

#### Returns

`Promise`\<`string`\>

___

### id

**id**(): `Promise`\<[`GitRefID`](../modules/api_client_gen.md#gitrefid)\>

A unique identifier for this GitRef.

#### Returns

`Promise`\<[`GitRefID`](../modules/api_client_gen.md#gitrefid)\>

___

### tree

**tree**(`opts?`): [`Directory`](api_client_gen.Directory.md)

The filesystem tree at this ref.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`GitRefTreeOpts`](../modules/api_client_gen.md#gitreftreeopts) |

#### Returns

[`Directory`](api_client_gen.Directory.md)
