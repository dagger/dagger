---
id: "api_client_gen.CurrentModule"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).CurrentModule

Reflective module API provided to functions at runtime.

## Hierarchy

- `BaseClient`

  ↳ **`CurrentModule`**

## Constructors

### constructor

**new CurrentModule**(`parent?`, `_id?`, `_name?`): [`CurrentModule`](api_client_gen.CurrentModule.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`CurrentModuleID`](../modules/api_client_gen.md#currentmoduleid) |
| `_name?` | `string` |

#### Returns

[`CurrentModule`](api_client_gen.CurrentModule.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`CurrentModuleID`](../modules/api_client_gen.md#currentmoduleid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

## Methods

### id

**id**(): `Promise`\<[`CurrentModuleID`](../modules/api_client_gen.md#currentmoduleid)\>

A unique identifier for this CurrentModule.

#### Returns

`Promise`\<[`CurrentModuleID`](../modules/api_client_gen.md#currentmoduleid)\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the module being executed in

#### Returns

`Promise`\<`string`\>

___

### source

**source**(): [`Directory`](api_client_gen.Directory.md)

The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### workdir

**workdir**(`path`, `opts?`): [`Directory`](api_client_gen.Directory.md)

Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the directory to access (e.g., "."). |
| `opts?` | [`CurrentModuleWorkdirOpts`](../modules/api_client_gen.md#currentmoduleworkdiropts) | - |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### workdirFile

**workdirFile**(`path`): [`File`](api_client_gen.File.md)

Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the file to retrieve (e.g., "README.md"). |

#### Returns

[`File`](api_client_gen.File.md)
