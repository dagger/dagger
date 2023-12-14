---
id: "api_client_gen.File"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).File

A file.

## Hierarchy

- `BaseClient`

  ↳ **`File`**

## Constructors

### constructor

**new File**(`parent?`, `_id?`, `_contents?`, `_export?`, `_size?`, `_sync?`): [`File`](api_client_gen.File.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`FileID`](../modules/api_client_gen.md#fileid) |
| `_contents?` | `string` |
| `_export?` | `boolean` |
| `_size?` | `number` |
| `_sync?` | [`FileID`](../modules/api_client_gen.md#fileid) |

#### Returns

[`File`](api_client_gen.File.md)

#### Overrides

BaseClient.constructor

## Properties

### \_contents

 `Private` `Optional` `Readonly` **\_contents**: `string` = `undefined`

___

### \_export

 `Private` `Optional` `Readonly` **\_export**: `boolean` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`FileID`](../modules/api_client_gen.md#fileid) = `undefined`

___

### \_size

 `Private` `Optional` `Readonly` **\_size**: `number` = `undefined`

___

### \_sync

 `Private` `Optional` `Readonly` **\_sync**: [`FileID`](../modules/api_client_gen.md#fileid) = `undefined`

## Methods

### contents

**contents**(): `Promise`\<`string`\>

Retrieves the contents of the file.

#### Returns

`Promise`\<`string`\>

___

### export

**export**(`path`, `opts?`): `Promise`\<`boolean`\>

Writes the file to a file path on the host.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the written directory (e.g., "output.txt"). |
| `opts?` | [`FileExportOpts`](../modules/api_client_gen.md#fileexportopts) | - |

#### Returns

`Promise`\<`boolean`\>

___

### id

**id**(): `Promise`\<[`FileID`](../modules/api_client_gen.md#fileid)\>

Retrieves the content-addressed identifier of the file.

#### Returns

`Promise`\<[`FileID`](../modules/api_client_gen.md#fileid)\>

___

### size

**size**(): `Promise`\<`number`\>

Gets the size of the file, in bytes.

#### Returns

`Promise`\<`number`\>

___

### sync

**sync**(): `Promise`\<[`File`](api_client_gen.File.md)\>

Force evaluation in the engine.

#### Returns

`Promise`\<[`File`](api_client_gen.File.md)\>

___

### with

**with**(`arg`): [`File`](api_client_gen.File.md)

Call the provided function with current File.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`File`](api_client_gen.File.md)) => [`File`](api_client_gen.File.md) |

#### Returns

[`File`](api_client_gen.File.md)

___

### withTimestamps

**withTimestamps**(`timestamp`): [`File`](api_client_gen.File.md)

Retrieves this file with its created/modified timestamps set to the given time.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `timestamp` | `number` | Timestamp to set dir/files in. Formatted in seconds following Unix epoch (e.g., 1672531199). |

#### Returns

[`File`](api_client_gen.File.md)
