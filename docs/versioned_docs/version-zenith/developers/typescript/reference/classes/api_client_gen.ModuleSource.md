---
id: "api_client_gen.ModuleSource"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).ModuleSource

The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc.

## Hierarchy

- `BaseClient`

  ↳ **`ModuleSource`**

## Constructors

### constructor

**new ModuleSource**(`parent?`, `_id?`, `_asString?`, `_kind?`, `_moduleName?`, `_subpath?`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`ModuleSourceID`](../modules/api_client_gen.md#modulesourceid) |
| `_asString?` | `string` |
| `_kind?` | [`ModuleSourceKind`](../enums/api_client_gen.ModuleSourceKind.md) |
| `_moduleName?` | `string` |
| `_subpath?` | `string` |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

#### Overrides

BaseClient.constructor

## Properties

### \_asString

 `Private` `Optional` `Readonly` **\_asString**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`ModuleSourceID`](../modules/api_client_gen.md#modulesourceid) = `undefined`

___

### \_kind

 `Private` `Optional` `Readonly` **\_kind**: [`ModuleSourceKind`](../enums/api_client_gen.ModuleSourceKind.md) = `undefined`

___

### \_moduleName

 `Private` `Optional` `Readonly` **\_moduleName**: `string` = `undefined`

___

### \_subpath

 `Private` `Optional` `Readonly` **\_subpath**: `string` = `undefined`

## Methods

### asGitSource

**asGitSource**(): [`GitModuleSource`](api_client_gen.GitModuleSource.md)

If the source is a of kind git, the git source representation of it.

#### Returns

[`GitModuleSource`](api_client_gen.GitModuleSource.md)

___

### asLocalSource

**asLocalSource**(): [`LocalModuleSource`](api_client_gen.LocalModuleSource.md)

If the source is of kind local, the local source representation of it.

#### Returns

[`LocalModuleSource`](api_client_gen.LocalModuleSource.md)

___

### asModule

**asModule**(): [`Module_`](api_client_gen.Module_.md)

Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### asString

**asString**(): `Promise`\<`string`\>

A human readable ref string representation of this module source.

#### Returns

`Promise`\<`string`\>

___

### directory

**directory**(`path`): [`Directory`](api_client_gen.Directory.md)

The directory containing the actual module's source code, as determined from the root directory and subpath.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | The path from the source directory to select. |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### id

**id**(): `Promise`\<[`ModuleSourceID`](../modules/api_client_gen.md#modulesourceid)\>

A unique identifier for this ModuleSource.

#### Returns

`Promise`\<[`ModuleSourceID`](../modules/api_client_gen.md#modulesourceid)\>

___

### kind

**kind**(): `Promise`\<[`ModuleSourceKind`](../enums/api_client_gen.ModuleSourceKind.md)\>

The kind of source (e.g. local, git, etc.)

#### Returns

`Promise`\<[`ModuleSourceKind`](../enums/api_client_gen.ModuleSourceKind.md)\>

___

### moduleName

**moduleName**(): `Promise`\<`string`\>

If set, the name of the module this source references

#### Returns

`Promise`\<`string`\>

___

### resolveDependency

**resolveDependency**(`dep`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Resolve the provided module source arg as a dependency relative to this module source.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `dep` | [`ModuleSource`](api_client_gen.ModuleSource.md) | The dependency module source to resolve. |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

___

### rootDirectory

**rootDirectory**(): [`Directory`](api_client_gen.Directory.md)

The root directory of the module source that contains its configuration and source code (which may be in a subdirectory of this root).

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### subpath

**subpath**(): `Promise`\<`string`\>

The path to the module subdirectory containing the actual module's source code.

#### Returns

`Promise`\<`string`\>

___

### with

**with**(`arg`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Call the provided function with current ModuleSource.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`ModuleSource`](api_client_gen.ModuleSource.md)) => [`ModuleSource`](api_client_gen.ModuleSource.md) |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)
