---
id: "api_client_gen.Module_"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).Module_

A Dagger module.

## Hierarchy

- `BaseClient`

  ↳ **`Module_`**

## Constructors

### constructor

**new Module_**(`parent?`, `_id?`, `_description?`, `_name?`, `_sdk?`, `_serve?`): [`Module_`](api_client_gen.Module_.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`ModuleID`](../modules/api_client_gen.md#moduleid) |
| `_description?` | `string` |
| `_name?` | `string` |
| `_sdk?` | `string` |
| `_serve?` | [`Void`](../modules/api_client_gen.md#void) |

#### Returns

[`Module_`](api_client_gen.Module_.md)

#### Overrides

BaseClient.constructor

## Properties

### \_description

 `Private` `Optional` `Readonly` **\_description**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`ModuleID`](../modules/api_client_gen.md#moduleid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_sdk

 `Private` `Optional` `Readonly` **\_sdk**: `string` = `undefined`

___

### \_serve

 `Private` `Optional` `Readonly` **\_serve**: [`Void`](../modules/api_client_gen.md#void) = `undefined`

## Methods

### dependencies

**dependencies**(): `Promise`\<[`Module_`](api_client_gen.Module_.md)[]\>

Modules used by this module.

#### Returns

`Promise`\<[`Module_`](api_client_gen.Module_.md)[]\>

___

### dependencyConfig

**dependencyConfig**(): `Promise`\<[`ModuleDependency`](api_client_gen.ModuleDependency.md)[]\>

The dependencies as configured by the module.

#### Returns

`Promise`\<[`ModuleDependency`](api_client_gen.ModuleDependency.md)[]\>

___

### description

**description**(): `Promise`\<`string`\>

The doc string of the module, if any

#### Returns

`Promise`\<`string`\>

___

### generatedSourceRootDirectory

**generatedSourceRootDirectory**(): [`Directory`](api_client_gen.Directory.md)

The module's root directory containing the config file for it and its source (possibly as a subdir). It includes any generated code or updated config files created after initial load, but not any files/directories that were unchanged after sdk codegen was run.

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### id

**id**(): `Promise`\<[`ModuleID`](../modules/api_client_gen.md#moduleid)\>

A unique identifier for this Module.

#### Returns

`Promise`\<[`ModuleID`](../modules/api_client_gen.md#moduleid)\>

___

### initialize

**initialize**(): [`Module_`](api_client_gen.Module_.md)

Retrieves the module with the objects loaded via its SDK.

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### interfaces

**interfaces**(): `Promise`\<[`TypeDef`](api_client_gen.TypeDef.md)[]\>

Interfaces served by this module.

#### Returns

`Promise`\<[`TypeDef`](api_client_gen.TypeDef.md)[]\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the module

#### Returns

`Promise`\<`string`\>

___

### objects

**objects**(): `Promise`\<[`TypeDef`](api_client_gen.TypeDef.md)[]\>

Objects served by this module.

#### Returns

`Promise`\<[`TypeDef`](api_client_gen.TypeDef.md)[]\>

___

### runtime

**runtime**(): [`Container`](api_client_gen.Container.md)

The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.

#### Returns

[`Container`](api_client_gen.Container.md)

___

### sdk

**sdk**(): `Promise`\<`string`\>

The SDK used by this module. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation.

#### Returns

`Promise`\<`string`\>

___

### serve

**serve**(): `Promise`\<[`Void`](../modules/api_client_gen.md#void)\>

Serve a module's API in the current session.

Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.

#### Returns

`Promise`\<[`Void`](../modules/api_client_gen.md#void)\>

___

### source

**source**(): [`ModuleSource`](api_client_gen.ModuleSource.md)

The source for the module.

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

___

### with

**with**(`arg`): [`Module_`](api_client_gen.Module_.md)

Call the provided function with current Module.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`Module_`](api_client_gen.Module_.md)) => [`Module_`](api_client_gen.Module_.md) |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### withDependencies

**withDependencies**(`dependencies`): [`Module_`](api_client_gen.Module_.md)

Update the module configuration to use the given dependencies.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `dependencies` | [`ModuleDependency`](api_client_gen.ModuleDependency.md)[] | The dependency modules to install. |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### withDescription

**withDescription**(`description`): [`Module_`](api_client_gen.Module_.md)

Retrieves the module with the given description

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `description` | `string` | The description to set |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### withInterface

**withInterface**(`iface`): [`Module_`](api_client_gen.Module_.md)

This module plus the given Interface type and associated functions

#### Parameters

| Name | Type |
| :------ | :------ |
| `iface` | [`TypeDef`](api_client_gen.TypeDef.md) |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### withName

**withName**(`name`): [`Module_`](api_client_gen.Module_.md)

Update the module configuration to use the given name.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name to use. |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### withObject

**withObject**(`object`): [`Module_`](api_client_gen.Module_.md)

This module plus the given Object type and associated functions.

#### Parameters

| Name | Type |
| :------ | :------ |
| `object` | [`TypeDef`](api_client_gen.TypeDef.md) |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### withSDK

**withSDK**(`sdk`): [`Module_`](api_client_gen.Module_.md)

Update the module configuration to use the given SDK.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `sdk` | `string` | The SDK to use. |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### withSource

**withSource**(`source`): [`Module_`](api_client_gen.Module_.md)

Retrieves the module with basic configuration loaded if present.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `source` | [`ModuleSource`](api_client_gen.ModuleSource.md) | The module source to initialize from. |

#### Returns

[`Module_`](api_client_gen.Module_.md)
