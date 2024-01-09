---
id: "api_client_gen.Client"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Client

## Hierarchy

- `BaseClient`

  ↳ **`Client`**

## Constructors

### constructor

**new Client**(`parent?`, `_checkVersionCompatibility?`, `_defaultPlatform?`): [`Client`](api_client_gen.Client.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_checkVersionCompatibility?` | `boolean` |
| `_defaultPlatform?` | [`Platform`](../modules/api_client_gen.md#platform) |

#### Returns

[`Client`](api_client_gen.Client.md)

#### Overrides

BaseClient.constructor

## Properties

### \_checkVersionCompatibility

 `Private` `Optional` `Readonly` **\_checkVersionCompatibility**: `boolean` = `undefined`

___

### \_defaultPlatform

 `Private` `Optional` `Readonly` **\_defaultPlatform**: [`Platform`](../modules/api_client_gen.md#platform) = `undefined`

## Methods

### cacheVolume

**cacheVolume**(`key`): [`CacheVolume`](api_client_gen.CacheVolume.md)

Constructs a cache volume for a given cache key.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `key` | `string` | A string identifier to target this cache volume (e.g., "modules-cache"). |

#### Returns

[`CacheVolume`](api_client_gen.CacheVolume.md)

___

### checkVersionCompatibility

**checkVersionCompatibility**(`version`): `Promise`\<`boolean`\>

Checks if the current Dagger Engine is compatible with an SDK's required version.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `version` | `string` | The SDK's required version. |

#### Returns

`Promise`\<`boolean`\>

___

### container

**container**(`opts?`): [`Container`](api_client_gen.Container.md)

Creates a scratch container or loads one by ID.

Optional platform argument initializes new containers to execute and publish
as that platform. Platform defaults to that of the builder's host.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ClientContainerOpts`](../modules/api_client_gen.md#clientcontaineropts) |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### currentFunctionCall

**currentFunctionCall**(): [`FunctionCall`](api_client_gen.FunctionCall.md)

The FunctionCall context that the SDK caller is currently executing in.
If the caller is not currently executing in a function, this will return
an error.

#### Returns

[`FunctionCall`](api_client_gen.FunctionCall.md)

___

### currentModule

**currentModule**(): [`Module_`](api_client_gen.Module_.md)

The module currently being served in the session, if any.

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### currentTypeDefs

**currentTypeDefs**(): `Promise`\<[`TypeDef`](api_client_gen.TypeDef.md)[]\>

The TypeDef representations of the objects currently being served in the session.

#### Returns

`Promise`\<[`TypeDef`](api_client_gen.TypeDef.md)[]\>

___

### defaultPlatform

**defaultPlatform**(): `Promise`\<[`Platform`](../modules/api_client_gen.md#platform)\>

The default platform of the builder.

#### Returns

`Promise`\<[`Platform`](../modules/api_client_gen.md#platform)\>

___

### directory

**directory**(`opts?`): [`Directory`](api_client_gen.Directory.md)

Creates an empty directory or loads one by ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ClientDirectoryOpts`](../modules/api_client_gen.md#clientdirectoryopts) |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### file

**file**(`id`): [`File`](api_client_gen.File.md)

Loads a file by ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FileID`](../modules/api_client_gen.md#fileid) |

#### Returns

[`File`](api_client_gen.File.md)

**`Deprecated`**

Use loadFileFromID instead.

___

### function\_

**function_**(`name`, `returnType`): [`Function_`](api_client_gen.Function_.md)

Create a function.

#### Parameters

| Name | Type |
| :------ | :------ |
| `name` | `string` |
| `returnType` | [`TypeDef`](api_client_gen.TypeDef.md) |

#### Returns

[`Function_`](api_client_gen.Function_.md)

___

### generatedCode

**generatedCode**(`code`): [`GeneratedCode`](api_client_gen.GeneratedCode.md)

Create a code generation result, given a directory containing the generated
code.

#### Parameters

| Name | Type |
| :------ | :------ |
| `code` | [`Directory`](api_client_gen.Directory.md) |

#### Returns

[`GeneratedCode`](api_client_gen.GeneratedCode.md)

___

### git

**git**(`url`, `opts?`): [`GitRepository`](api_client_gen.GitRepository.md)

Queries a git repository.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `url` | `string` | Url of the git repository. Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}` Suffix ".git" is optional. |
| `opts?` | [`ClientGitOpts`](../modules/api_client_gen.md#clientgitopts) | - |

#### Returns

[`GitRepository`](api_client_gen.GitRepository.md)

___

### host

**host**(): [`Host`](api_client_gen.Host.md)

Queries the host environment.

#### Returns

[`Host`](api_client_gen.Host.md)

___

### http

**http**(`url`, `opts?`): [`File`](api_client_gen.File.md)

Returns a file containing an http remote url content.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `url` | `string` | HTTP url to get the content from (e.g., "https://docs.dagger.io"). |
| `opts?` | [`ClientHttpOpts`](../modules/api_client_gen.md#clienthttpopts) | - |

#### Returns

[`File`](api_client_gen.File.md)

___

### loadCacheVolumeFromID

**loadCacheVolumeFromID**(`id`): [`CacheVolume`](api_client_gen.CacheVolume.md)

Load a CacheVolume from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`CacheVolumeID`](../modules/api_client_gen.md#cachevolumeid) |

#### Returns

[`CacheVolume`](api_client_gen.CacheVolume.md)

___

### loadContainerFromID

**loadContainerFromID**(`id`): [`Container`](api_client_gen.Container.md)

Loads a container from an ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ContainerID`](../modules/api_client_gen.md#containerid) |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### loadDirectoryFromID

**loadDirectoryFromID**(`id`): [`Directory`](api_client_gen.Directory.md)

Load a Directory from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`DirectoryID`](../modules/api_client_gen.md#directoryid) |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### loadFileFromID

**loadFileFromID**(`id`): [`File`](api_client_gen.File.md)

Load a File from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FileID`](../modules/api_client_gen.md#fileid) |

#### Returns

[`File`](api_client_gen.File.md)

___

### loadFunctionArgFromID

**loadFunctionArgFromID**(`id`): [`FunctionArg`](api_client_gen.FunctionArg.md)

Load a function argument by ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FunctionArgID`](../modules/api_client_gen.md#functionargid) |

#### Returns

[`FunctionArg`](api_client_gen.FunctionArg.md)

___

### loadFunctionFromID

**loadFunctionFromID**(`id`): [`Function_`](api_client_gen.Function_.md)

Load a function by ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FunctionID`](../modules/api_client_gen.md#functionid) |

#### Returns

[`Function_`](api_client_gen.Function_.md)

___

### loadGeneratedCodeFromID

**loadGeneratedCodeFromID**(`id`): [`GeneratedCode`](api_client_gen.GeneratedCode.md)

Load a GeneratedCode by ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`GeneratedCodeID`](../modules/api_client_gen.md#generatedcodeid) |

#### Returns

[`GeneratedCode`](api_client_gen.GeneratedCode.md)

___

### loadGitRefFromID

**loadGitRefFromID**(`id`): [`GitRef`](api_client_gen.GitRef.md)

Load a git ref from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`GitRefID`](../modules/api_client_gen.md#gitrefid) |

#### Returns

[`GitRef`](api_client_gen.GitRef.md)

___

### loadGitRepositoryFromID

**loadGitRepositoryFromID**(`id`): [`GitRepository`](api_client_gen.GitRepository.md)

Load a git repository from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`GitRepositoryID`](../modules/api_client_gen.md#gitrepositoryid) |

#### Returns

[`GitRepository`](api_client_gen.GitRepository.md)

___

### loadModuleFromID

**loadModuleFromID**(`id`): [`Module_`](api_client_gen.Module_.md)

Load a module by ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ModuleID`](../modules/api_client_gen.md#moduleid) |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### loadSecretFromID

**loadSecretFromID**(`id`): [`Secret`](api_client_gen.Secret.md)

Load a Secret from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`SecretID`](../modules/api_client_gen.md#secretid) |

#### Returns

[`Secret`](api_client_gen.Secret.md)

___

### loadServiceFromID

**loadServiceFromID**(`id`): [`Service`](api_client_gen.Service.md)

Loads a service from ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ServiceID`](../modules/api_client_gen.md#serviceid) |

#### Returns

[`Service`](api_client_gen.Service.md)

___

### loadSocketFromID

**loadSocketFromID**(`id`): [`Socket`](api_client_gen.Socket.md)

Load a Socket from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`SocketID`](../modules/api_client_gen.md#socketid) |

#### Returns

[`Socket`](api_client_gen.Socket.md)

___

### loadTypeDefFromID

**loadTypeDefFromID**(`id`): [`TypeDef`](api_client_gen.TypeDef.md)

Load a TypeDef by ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`TypeDefID`](../modules/api_client_gen.md#typedefid) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### moduleConfig

**moduleConfig**(`sourceDirectory`, `opts?`): [`ModuleConfig`](api_client_gen.ModuleConfig.md)

Load the static configuration for a module from the given source directory and optional subpath.

#### Parameters

| Name | Type |
| :------ | :------ |
| `sourceDirectory` | [`Directory`](api_client_gen.Directory.md) |
| `opts?` | [`ClientModuleConfigOpts`](../modules/api_client_gen.md#clientmoduleconfigopts) |

#### Returns

[`ModuleConfig`](api_client_gen.ModuleConfig.md)

___

### module\_

**module_**(): [`Module_`](api_client_gen.Module_.md)

Create a new module.

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### pipeline

**pipeline**(`name`, `opts?`): [`Client`](api_client_gen.Client.md)

Creates a named sub-pipeline.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | Pipeline name. |
| `opts?` | [`ClientPipelineOpts`](../modules/api_client_gen.md#clientpipelineopts) | - |

#### Returns

[`Client`](api_client_gen.Client.md)

___

### secret

**secret**(`id`): [`Secret`](api_client_gen.Secret.md)

Loads a secret from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`SecretID`](../modules/api_client_gen.md#secretid) |

#### Returns

[`Secret`](api_client_gen.Secret.md)

**`Deprecated`**

Use loadSecretFromID instead

___

### setSecret

**setSecret**(`name`, `plaintext`): [`Secret`](api_client_gen.Secret.md)

Sets a secret given a user defined name to its plaintext and returns the secret.
The plaintext value is limited to a size of 128000 bytes.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The user defined name for this secret |
| `plaintext` | `string` | The plaintext of the secret |

#### Returns

[`Secret`](api_client_gen.Secret.md)

___

### socket

**socket**(`opts?`): [`Socket`](api_client_gen.Socket.md)

Loads a socket by its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ClientSocketOpts`](../modules/api_client_gen.md#clientsocketopts) |

#### Returns

[`Socket`](api_client_gen.Socket.md)

**`Deprecated`**

Use loadSocketFromID instead.

___

### typeDef

**typeDef**(): [`TypeDef`](api_client_gen.TypeDef.md)

Create a new TypeDef.

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### with

**with**(`arg`): [`Client`](api_client_gen.Client.md)

Call the provided function with current Client.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`Client`](api_client_gen.Client.md)) => [`Client`](api_client_gen.Client.md) |

#### Returns

[`Client`](api_client_gen.Client.md)
