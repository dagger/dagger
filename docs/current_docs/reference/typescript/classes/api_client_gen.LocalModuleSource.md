---
id: "api_client_gen.LocalModuleSource"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).LocalModuleSource

Module source that that originates from a path locally relative to an arbitrary directory.

## Hierarchy

- `BaseClient`

  ↳ **`LocalModuleSource`**

## Constructors

### constructor

**new LocalModuleSource**(`parent?`, `_id?`, `_rootSubpath?`): [`LocalModuleSource`](api_client_gen.LocalModuleSource.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid) |
| `_rootSubpath?` | `string` |

#### Returns

[`LocalModuleSource`](api_client_gen.LocalModuleSource.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid) = `undefined`

___

### \_rootSubpath

 `Private` `Optional` `Readonly` **\_rootSubpath**: `string` = `undefined`

## Methods

### contextDirectory

**contextDirectory**(): [`Directory`](api_client_gen.Directory.md)

The directory containing everything needed to load load and use the module.

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### id

**id**(): `Promise`\<[`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid)\>

A unique identifier for this LocalModuleSource.

#### Returns

`Promise`\<[`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid)\>

___

### rootSubpath

**rootSubpath**(): `Promise`\<`string`\>

The path to the root of the module source under the context directory. This directory contains its configuration file. It also contains its source code (possibly as a subdirectory).

#### Returns

`Promise`\<`string`\>
