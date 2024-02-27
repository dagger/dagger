---
id: "api_client_gen.ModuleSource"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).ModuleSource

The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc.

## Hierarchy

- `BaseClient`

  ↳ **`ModuleSource`**

## Constructors

### constructor

**new ModuleSource**(`parent?`, `_id?`, `_asString?`, `_configExists?`, `_kind?`, `_moduleName?`, `_moduleOriginalName?`, `_resolveContextPathFromCaller?`, `_sourceRootSubpath?`, `_sourceSubpath?`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`ModuleSourceID`](../modules/api_client_gen.md#modulesourceid) |
| `_asString?` | `string` |
| `_configExists?` | `boolean` |
| `_kind?` | [`ModuleSourceKind`](../enums/api_client_gen.ModuleSourceKind.md) |
| `_moduleName?` | `string` |
| `_moduleOriginalName?` | `string` |
| `_resolveContextPathFromCaller?` | `string` |
| `_sourceRootSubpath?` | `string` |
| `_sourceSubpath?` | `string` |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

#### Overrides

BaseClient.constructor

## Properties

### \_asString

 `Private` `Optional` `Readonly` **\_asString**: `string` = `undefined`

___

### \_configExists

 `Private` `Optional` `Readonly` **\_configExists**: `boolean` = `undefined`

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

### \_moduleOriginalName

 `Private` `Optional` `Readonly` **\_moduleOriginalName**: `string` = `undefined`

___

### \_resolveContextPathFromCaller

 `Private` `Optional` `Readonly` **\_resolveContextPathFromCaller**: `string` = `undefined`

___

### \_sourceRootSubpath

 `Private` `Optional` `Readonly` **\_sourceRootSubpath**: `string` = `undefined`

___

### \_sourceSubpath

 `Private` `Optional` `Readonly` **\_sourceSubpath**: `string` = `undefined`

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

### configExists

**configExists**(): `Promise`\<`boolean`\>

Returns whether the module source has a configuration file.

#### Returns

`Promise`\<`boolean`\>

___

### contextDirectory

**contextDirectory**(): [`Directory`](api_client_gen.Directory.md)

The directory containing everything needed to load load and use the module.

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### dependencies

**dependencies**(): `Promise`\<[`ModuleDependency`](api_client_gen.ModuleDependency.md)[]\>

The dependencies of the module source. Includes dependencies from the configuration and any extras from withDependencies calls.

#### Returns

`Promise`\<[`ModuleDependency`](api_client_gen.ModuleDependency.md)[]\>

___

### directory

**directory**(`path`): [`Directory`](api_client_gen.Directory.md)

The directory containing the module configuration and source code (source code may be in a subdir).

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

If set, the name of the module this source references, including any overrides at runtime by callers.

#### Returns

`Promise`\<`string`\>

___

### moduleOriginalName

**moduleOriginalName**(): `Promise`\<`string`\>

The original name of the module this source references, as defined in the module configuration.

#### Returns

`Promise`\<`string`\>

___

### resolveContextPathFromCaller

**resolveContextPathFromCaller**(): `Promise`\<`string`\>

The path to the module source's context directory on the caller's filesystem. Only valid for local sources.

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

### resolveFromCaller

**resolveFromCaller**(): [`ModuleSource`](api_client_gen.ModuleSource.md)

Load the source from its path on the caller's filesystem, including only needed+configured files and directories. Only valid for local sources.

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

___

### sourceRootSubpath

**sourceRootSubpath**(): `Promise`\<`string`\>

The path relative to context of the root of the module source, which contains dagger.json. It also contains the module implementation source code, but that may or may not being a subdir of this root.

#### Returns

`Promise`\<`string`\>

___

### sourceSubpath

**sourceSubpath**(): `Promise`\<`string`\>

The path relative to context of the module implementation source code.

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

___

### withContextDirectory

**withContextDirectory**(`dir`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Update the module source with a new context directory. Only valid for local sources.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `dir` | [`Directory`](api_client_gen.Directory.md) | The directory to set as the context directory. |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

___

### withDependencies

**withDependencies**(`dependencies`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Append the provided dependencies to the module source's dependency list.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `dependencies` | [`ModuleDependency`](api_client_gen.ModuleDependency.md)[] | The dependencies to append. |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

___

### withName

**withName**(`name`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Update the module source with a new name.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name to set. |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

___

### withSDK

**withSDK**(`sdk`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Update the module source with a new SDK.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `sdk` | `string` | The SDK to set. |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

___

### withSourceSubpath

**withSourceSubpath**(`path`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Update the module source with a new source subpath.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | The path to set as the source subpath. |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)
