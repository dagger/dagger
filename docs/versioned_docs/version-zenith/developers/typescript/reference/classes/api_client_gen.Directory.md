---
id: "api_client_gen.Directory"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).Directory

A directory.

## Hierarchy

- `BaseClient`

  ↳ **`Directory`**

## Constructors

### constructor

**new Directory**(`parent?`, `_id?`, `_export?`, `_sync?`): [`Directory`](api_client_gen.Directory.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`DirectoryID`](../modules/api_client_gen.md#directoryid) |
| `_export?` | `boolean` |
| `_sync?` | [`DirectoryID`](../modules/api_client_gen.md#directoryid) |

#### Returns

[`Directory`](api_client_gen.Directory.md)

#### Overrides

BaseClient.constructor

## Properties

### \_export

 `Private` `Optional` `Readonly` **\_export**: `boolean` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`DirectoryID`](../modules/api_client_gen.md#directoryid) = `undefined`

___

### \_sync

 `Private` `Optional` `Readonly` **\_sync**: [`DirectoryID`](../modules/api_client_gen.md#directoryid) = `undefined`

## Methods

### asModule

**asModule**(`opts?`): [`Module_`](api_client_gen.Module_.md)

Load the directory as a Dagger module

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`DirectoryAsModuleOpts`](../modules/api_client_gen.md#directoryasmoduleopts) |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### diff

**diff**(`other`): [`Directory`](api_client_gen.Directory.md)

Gets the difference between this directory and an another directory.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `other` | [`Directory`](api_client_gen.Directory.md) | Identifier of the directory to compare. |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### directory

**directory**(`path`): [`Directory`](api_client_gen.Directory.md)

Retrieves a directory at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the directory to retrieve (e.g., "/src"). |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### dockerBuild

**dockerBuild**(`opts?`): [`Container`](api_client_gen.Container.md)

Builds a new Docker container from this directory.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`DirectoryDockerBuildOpts`](../modules/api_client_gen.md#directorydockerbuildopts) |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### entries

**entries**(`opts?`): `Promise`\<`string`[]\>

Returns a list of files and directories at the given path.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`DirectoryEntriesOpts`](../modules/api_client_gen.md#directoryentriesopts) |

#### Returns

`Promise`\<`string`[]\>

___

### export

**export**(`path`): `Promise`\<`boolean`\>

Writes the contents of the directory to a path on the host.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the copied directory (e.g., "logs/"). |

#### Returns

`Promise`\<`boolean`\>

___

### file

**file**(`path`): [`File`](api_client_gen.File.md)

Retrieves a file at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the file to retrieve (e.g., "README.md"). |

#### Returns

[`File`](api_client_gen.File.md)

___

### glob

**glob**(`pattern`): `Promise`\<`string`[]\>

Returns a list of files and directories that matche the given pattern.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `pattern` | `string` | Pattern to match (e.g., "*.md"). |

#### Returns

`Promise`\<`string`[]\>

___

### id

**id**(): `Promise`\<[`DirectoryID`](../modules/api_client_gen.md#directoryid)\>

A unique identifier for this Directory.

#### Returns

`Promise`\<[`DirectoryID`](../modules/api_client_gen.md#directoryid)\>

___

### pipeline

**pipeline**(`name`, `opts?`): [`Directory`](api_client_gen.Directory.md)

Creates a named sub-pipeline.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | Name of the sub-pipeline. |
| `opts?` | [`DirectoryPipelineOpts`](../modules/api_client_gen.md#directorypipelineopts) | - |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### sync

**sync**(): `Promise`\<[`Directory`](api_client_gen.Directory.md)\>

Force evaluation in the engine.

#### Returns

`Promise`\<[`Directory`](api_client_gen.Directory.md)\>

___

### with

**with**(`arg`): [`Directory`](api_client_gen.Directory.md)

Call the provided function with current Directory.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`Directory`](api_client_gen.Directory.md)) => [`Directory`](api_client_gen.Directory.md) |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### withDirectory

**withDirectory**(`path`, `directory`, `opts?`): [`Directory`](api_client_gen.Directory.md)

Retrieves this directory plus a directory written at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the written directory (e.g., "/src/"). |
| `directory` | [`Directory`](api_client_gen.Directory.md) | Identifier of the directory to copy. |
| `opts?` | [`DirectoryWithDirectoryOpts`](../modules/api_client_gen.md#directorywithdirectoryopts) | - |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### withFile

**withFile**(`path`, `source`, `opts?`): [`Directory`](api_client_gen.Directory.md)

Retrieves this directory plus the contents of the given file copied to the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the copied file (e.g., "/file.txt"). |
| `source` | [`File`](api_client_gen.File.md) | Identifier of the file to copy. |
| `opts?` | [`DirectoryWithFileOpts`](../modules/api_client_gen.md#directorywithfileopts) | - |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### withNewDirectory

**withNewDirectory**(`path`, `opts?`): [`Directory`](api_client_gen.Directory.md)

Retrieves this directory plus a new directory created at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the directory created (e.g., "/logs"). |
| `opts?` | [`DirectoryWithNewDirectoryOpts`](../modules/api_client_gen.md#directorywithnewdirectoryopts) | - |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### withNewFile

**withNewFile**(`path`, `contents`, `opts?`): [`Directory`](api_client_gen.Directory.md)

Retrieves this directory plus a new file written at the given path.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the written file (e.g., "/file.txt"). |
| `contents` | `string` | Content of the written file (e.g., "Hello world!"). |
| `opts?` | [`DirectoryWithNewFileOpts`](../modules/api_client_gen.md#directorywithnewfileopts) | - |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### withTimestamps

**withTimestamps**(`timestamp`): [`Directory`](api_client_gen.Directory.md)

Retrieves this directory with all file/dir timestamps set to the given time.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `timestamp` | `number` | Timestamp to set dir/files in. Formatted in seconds following Unix epoch (e.g., 1672531199). |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### withoutDirectory

**withoutDirectory**(`path`): [`Directory`](api_client_gen.Directory.md)

Retrieves this directory with the directory at the given path removed.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the directory to remove (e.g., ".github/"). |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### withoutFile

**withoutFile**(`path`): [`Directory`](api_client_gen.Directory.md)

Retrieves this directory with the file at the given path removed.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `path` | `string` | Location of the file to remove (e.g., "/file.txt"). |

#### Returns

[`Directory`](api_client_gen.Directory.md)
