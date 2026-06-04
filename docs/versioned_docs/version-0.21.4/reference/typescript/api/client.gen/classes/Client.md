---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Client

The root of the DAG.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Client**(`ctx?`, `_id?`, `_defaultPlatform?`, `_version?`): `Client`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_defaultPlatform?

[`Platform`](../type-aliases/Platform.md)

##### \_version?

`string`

#### Returns

`Client`

#### Overrides

`BaseClient.constructor`

## Methods

### address()

> **address**(`value`): [`Address`](Address.md)

initialize an address to load directories, containers, secrets or other object types.

#### Parameters

##### value

`string`

#### Returns

[`Address`](Address.md)

***

### cacheVolume()

> **cacheVolume**(`key`, `opts?`): [`CacheVolume`](CacheVolume.md)

Constructs a cache volume for a given cache key.

#### Parameters

##### key

`string`

A string identifier to target this cache volume (e.g., "modules-cache").

##### opts?

[`ClientCacheVolumeOpts`](../type-aliases/ClientCacheVolumeOpts.md)

#### Returns

[`CacheVolume`](CacheVolume.md)

***

### changeset()

> **changeset**(): [`Changeset`](Changeset.md)

Creates an empty changeset

#### Returns

[`Changeset`](Changeset.md)

***

### cloud()

> **cloud**(): [`Cloud`](Cloud.md)

Dagger Cloud configuration and state

#### Returns

[`Cloud`](Cloud.md)

***

### container()

> **container**(`opts?`): [`Container`](Container.md)

Creates a scratch container, with no image or metadata.

To pull an image, follow up with the "from" function.

#### Parameters

##### opts?

[`ClientContainerOpts`](../type-aliases/ClientContainerOpts.md)

#### Returns

[`Container`](Container.md)

***

### currentEnv()

> **currentEnv**(): [`Env`](Env.md)

**`Experimental`**

Returns the current environment

When called from a function invoked via an LLM tool call, this will be the LLM's current environment, including any modifications made through calling tools. Env values returned by functions become the new environment for subsequent calls, and Changeset values returned by functions are applied to the environment's workspace.

When called from a module function outside of an LLM, this returns an Env with the current module installed, and with the current module's source directory as its workspace.

#### Returns

[`Env`](Env.md)

***

### currentFunctionCall()

> **currentFunctionCall**(): [`FunctionCall`](FunctionCall.md)

The FunctionCall context that the SDK caller is currently executing in.

If the caller is not currently executing in a function, this will return an error.

#### Returns

[`FunctionCall`](FunctionCall.md)

***

### currentModule()

> **currentModule**(): [`CurrentModule`](CurrentModule.md)

The module currently being served in the session, if any.

#### Returns

[`CurrentModule`](CurrentModule.md)

***

### currentTypeDefs()

> **currentTypeDefs**(`opts?`): `Promise`\<[`TypeDef`](TypeDef.md)[]\>

The TypeDef representations of the objects currently being served in the session.

#### Parameters

##### opts?

[`ClientCurrentTypeDefsOpts`](../type-aliases/ClientCurrentTypeDefsOpts.md)

#### Returns

`Promise`\<[`TypeDef`](TypeDef.md)[]\>

***

### currentWorkspace()

> **currentWorkspace**(): [`Workspace`](Workspace.md)

**`Experimental`**

Detect and return the current workspace.

#### Returns

[`Workspace`](Workspace.md)

***

### defaultPlatform()

> **defaultPlatform**(): `Promise`\<[`Platform`](../type-aliases/Platform.md)\>

The default platform of the engine.

#### Returns

`Promise`\<[`Platform`](../type-aliases/Platform.md)\>

***

### directory()

> **directory**(): [`Directory`](Directory.md)

Creates an empty directory.

#### Returns

[`Directory`](Directory.md)

***

### engine()

> **engine**(): [`Engine`](Engine.md)

The Dagger engine container configuration and state

#### Returns

[`Engine`](Engine.md)

***

### env()

> **env**(`opts?`): [`Env`](Env.md)

**`Experimental`**

Initializes a new environment

#### Parameters

##### opts?

[`ClientEnvOpts`](../type-aliases/ClientEnvOpts.md)

#### Returns

[`Env`](Env.md)

***

### envFile()

> **envFile**(`opts?`): [`EnvFile`](EnvFile.md)

Initialize an environment file

#### Parameters

##### opts?

[`ClientEnvFileOpts`](../type-aliases/ClientEnvFileOpts.md)

#### Returns

[`EnvFile`](EnvFile.md)

***

### error()

> **error**(`message`): [`Error`](Error.md)

Create a new error.

#### Parameters

##### message

`string`

A brief description of the error.

#### Returns

[`Error`](Error.md)

***

### file()

> **file**(`name`, `contents`, `opts?`): [`File`](File.md)

Creates a file with the specified contents.

#### Parameters

##### name

`string`

Name of the new file. Example: "foo.txt"

##### contents

`string`

Contents of the new file. Example: "Hello world!"

##### opts?

[`ClientFileOpts`](../type-aliases/ClientFileOpts.md)

#### Returns

[`File`](File.md)

***

### function\_()

> **function\_**(`name`, `returnType`): [`Function_`](Function.md)

Creates a function.

#### Parameters

##### name

`string`

Name of the function, in its original format from the implementation language.

##### returnType

[`TypeDef`](TypeDef.md)

Return type of the function.

#### Returns

[`Function_`](Function.md)

***

### generatedCode()

> **generatedCode**(`code`): [`GeneratedCode`](GeneratedCode.md)

Create a code generation result, given a directory containing the generated code.

#### Parameters

##### code

[`Directory`](Directory.md)

#### Returns

[`GeneratedCode`](GeneratedCode.md)

***

### getGQLClient()

> **getGQLClient**(): `GraphQLClient`

Get the Raw GraphQL client.

#### Returns

`GraphQLClient`

***

### git()

> **git**(`url`, `opts?`): [`GitRepository`](GitRepository.md)

Queries a Git repository.

#### Parameters

##### url

`string`

URL of the git repository.

Can be formatted as `https://{host}/{owner}/{repo}`, `git@{host}:{owner}/{repo}`.

Suffix ".git" is optional.

##### opts?

[`ClientGitOpts`](../type-aliases/ClientGitOpts.md)

#### Returns

[`GitRepository`](GitRepository.md)

***

### host()

> **host**(): [`Host`](Host.md)

Queries the host environment.

#### Returns

[`Host`](Host.md)

***

### http()

> **http**(`url`, `opts?`): [`File`](File.md)

Returns a file containing an http remote url content.

#### Parameters

##### url

`string`

HTTP url to get the content from (e.g., "https://docs.dagger.io").

##### opts?

[`ClientHttpOpts`](../type-aliases/ClientHttpOpts.md)

#### Returns

[`File`](File.md)

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Query.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### json()

> **json**(): [`JSONValue`](JSONValue.md)

Initialize a JSON value

#### Returns

[`JSONValue`](JSONValue.md)

***

### llm()

> **llm**(`opts?`): [`LLM`](LLM.md)

**`Experimental`**

Initialize a Large Language Model (LLM)

#### Parameters

##### opts?

[`ClientLlmOpts`](../type-aliases/ClientLlmOpts.md)

#### Returns

[`LLM`](LLM.md)

***

### loadAddressFromID()

> **loadAddressFromID**(`id`): [`Address`](Address.md)

Load a Address from its ID.

#### Parameters

##### id

[`AddressID`](../type-aliases/AddressID.md)

#### Returns

[`Address`](Address.md)

***

### loadBindingFromID()

> **loadBindingFromID**(`id`): [`Binding`](Binding.md)

Load a Binding from its ID.

#### Parameters

##### id

[`BindingID`](../type-aliases/BindingID.md)

#### Returns

[`Binding`](Binding.md)

***

### loadCacheVolumeFromID()

> **loadCacheVolumeFromID**(`id`): [`CacheVolume`](CacheVolume.md)

Load a CacheVolume from its ID.

#### Parameters

##### id

[`CacheVolumeID`](../type-aliases/CacheVolumeID.md)

#### Returns

[`CacheVolume`](CacheVolume.md)

***

### loadChangesetFromID()

> **loadChangesetFromID**(`id`): [`Changeset`](Changeset.md)

Load a Changeset from its ID.

#### Parameters

##### id

[`ChangesetID`](../type-aliases/ChangesetID.md)

#### Returns

[`Changeset`](Changeset.md)

***

### loadCheckFromID()

> **loadCheckFromID**(`id`): [`Check`](Check.md)

Load a Check from its ID.

#### Parameters

##### id

[`CheckID`](../type-aliases/CheckID.md)

#### Returns

[`Check`](Check.md)

***

### loadCheckGroupFromID()

> **loadCheckGroupFromID**(`id`): [`CheckGroup`](CheckGroup.md)

Load a CheckGroup from its ID.

#### Parameters

##### id

[`CheckGroupID`](../type-aliases/CheckGroupID.md)

#### Returns

[`CheckGroup`](CheckGroup.md)

***

### loadClientFilesyncMirrorFromID()

> **loadClientFilesyncMirrorFromID**(`id`): [`ClientFilesyncMirror`](ClientFilesyncMirror.md)

Load a ClientFilesyncMirror from its ID.

#### Parameters

##### id

[`ClientFilesyncMirrorID`](../type-aliases/ClientFilesyncMirrorID.md)

#### Returns

[`ClientFilesyncMirror`](ClientFilesyncMirror.md)

***

### loadCloudFromID()

> **loadCloudFromID**(`id`): [`Cloud`](Cloud.md)

Load a Cloud from its ID.

#### Parameters

##### id

[`CloudID`](../type-aliases/CloudID.md)

#### Returns

[`Cloud`](Cloud.md)

***

### loadContainerFromID()

> **loadContainerFromID**(`id`): [`Container`](Container.md)

Load a Container from its ID.

#### Parameters

##### id

[`ContainerID`](../type-aliases/ContainerID.md)

#### Returns

[`Container`](Container.md)

***

### loadCurrentModuleFromID()

> **loadCurrentModuleFromID**(`id`): [`CurrentModule`](CurrentModule.md)

Load a CurrentModule from its ID.

#### Parameters

##### id

[`CurrentModuleID`](../type-aliases/CurrentModuleID.md)

#### Returns

[`CurrentModule`](CurrentModule.md)

***

### loadDiffStatFromID()

> **loadDiffStatFromID**(`id`): [`DiffStat`](DiffStat.md)

Load a DiffStat from its ID.

#### Parameters

##### id

[`DiffStatID`](../type-aliases/DiffStatID.md)

#### Returns

[`DiffStat`](DiffStat.md)

***

### loadDirectoryFromID()

> **loadDirectoryFromID**(`id`): [`Directory`](Directory.md)

Load a Directory from its ID.

#### Parameters

##### id

[`DirectoryID`](../type-aliases/DirectoryID.md)

#### Returns

[`Directory`](Directory.md)

***

### loadEngineCacheEntryFromID()

> **loadEngineCacheEntryFromID**(`id`): [`EngineCacheEntry`](EngineCacheEntry.md)

Load a EngineCacheEntry from its ID.

#### Parameters

##### id

[`EngineCacheEntryID`](../type-aliases/EngineCacheEntryID.md)

#### Returns

[`EngineCacheEntry`](EngineCacheEntry.md)

***

### loadEngineCacheEntrySetFromID()

> **loadEngineCacheEntrySetFromID**(`id`): [`EngineCacheEntrySet`](EngineCacheEntrySet.md)

Load a EngineCacheEntrySet from its ID.

#### Parameters

##### id

[`EngineCacheEntrySetID`](../type-aliases/EngineCacheEntrySetID.md)

#### Returns

[`EngineCacheEntrySet`](EngineCacheEntrySet.md)

***

### loadEngineCacheFromID()

> **loadEngineCacheFromID**(`id`): [`EngineCache`](EngineCache.md)

Load a EngineCache from its ID.

#### Parameters

##### id

[`EngineCacheID`](../type-aliases/EngineCacheID.md)

#### Returns

[`EngineCache`](EngineCache.md)

***

### loadEngineFromID()

> **loadEngineFromID**(`id`): [`Engine`](Engine.md)

Load a Engine from its ID.

#### Parameters

##### id

[`EngineID`](../type-aliases/EngineID.md)

#### Returns

[`Engine`](Engine.md)

***

### loadEnumTypeDefFromID()

> **loadEnumTypeDefFromID**(`id`): [`EnumTypeDef`](EnumTypeDef.md)

Load a EnumTypeDef from its ID.

#### Parameters

##### id

[`EnumTypeDefID`](../type-aliases/EnumTypeDefID.md)

#### Returns

[`EnumTypeDef`](EnumTypeDef.md)

***

### loadEnumValueTypeDefFromID()

> **loadEnumValueTypeDefFromID**(`id`): [`EnumValueTypeDef`](EnumValueTypeDef.md)

Load a EnumValueTypeDef from its ID.

#### Parameters

##### id

[`EnumValueTypeDefID`](../type-aliases/EnumValueTypeDefID.md)

#### Returns

[`EnumValueTypeDef`](EnumValueTypeDef.md)

***

### loadEnvFileFromID()

> **loadEnvFileFromID**(`id`): [`EnvFile`](EnvFile.md)

Load a EnvFile from its ID.

#### Parameters

##### id

[`EnvFileID`](../type-aliases/EnvFileID.md)

#### Returns

[`EnvFile`](EnvFile.md)

***

### loadEnvFromID()

> **loadEnvFromID**(`id`): [`Env`](Env.md)

Load a Env from its ID.

#### Parameters

##### id

[`EnvID`](../type-aliases/EnvID.md)

#### Returns

[`Env`](Env.md)

***

### loadEnvVariableFromID()

> **loadEnvVariableFromID**(`id`): [`EnvVariable`](EnvVariable.md)

Load a EnvVariable from its ID.

#### Parameters

##### id

[`EnvVariableID`](../type-aliases/EnvVariableID.md)

#### Returns

[`EnvVariable`](EnvVariable.md)

***

### loadErrorFromID()

> **loadErrorFromID**(`id`): [`Error`](Error.md)

Load a Error from its ID.

#### Parameters

##### id

[`ErrorID`](../type-aliases/ErrorID.md)

#### Returns

[`Error`](Error.md)

***

### loadErrorValueFromID()

> **loadErrorValueFromID**(`id`): [`ErrorValue`](ErrorValue.md)

Load a ErrorValue from its ID.

#### Parameters

##### id

[`ErrorValueID`](../type-aliases/ErrorValueID.md)

#### Returns

[`ErrorValue`](ErrorValue.md)

***

### loadExportableFromID()

> **loadExportableFromID**(`id`): [`Exportable`](../interfaces/Exportable.md)

Load a Exportable from its ID.

#### Parameters

##### id

[`ExportableID`](../type-aliases/ExportableID.md)

#### Returns

[`Exportable`](../interfaces/Exportable.md)

***

### loadFieldTypeDefFromID()

> **loadFieldTypeDefFromID**(`id`): [`FieldTypeDef`](FieldTypeDef.md)

Load a FieldTypeDef from its ID.

#### Parameters

##### id

[`FieldTypeDefID`](../type-aliases/FieldTypeDefID.md)

#### Returns

[`FieldTypeDef`](FieldTypeDef.md)

***

### loadFileFromID()

> **loadFileFromID**(`id`): [`File`](File.md)

Load a File from its ID.

#### Parameters

##### id

[`FileID`](../type-aliases/FileID.md)

#### Returns

[`File`](File.md)

***

### loadFunctionArgFromID()

> **loadFunctionArgFromID**(`id`): [`FunctionArg`](FunctionArg.md)

Load a FunctionArg from its ID.

#### Parameters

##### id

[`FunctionArgID`](../type-aliases/FunctionArgID.md)

#### Returns

[`FunctionArg`](FunctionArg.md)

***

### loadFunctionCallArgValueFromID()

> **loadFunctionCallArgValueFromID**(`id`): [`FunctionCallArgValue`](FunctionCallArgValue.md)

Load a FunctionCallArgValue from its ID.

#### Parameters

##### id

[`FunctionCallArgValueID`](../type-aliases/FunctionCallArgValueID.md)

#### Returns

[`FunctionCallArgValue`](FunctionCallArgValue.md)

***

### loadFunctionCallFromID()

> **loadFunctionCallFromID**(`id`): [`FunctionCall`](FunctionCall.md)

Load a FunctionCall from its ID.

#### Parameters

##### id

[`FunctionCallID`](../type-aliases/FunctionCallID.md)

#### Returns

[`FunctionCall`](FunctionCall.md)

***

### loadFunctionFromID()

> **loadFunctionFromID**(`id`): [`Function_`](Function.md)

Load a Function from its ID.

#### Parameters

##### id

[`FunctionID`](../type-aliases/FunctionID.md)

#### Returns

[`Function_`](Function.md)

***

### loadGeneratedCodeFromID()

> **loadGeneratedCodeFromID**(`id`): [`GeneratedCode`](GeneratedCode.md)

Load a GeneratedCode from its ID.

#### Parameters

##### id

[`GeneratedCodeID`](../type-aliases/GeneratedCodeID.md)

#### Returns

[`GeneratedCode`](GeneratedCode.md)

***

### loadGeneratorFromID()

> **loadGeneratorFromID**(`id`): [`Generator`](Generator.md)

Load a Generator from its ID.

#### Parameters

##### id

[`GeneratorID`](../type-aliases/GeneratorID.md)

#### Returns

[`Generator`](Generator.md)

***

### loadGeneratorGroupFromID()

> **loadGeneratorGroupFromID**(`id`): [`GeneratorGroup`](GeneratorGroup.md)

Load a GeneratorGroup from its ID.

#### Parameters

##### id

[`GeneratorGroupID`](../type-aliases/GeneratorGroupID.md)

#### Returns

[`GeneratorGroup`](GeneratorGroup.md)

***

### loadGitRefFromID()

> **loadGitRefFromID**(`id`): [`GitRef`](GitRef.md)

Load a GitRef from its ID.

#### Parameters

##### id

[`GitRefID`](../type-aliases/GitRefID.md)

#### Returns

[`GitRef`](GitRef.md)

***

### loadGitRepositoryFromID()

> **loadGitRepositoryFromID**(`id`): [`GitRepository`](GitRepository.md)

Load a GitRepository from its ID.

#### Parameters

##### id

[`GitRepositoryID`](../type-aliases/GitRepositoryID.md)

#### Returns

[`GitRepository`](GitRepository.md)

***

### loadHealthcheckConfigFromID()

> **loadHealthcheckConfigFromID**(`id`): [`HealthcheckConfig`](HealthcheckConfig.md)

Load a HealthcheckConfig from its ID.

#### Parameters

##### id

[`HealthcheckConfigID`](../type-aliases/HealthcheckConfigID.md)

#### Returns

[`HealthcheckConfig`](HealthcheckConfig.md)

***

### loadHostFromID()

> **loadHostFromID**(`id`): [`Host`](Host.md)

Load a Host from its ID.

#### Parameters

##### id

[`HostID`](../type-aliases/HostID.md)

#### Returns

[`Host`](Host.md)

***

### loadHTTPStateFromID()

> **loadHTTPStateFromID**(`id`): [`HTTPState`](HTTPState.md)

Load a HTTPState from its ID.

#### Parameters

##### id

[`HTTPStateID`](../type-aliases/HTTPStateID.md)

#### Returns

[`HTTPState`](HTTPState.md)

***

### loadInputTypeDefFromID()

> **loadInputTypeDefFromID**(`id`): [`InputTypeDef`](InputTypeDef.md)

Load a InputTypeDef from its ID.

#### Parameters

##### id

[`InputTypeDefID`](../type-aliases/InputTypeDefID.md)

#### Returns

[`InputTypeDef`](InputTypeDef.md)

***

### loadInterfaceTypeDefFromID()

> **loadInterfaceTypeDefFromID**(`id`): [`InterfaceTypeDef`](InterfaceTypeDef.md)

Load a InterfaceTypeDef from its ID.

#### Parameters

##### id

[`InterfaceTypeDefID`](../type-aliases/InterfaceTypeDefID.md)

#### Returns

[`InterfaceTypeDef`](InterfaceTypeDef.md)

***

### loadJSONValueFromID()

> **loadJSONValueFromID**(`id`): [`JSONValue`](JSONValue.md)

Load a JSONValue from its ID.

#### Parameters

##### id

[`JSONValueID`](../type-aliases/JSONValueID.md)

#### Returns

[`JSONValue`](JSONValue.md)

***

### loadLabelFromID()

> **loadLabelFromID**(`id`): [`Label`](Label.md)

Load a Label from its ID.

#### Parameters

##### id

[`LabelID`](../type-aliases/LabelID.md)

#### Returns

[`Label`](Label.md)

***

### loadListTypeDefFromID()

> **loadListTypeDefFromID**(`id`): [`ListTypeDef`](ListTypeDef.md)

Load a ListTypeDef from its ID.

#### Parameters

##### id

[`ListTypeDefID`](../type-aliases/ListTypeDefID.md)

#### Returns

[`ListTypeDef`](ListTypeDef.md)

***

### loadLLMFromID()

> **loadLLMFromID**(`id`): [`LLM`](LLM.md)

Load a LLM from its ID.

#### Parameters

##### id

[`LLMID`](../type-aliases/LLMID.md)

#### Returns

[`LLM`](LLM.md)

***

### loadLLMTokenUsageFromID()

> **loadLLMTokenUsageFromID**(`id`): [`LLMTokenUsage`](LLMTokenUsage.md)

Load a LLMTokenUsage from its ID.

#### Parameters

##### id

[`LLMTokenUsageID`](../type-aliases/LLMTokenUsageID.md)

#### Returns

[`LLMTokenUsage`](LLMTokenUsage.md)

***

### loadModuleConfigClientFromID()

> **loadModuleConfigClientFromID**(`id`): [`ModuleConfigClient`](ModuleConfigClient.md)

Load a ModuleConfigClient from its ID.

#### Parameters

##### id

[`ModuleConfigClientID`](../type-aliases/ModuleConfigClientID.md)

#### Returns

[`ModuleConfigClient`](ModuleConfigClient.md)

***

### loadModuleFromID()

> **loadModuleFromID**(`id`): [`Module_`](Module.md)

Load a Module from its ID.

#### Parameters

##### id

[`ModuleID`](../type-aliases/ModuleID.md)

#### Returns

[`Module_`](Module.md)

***

### loadModuleSourceFromID()

> **loadModuleSourceFromID**(`id`): [`ModuleSource`](ModuleSource.md)

Load a ModuleSource from its ID.

#### Parameters

##### id

[`ModuleSourceID`](../type-aliases/ModuleSourceID.md)

#### Returns

[`ModuleSource`](ModuleSource.md)

***

### loadObjectTypeDefFromID()

> **loadObjectTypeDefFromID**(`id`): [`ObjectTypeDef`](ObjectTypeDef.md)

Load a ObjectTypeDef from its ID.

#### Parameters

##### id

[`ObjectTypeDefID`](../type-aliases/ObjectTypeDefID.md)

#### Returns

[`ObjectTypeDef`](ObjectTypeDef.md)

***

### loadPortFromID()

> **loadPortFromID**(`id`): [`Port`](Port.md)

Load a Port from its ID.

#### Parameters

##### id

[`PortID`](../type-aliases/PortID.md)

#### Returns

[`Port`](Port.md)

***

### loadRemoteGitMirrorFromID()

> **loadRemoteGitMirrorFromID**(`id`): [`RemoteGitMirror`](RemoteGitMirror.md)

Load a RemoteGitMirror from its ID.

#### Parameters

##### id

[`RemoteGitMirrorID`](../type-aliases/RemoteGitMirrorID.md)

#### Returns

[`RemoteGitMirror`](RemoteGitMirror.md)

***

### loadScalarTypeDefFromID()

> **loadScalarTypeDefFromID**(`id`): [`ScalarTypeDef`](ScalarTypeDef.md)

Load a ScalarTypeDef from its ID.

#### Parameters

##### id

[`ScalarTypeDefID`](../type-aliases/ScalarTypeDefID.md)

#### Returns

[`ScalarTypeDef`](ScalarTypeDef.md)

***

### loadSDKConfigFromID()

> **loadSDKConfigFromID**(`id`): [`SDKConfig`](SDKConfig.md)

Load a SDKConfig from its ID.

#### Parameters

##### id

[`SDKConfigID`](../type-aliases/SDKConfigID.md)

#### Returns

[`SDKConfig`](SDKConfig.md)

***

### loadSearchResultFromID()

> **loadSearchResultFromID**(`id`): [`SearchResult`](SearchResult.md)

Load a SearchResult from its ID.

#### Parameters

##### id

[`SearchResultID`](../type-aliases/SearchResultID.md)

#### Returns

[`SearchResult`](SearchResult.md)

***

### loadSearchSubmatchFromID()

> **loadSearchSubmatchFromID**(`id`): [`SearchSubmatch`](SearchSubmatch.md)

Load a SearchSubmatch from its ID.

#### Parameters

##### id

[`SearchSubmatchID`](../type-aliases/SearchSubmatchID.md)

#### Returns

[`SearchSubmatch`](SearchSubmatch.md)

***

### loadSecretFromID()

> **loadSecretFromID**(`id`): [`Secret`](Secret.md)

Load a Secret from its ID.

#### Parameters

##### id

[`SecretID`](../type-aliases/SecretID.md)

#### Returns

[`Secret`](Secret.md)

***

### loadServiceFromID()

> **loadServiceFromID**(`id`): [`Service`](Service.md)

Load a Service from its ID.

#### Parameters

##### id

[`ServiceID`](../type-aliases/ServiceID.md)

#### Returns

[`Service`](Service.md)

***

### loadSocketFromID()

> **loadSocketFromID**(`id`): [`Socket`](Socket.md)

Load a Socket from its ID.

#### Parameters

##### id

[`SocketID`](../type-aliases/SocketID.md)

#### Returns

[`Socket`](Socket.md)

***

### loadSourceMapFromID()

> **loadSourceMapFromID**(`id`): [`SourceMap`](SourceMap.md)

Load a SourceMap from its ID.

#### Parameters

##### id

[`SourceMapID`](../type-aliases/SourceMapID.md)

#### Returns

[`SourceMap`](SourceMap.md)

***

### loadStatFromID()

> **loadStatFromID**(`id`): [`Stat`](Stat.md)

Load a Stat from its ID.

#### Parameters

##### id

[`StatID`](../type-aliases/StatID.md)

#### Returns

[`Stat`](Stat.md)

***

### loadSyncerFromID()

> **loadSyncerFromID**(`id`): [`Syncer`](../interfaces/Syncer.md)

Load a Syncer from its ID.

#### Parameters

##### id

[`SyncerID`](../type-aliases/SyncerID.md)

#### Returns

[`Syncer`](../interfaces/Syncer.md)

***

### loadTerminalFromID()

> **loadTerminalFromID**(`id`): [`Terminal`](Terminal.md)

Load a Terminal from its ID.

#### Parameters

##### id

[`TerminalID`](../type-aliases/TerminalID.md)

#### Returns

[`Terminal`](Terminal.md)

***

### loadTypeDefFromID()

> **loadTypeDefFromID**(`id`): [`TypeDef`](TypeDef.md)

Load a TypeDef from its ID.

#### Parameters

##### id

[`TypeDefID`](../type-aliases/TypeDefID.md)

#### Returns

[`TypeDef`](TypeDef.md)

***

### loadUpFromID()

> **loadUpFromID**(`id`): [`Up`](Up.md)

Load a Up from its ID.

#### Parameters

##### id

[`UpID`](../type-aliases/UpID.md)

#### Returns

[`Up`](Up.md)

***

### loadUpGroupFromID()

> **loadUpGroupFromID**(`id`): [`UpGroup`](UpGroup.md)

Load a UpGroup from its ID.

#### Parameters

##### id

[`UpGroupID`](../type-aliases/UpGroupID.md)

#### Returns

[`UpGroup`](UpGroup.md)

***

### loadWorkspaceFromID()

> **loadWorkspaceFromID**(`id`): [`Workspace`](Workspace.md)

Load a Workspace from its ID.

#### Parameters

##### id

[`WorkspaceID`](../type-aliases/WorkspaceID.md)

#### Returns

[`Workspace`](Workspace.md)

***

### module\_()

> **module\_**(): [`Module_`](Module.md)

Create a new module.

#### Returns

[`Module_`](Module.md)

***

### moduleSource()

> **moduleSource**(`refString`, `opts?`): [`ModuleSource`](ModuleSource.md)

Create a new module source instance from a source ref string

#### Parameters

##### refString

`string`

The string ref representation of the module source

##### opts?

[`ClientModuleSourceOpts`](../type-aliases/ClientModuleSourceOpts.md)

#### Returns

[`ModuleSource`](ModuleSource.md)

***

### node()

> **node**(`id`): [`Node`](../interfaces/Node.md)

Load any object by its ID.

#### Parameters

##### id

[`ID`](../type-aliases/ID.md)

#### Returns

[`Node`](../interfaces/Node.md)

***

### secret()

> **secret**(`uri`, `opts?`): [`Secret`](Secret.md)

Creates a new secret.

#### Parameters

##### uri

`string`

The URI of the secret store

##### opts?

[`ClientSecretOpts`](../type-aliases/ClientSecretOpts.md)

#### Returns

[`Secret`](Secret.md)

***

### setSecret()

> **setSecret**(`name`, `plaintext`): [`Secret`](Secret.md)

Sets a secret given a user defined name to its plaintext and returns the secret.

The plaintext value is limited to a size of 128000 bytes.

#### Parameters

##### name

`string`

The user defined name for this secret

##### plaintext

`string`

The plaintext of the secret

#### Returns

[`Secret`](Secret.md)

***

### sourceMap()

> **sourceMap**(`filename`, `line`, `column`): [`SourceMap`](SourceMap.md)

Creates source map metadata.

#### Parameters

##### filename

`string`

The filename from the module source.

##### line

`number`

The line number within the filename.

##### column

`number`

The column number within the line.

#### Returns

[`SourceMap`](SourceMap.md)

***

### typeDef()

> **typeDef**(): [`TypeDef`](TypeDef.md)

Create a new TypeDef.

#### Returns

[`TypeDef`](TypeDef.md)

***

### version()

> **version**(): `Promise`\<`string`\>

Get the current Dagger Engine version.

#### Returns

`Promise`\<`string`\>
