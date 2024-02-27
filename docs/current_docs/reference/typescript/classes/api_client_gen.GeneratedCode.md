---
id: "api_client_gen.GeneratedCode"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).GeneratedCode

The result of running an SDK's codegen.

## Hierarchy

- `BaseClient`

  ↳ **`GeneratedCode`**

## Constructors

### constructor

**new GeneratedCode**(`parent?`, `_id?`): [`GeneratedCode`](api_client_gen.GeneratedCode.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`GeneratedCodeID`](../modules/api_client_gen.md#generatedcodeid) |

#### Returns

[`GeneratedCode`](api_client_gen.GeneratedCode.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`GeneratedCodeID`](../modules/api_client_gen.md#generatedcodeid) = `undefined`

## Methods

### code

**code**(): [`Directory`](api_client_gen.Directory.md)

The directory containing the generated code.

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### id

**id**(): `Promise`\<[`GeneratedCodeID`](../modules/api_client_gen.md#generatedcodeid)\>

A unique identifier for this GeneratedCode.

#### Returns

`Promise`\<[`GeneratedCodeID`](../modules/api_client_gen.md#generatedcodeid)\>

___

### vcsGeneratedPaths

**vcsGeneratedPaths**(): `Promise`\<`string`[]\>

List of paths to mark generated in version control (i.e. .gitattributes).

#### Returns

`Promise`\<`string`[]\>

___

### vcsIgnoredPaths

**vcsIgnoredPaths**(): `Promise`\<`string`[]\>

List of paths to ignore in version control (i.e. .gitignore).

#### Returns

`Promise`\<`string`[]\>

___

### with

**with**(`arg`): [`GeneratedCode`](api_client_gen.GeneratedCode.md)

Call the provided function with current GeneratedCode.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`GeneratedCode`](api_client_gen.GeneratedCode.md)) => [`GeneratedCode`](api_client_gen.GeneratedCode.md) |

#### Returns

[`GeneratedCode`](api_client_gen.GeneratedCode.md)

___

### withVCSGeneratedPaths

**withVCSGeneratedPaths**(`paths`): [`GeneratedCode`](api_client_gen.GeneratedCode.md)

Set the list of paths to mark generated in version control.

#### Parameters

| Name | Type |
| :------ | :------ |
| `paths` | `string`[] |

#### Returns

[`GeneratedCode`](api_client_gen.GeneratedCode.md)

___

### withVCSIgnoredPaths

**withVCSIgnoredPaths**(`paths`): [`GeneratedCode`](api_client_gen.GeneratedCode.md)

Set the list of paths to ignore in version control.

#### Parameters

| Name | Type |
| :------ | :------ |
| `paths` | `string`[] |

#### Returns

[`GeneratedCode`](api_client_gen.GeneratedCode.md)
