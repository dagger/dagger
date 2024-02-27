---
id: "api_client_gen.Client"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Client

The root of the DAG.

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

### blob

**blob**(`digest`, `size`, `mediaType`, `uncompressed`): [`Directory`](api_client_gen.Directory.md)

Retrieves a content-addressed blob.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `digest` | `string` | Digest of the blob |
| `size` | `number` | Size of the blob |
| `mediaType` | `string` | Media type of the blob |
| `uncompressed` | `string` | Digest of the uncompressed blob |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### builtinContainer

**builtinContainer**(`digest`): [`Container`](api_client_gen.Container.md)

Retrieves a container builtin to the engine.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `digest` | `string` | Digest of the image manifest |

#### Returns

[`Container`](api_client_gen.Container.md)

___

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
| `version` | `string` | Version required by the SDK. |

#### Returns

`Promise`\<`boolean`\>

___

### container

**container**(`opts?`): [`Container`](api_client_gen.Container.md)

Creates a scratch container.

Optional platform argument initializes new containers to execute and publish as that platform. Platform defaults to that of the builder's host.

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

If the caller is not currently executing in a function, this will return an error.

#### Returns

[`FunctionCall`](api_client_gen.FunctionCall.md)

___

### currentModule

**currentModule**(): [`CurrentModule`](api_client_gen.CurrentModule.md)

The module currently being served in the session, if any.

#### Returns

[`CurrentModule`](api_client_gen.CurrentModule.md)

___

### currentTypeDefs

**currentTypeDefs**(): `Promise`\<[`TypeDef`](api_client_gen.TypeDef.md)[]\>

The TypeDef representations of the objects currently being served in the session.

#### Returns

`Promise`\<[`TypeDef`](api_client_gen.TypeDef.md)[]\>

___

### defaultPlatform

**defaultPlatform**(): `Promise`\<[`Platform`](../modules/api_client_gen.md#platform)\>

The default platform of the engine.

#### Returns

`Promise`\<[`Platform`](../modules/api_client_gen.md#platform)\>

___

### directory

**directory**(`opts?`): [`Directory`](api_client_gen.Directory.md)

Creates an empty directory.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ClientDirectoryOpts`](../modules/api_client_gen.md#clientdirectoryopts) |

#### Returns

[`Directory`](api_client_gen.Directory.md)

___

### file

**file**(`id`): [`File`](api_client_gen.File.md)

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

Creates a function.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | Name of the function, in its original format from the implementation language. |
| `returnType` | [`TypeDef`](api_client_gen.TypeDef.md) | Return type of the function. |

#### Returns

[`Function_`](api_client_gen.Function_.md)

___

### generatedCode

**generatedCode**(`code`): [`GeneratedCode`](api_client_gen.GeneratedCode.md)

Create a code generation result, given a directory containing the generated code.

#### Parameters

| Name | Type |
| :------ | :------ |
| `code` | [`Directory`](api_client_gen.Directory.md) |

#### Returns

[`GeneratedCode`](api_client_gen.GeneratedCode.md)

___

### git

**git**(`url`, `opts?`): [`GitRepository`](api_client_gen.GitRepository.md)

Queries a Git repository.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `url` | `string` | URL of the git repository. Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`. Suffix ".git" is optional. |
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

Load a Container from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ContainerID`](../modules/api_client_gen.md#containerid) |

#### Returns

[`Container`](api_client_gen.Container.md)

___

### loadCurrentModuleFromID

**loadCurrentModuleFromID**(`id`): [`CurrentModule`](api_client_gen.CurrentModule.md)

Load a CurrentModule from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`CurrentModuleID`](../modules/api_client_gen.md#currentmoduleid) |

#### Returns

[`CurrentModule`](api_client_gen.CurrentModule.md)

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

### loadEnvVariableFromID

**loadEnvVariableFromID**(`id`): [`EnvVariable`](api_client_gen.EnvVariable.md)

Load a EnvVariable from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`EnvVariableID`](../modules/api_client_gen.md#envvariableid) |

#### Returns

[`EnvVariable`](api_client_gen.EnvVariable.md)

___

### loadFieldTypeDefFromID

**loadFieldTypeDefFromID**(`id`): [`FieldTypeDef`](api_client_gen.FieldTypeDef.md)

Load a FieldTypeDef from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FieldTypeDefID`](../modules/api_client_gen.md#fieldtypedefid) |

#### Returns

[`FieldTypeDef`](api_client_gen.FieldTypeDef.md)

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

Load a FunctionArg from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FunctionArgID`](../modules/api_client_gen.md#functionargid) |

#### Returns

[`FunctionArg`](api_client_gen.FunctionArg.md)

___

### loadFunctionCallArgValueFromID

**loadFunctionCallArgValueFromID**(`id`): [`FunctionCallArgValue`](api_client_gen.FunctionCallArgValue.md)

Load a FunctionCallArgValue from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FunctionCallArgValueID`](../modules/api_client_gen.md#functioncallargvalueid) |

#### Returns

[`FunctionCallArgValue`](api_client_gen.FunctionCallArgValue.md)

___

### loadFunctionCallFromID

**loadFunctionCallFromID**(`id`): [`FunctionCall`](api_client_gen.FunctionCall.md)

Load a FunctionCall from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FunctionCallID`](../modules/api_client_gen.md#functioncallid) |

#### Returns

[`FunctionCall`](api_client_gen.FunctionCall.md)

___

### loadFunctionFromID

**loadFunctionFromID**(`id`): [`Function_`](api_client_gen.Function_.md)

Load a Function from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`FunctionID`](../modules/api_client_gen.md#functionid) |

#### Returns

[`Function_`](api_client_gen.Function_.md)

___

### loadGeneratedCodeFromID

**loadGeneratedCodeFromID**(`id`): [`GeneratedCode`](api_client_gen.GeneratedCode.md)

Load a GeneratedCode from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`GeneratedCodeID`](../modules/api_client_gen.md#generatedcodeid) |

#### Returns

[`GeneratedCode`](api_client_gen.GeneratedCode.md)

___

### loadGitModuleSourceFromID

**loadGitModuleSourceFromID**(`id`): [`GitModuleSource`](api_client_gen.GitModuleSource.md)

Load a GitModuleSource from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`GitModuleSourceID`](../modules/api_client_gen.md#gitmodulesourceid) |

#### Returns

[`GitModuleSource`](api_client_gen.GitModuleSource.md)

___

### loadGitRefFromID

**loadGitRefFromID**(`id`): [`GitRef`](api_client_gen.GitRef.md)

Load a GitRef from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`GitRefID`](../modules/api_client_gen.md#gitrefid) |

#### Returns

[`GitRef`](api_client_gen.GitRef.md)

___

### loadGitRepositoryFromID

**loadGitRepositoryFromID**(`id`): [`GitRepository`](api_client_gen.GitRepository.md)

Load a GitRepository from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`GitRepositoryID`](../modules/api_client_gen.md#gitrepositoryid) |

#### Returns

[`GitRepository`](api_client_gen.GitRepository.md)

___

### loadHostFromID

**loadHostFromID**(`id`): [`Host`](api_client_gen.Host.md)

Load a Host from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`HostID`](../modules/api_client_gen.md#hostid) |

#### Returns

[`Host`](api_client_gen.Host.md)

___

### loadInputTypeDefFromID

**loadInputTypeDefFromID**(`id`): [`InputTypeDef`](api_client_gen.InputTypeDef.md)

Load a InputTypeDef from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`InputTypeDefID`](../modules/api_client_gen.md#inputtypedefid) |

#### Returns

[`InputTypeDef`](api_client_gen.InputTypeDef.md)

___

### loadInterfaceTypeDefFromID

**loadInterfaceTypeDefFromID**(`id`): [`InterfaceTypeDef`](api_client_gen.InterfaceTypeDef.md)

Load a InterfaceTypeDef from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`InterfaceTypeDefID`](../modules/api_client_gen.md#interfacetypedefid) |

#### Returns

[`InterfaceTypeDef`](api_client_gen.InterfaceTypeDef.md)

___

### loadLabelFromID

**loadLabelFromID**(`id`): [`Label`](api_client_gen.Label.md)

Load a Label from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`LabelID`](../modules/api_client_gen.md#labelid) |

#### Returns

[`Label`](api_client_gen.Label.md)

___

### loadListTypeDefFromID

**loadListTypeDefFromID**(`id`): [`ListTypeDef`](api_client_gen.ListTypeDef.md)

Load a ListTypeDef from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ListTypeDefID`](../modules/api_client_gen.md#listtypedefid) |

#### Returns

[`ListTypeDef`](api_client_gen.ListTypeDef.md)

___

### loadLocalModuleSourceFromID

**loadLocalModuleSourceFromID**(`id`): [`LocalModuleSource`](api_client_gen.LocalModuleSource.md)

Load a LocalModuleSource from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`LocalModuleSourceID`](../modules/api_client_gen.md#localmodulesourceid) |

#### Returns

[`LocalModuleSource`](api_client_gen.LocalModuleSource.md)

___

### loadModuleDependencyFromID

**loadModuleDependencyFromID**(`id`): [`ModuleDependency`](api_client_gen.ModuleDependency.md)

Load a ModuleDependency from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ModuleDependencyID`](../modules/api_client_gen.md#moduledependencyid) |

#### Returns

[`ModuleDependency`](api_client_gen.ModuleDependency.md)

___

### loadModuleFromID

**loadModuleFromID**(`id`): [`Module_`](api_client_gen.Module_.md)

Load a Module from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ModuleID`](../modules/api_client_gen.md#moduleid) |

#### Returns

[`Module_`](api_client_gen.Module_.md)

___

### loadModuleSourceFromID

**loadModuleSourceFromID**(`id`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Load a ModuleSource from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ModuleSourceID`](../modules/api_client_gen.md#modulesourceid) |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

___

### loadObjectTypeDefFromID

**loadObjectTypeDefFromID**(`id`): [`ObjectTypeDef`](api_client_gen.ObjectTypeDef.md)

Load a ObjectTypeDef from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`ObjectTypeDefID`](../modules/api_client_gen.md#objecttypedefid) |

#### Returns

[`ObjectTypeDef`](api_client_gen.ObjectTypeDef.md)

___

### loadPortFromID

**loadPortFromID**(`id`): [`Port`](api_client_gen.Port.md)

Load a Port from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`PortID`](../modules/api_client_gen.md#portid) |

#### Returns

[`Port`](api_client_gen.Port.md)

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

Load a Service from its ID.

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

### loadTerminalFromID

**loadTerminalFromID**(`id`): [`Terminal`](api_client_gen.Terminal.md)

Load a Terminal from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`TerminalID`](../modules/api_client_gen.md#terminalid) |

#### Returns

[`Terminal`](api_client_gen.Terminal.md)

___

### loadTypeDefFromID

**loadTypeDefFromID**(`id`): [`TypeDef`](api_client_gen.TypeDef.md)

Load a TypeDef from its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`TypeDefID`](../modules/api_client_gen.md#typedefid) |

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### moduleDependency

**moduleDependency**(`source`, `opts?`): [`ModuleDependency`](api_client_gen.ModuleDependency.md)

Create a new module dependency configuration from a module source and name

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `source` | [`ModuleSource`](api_client_gen.ModuleSource.md) | The source of the dependency |
| `opts?` | [`ClientModuleDependencyOpts`](../modules/api_client_gen.md#clientmoduledependencyopts) | - |

#### Returns

[`ModuleDependency`](api_client_gen.ModuleDependency.md)

___

### moduleSource

**moduleSource**(`refString`, `opts?`): [`ModuleSource`](api_client_gen.ModuleSource.md)

Create a new module source instance from a source ref string.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `refString` | `string` | The string ref representation of the module source |
| `opts?` | [`ClientModuleSourceOpts`](../modules/api_client_gen.md#clientmodulesourceopts) | - |

#### Returns

[`ModuleSource`](api_client_gen.ModuleSource.md)

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
| `name` | `string` | Name of the sub-pipeline. |
| `opts?` | [`ClientPipelineOpts`](../modules/api_client_gen.md#clientpipelineopts) | - |

#### Returns

[`Client`](api_client_gen.Client.md)

___

### secret

**secret**(`name`, `opts?`): [`Secret`](api_client_gen.Secret.md)

Reference a secret by name.

#### Parameters

| Name | Type |
| :------ | :------ |
| `name` | `string` |
| `opts?` | [`ClientSecretOpts`](../modules/api_client_gen.md#clientsecretopts) |

#### Returns

[`Secret`](api_client_gen.Secret.md)

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

**socket**(`id`): [`Socket`](api_client_gen.Socket.md)

Loads a socket by its ID.

#### Parameters

| Name | Type |
| :------ | :------ |
| `id` | [`SocketID`](../modules/api_client_gen.md#socketid) |

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
