---
id: "api_client_gen.ModuleDependency"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).ModuleDependency

The configuration of dependency of a module.

## Hierarchy

- `BaseClient`

  ↳ **`ModuleDependency`**

## Constructors

### constructor

**new ModuleDependency**(`parent?`, `_id?`, `_name?`): [`ModuleDependency`](api_client_gen.ModuleDependency.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`ModuleDependencyID`](../modules/api_client_gen.md#moduledependencyid) |
| `_name?` | `string` |

#### Returns

[`ModuleDependency`](api_client_gen.ModuleDependency.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`ModuleDependencyID`](../modules/api_client_gen.md#moduledependencyid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

## Methods

### id

**id**(): `Promise`\<[`ModuleDependencyID`](../modules/api_client_gen.md#moduledependencyid)\>

A unique identifier for this ModuleDependency.

#### Returns

`Promise`\<[`ModuleDependencyID`](../modules/api_client_gen.md#moduledependencyid)\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the dependency module.

#### Returns

`Promise`\<`string`\>

___

### source

**source**(): [`ModuleSource`](api_client_gen.ModuleSource.md)

The source for the dependency module.

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)
