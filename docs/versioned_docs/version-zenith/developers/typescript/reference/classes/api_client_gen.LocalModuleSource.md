---
id: "api_client_gen.LocalModuleSource"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).LocalModuleSource

Module source that that originates from a path locally relative to an arbitrary directory.

## Hierarchy

- `BaseClient`

  ↳ **`LocalModuleSource`**

## Constructors

### constructor

**new LocalModuleSource**(`parent?`, `_id?`, `_sourceSubpath?`): [`LocalModuleSource`](api_client_gen.LocalModuleSource.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid) |
| `_sourceSubpath?` | `string` |

#### Returns

[`LocalModuleSource`](api_client_gen.LocalModuleSource.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid) = `undefined`

___

### \_sourceSubpath

 `Private` `Optional` `Readonly` **\_sourceSubpath**: `string` = `undefined`

## Methods

### id

**id**(): `Promise`\<[`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid)\>

A unique identifier for this LocalModuleSource.

#### Returns

`Promise`\<[`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid)\>

___

### sourceSubpath

**sourceSubpath**(): `Promise`\<`string`\>

The path to the module source code dir specified by this source.

#### Returns

`Promise`\<`string`\>
