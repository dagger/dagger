---
id: "api_client_gen.ModuleConfig"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).ModuleConfig

Static configuration for a module (e.g. parsed contents of dagger.json)

## Hierarchy

- `BaseClient`

  ↳ **`ModuleConfig`**

## Constructors

### constructor

**new ModuleConfig**(`parent?`, `_name?`, `_root?`, `_sdk?`): [`ModuleConfig`](api_client_gen.ModuleConfig.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_name?` | `string` |
| `_root?` | `string` |
| `_sdk?` | `string` |

#### Returns

[`ModuleConfig`](api_client_gen.ModuleConfig.md)

#### Overrides

BaseClient.constructor

## Properties

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_root

 `Private` `Optional` `Readonly` **\_root**: `string` = `undefined`

___

### \_sdk

 `Private` `Optional` `Readonly` **\_sdk**: `string` = `undefined`

## Methods

### dependencies

**dependencies**(): `Promise`\<`string`[]\>

Modules that this module depends on.

#### Returns

`Promise`\<`string`[]\>

___

### exclude

**exclude**(): `Promise`\<`string`[]\>

Exclude these file globs when loading the module root.

#### Returns

`Promise`\<`string`[]\>

___

### include

**include**(): `Promise`\<`string`[]\>

Include only these file globs when loading the module root.

#### Returns

`Promise`\<`string`[]\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the module.

#### Returns

`Promise`\<`string`\>

___

### root

**root**(): `Promise`\<`string`\>

The root directory of the module's project, which may be above the module source code.

#### Returns

`Promise`\<`string`\>

___

### sdk

**sdk**(): `Promise`\<`string`\>

Either the name of a built-in SDK ('go', 'python', etc.) OR a module reference pointing to the SDK's module implementation.

#### Returns

`Promise`\<`string`\>
