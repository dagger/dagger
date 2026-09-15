[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ModuleSource

# Class: ModuleSource

The source needed to load and run a module, along with any metadata about the source such as versions/urls/etc.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new ModuleSource**(`ctx?`, `_id?`, `_asString?`, `_cloneRef?`, `_commit?`, `_configExists?`, `_digest?`, `_engineVersion?`, `_htmlRepoURL?`, `_htmlURL?`, `_kind?`, `_localContextDirectoryPath?`, `_moduleName?`, `_moduleOriginalName?`, `_originalSubpath?`, `_pin?`, `_repoRootPath?`, `_sourceRootSubpath?`, `_sourceSubpath?`, `_sync?`, `_version?`): `ModuleSource`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ModuleSourceID`](../type-aliases/ModuleSourceID.md)

##### \_asString?

`string`

##### \_cloneRef?

`string`

##### \_commit?

`string`

##### \_configExists?

`boolean`

##### \_digest?

`string`

##### \_engineVersion?

`string`

##### \_htmlRepoURL?

`string`

##### \_htmlURL?

`string`

##### \_kind?

[`ModuleSourceKind`](../enumerations/ModuleSourceKind.md)

##### \_localContextDirectoryPath?

`string`

##### \_moduleName?

`string`

##### \_moduleOriginalName?

`string`

##### \_originalSubpath?

`string`

##### \_pin?

`string`

##### \_repoRootPath?

`string`

##### \_sourceRootSubpath?

`string`

##### \_sourceSubpath?

`string`

##### \_sync?

[`ModuleSourceID`](../type-aliases/ModuleSourceID.md)

##### \_version?

`string`

#### Returns

`ModuleSource`

#### Overrides

`BaseClient.constructor`

## Methods

### asModule()

> **asModule**(): [`Module_`](Module.md)

Load the source as a module. If this is a local source, the parent directory must have been provided during module source creation

#### Returns

[`Module_`](Module.md)

***

### asString()

> **asString**(): `Promise`\<`string`\>

A human readable ref string representation of this module source.

#### Returns

`Promise`\<`string`\>

***

### blueprint()

> **blueprint**(): `ModuleSource`

The blueprint referenced by the module source.

#### Returns

`ModuleSource`

***

### cloneRef()

> **cloneRef**(): `Promise`\<`string`\>

The ref to clone the root of the git repo from. Only valid for git sources.

#### Returns

`Promise`\<`string`\>

***

### commit()

> **commit**(): `Promise`\<`string`\>

The resolved commit of the git repo this source points to.

#### Returns

`Promise`\<`string`\>

***

### configClients()

> **configClients**(): `Promise`\<[`ModuleConfigClient`](ModuleConfigClient.md)[]\>

The clients generated for the module.

#### Returns

`Promise`\<[`ModuleConfigClient`](ModuleConfigClient.md)[]\>

***

### configExists()

> **configExists**(): `Promise`\<`boolean`\>

Whether an existing dagger.json for the module was found.

#### Returns

`Promise`\<`boolean`\>

***

### contextDirectory()

> **contextDirectory**(): [`Directory`](Directory.md)

The full directory loaded for the module source, including the source code as a subdirectory.

#### Returns

[`Directory`](Directory.md)

***

### dependencies()

> **dependencies**(): `Promise`\<`ModuleSource`[]\>

The dependencies of the module source.

#### Returns

`Promise`\<`ModuleSource`[]\>

***

### digest()

> **digest**(): `Promise`\<`string`\>

A content-hash of the module source. Module sources with the same digest will output the same generated context and convert into the same module instance.

#### Returns

`Promise`\<`string`\>

***

### directory()

> **directory**(`path`): [`Directory`](Directory.md)

The directory containing the module configuration and source code (source code may be in a subdir).

#### Parameters

##### path

`string`

A subpath from the source directory to select.

#### Returns

[`Directory`](Directory.md)

***

### engineVersion()

> **engineVersion**(): `Promise`\<`string`\>

The engine version of the module.

#### Returns

`Promise`\<`string`\>

***

### generatedContextDirectory()

> **generatedContextDirectory**(): [`Directory`](Directory.md)

The generated files and directories made on top of the module source's context directory.

#### Returns

[`Directory`](Directory.md)

***

### htmlRepoURL()

> **htmlRepoURL**(): `Promise`\<`string`\>

The URL to access the web view of the repository (e.g., GitHub, GitLab, Bitbucket).

#### Returns

`Promise`\<`string`\>

***

### htmlURL()

> **htmlURL**(): `Promise`\<`string`\>

The URL to the source's git repo in a web browser. Only valid for git sources.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ModuleSourceID`](../type-aliases/ModuleSourceID.md)\>

A unique identifier for this ModuleSource.

#### Returns

`Promise`\<[`ModuleSourceID`](../type-aliases/ModuleSourceID.md)\>

***

### introspectionSchemaJSON()

> **introspectionSchemaJSON**(): [`File`](File.md)

The introspection schema JSON file for this module source.

This file represents the schema visible to the module's source code, including all core types and those from the dependencies.

Note: this is in the context of a module, so some core types may be hidden.

#### Returns

[`File`](File.md)

***

### kind()

> **kind**(): `Promise`\<[`ModuleSourceKind`](../enumerations/ModuleSourceKind.md)\>

The kind of module source (currently local, git or dir).

#### Returns

`Promise`\<[`ModuleSourceKind`](../enumerations/ModuleSourceKind.md)\>

***

### localContextDirectoryPath()

> **localContextDirectoryPath**(): `Promise`\<`string`\>

The full absolute path to the context directory on the caller's host filesystem that this module source is loaded from. Only valid for local module sources.

#### Returns

`Promise`\<`string`\>

***

### moduleName()

> **moduleName**(): `Promise`\<`string`\>

The name of the module, including any setting via the withName API.

#### Returns

`Promise`\<`string`\>

***

### moduleOriginalName()

> **moduleOriginalName**(): `Promise`\<`string`\>

The original name of the module as read from the module's dagger.json (or set for the first time with the withName API).

#### Returns

`Promise`\<`string`\>

***

### originalSubpath()

> **originalSubpath**(): `Promise`\<`string`\>

The original subpath used when instantiating this module source, relative to the context directory.

#### Returns

`Promise`\<`string`\>

***

### pin()

> **pin**(): `Promise`\<`string`\>

The pinned version of this module source.

#### Returns

`Promise`\<`string`\>

***

### repoRootPath()

> **repoRootPath**(): `Promise`\<`string`\>

The import path corresponding to the root of the git repo this source points to. Only valid for git sources.

#### Returns

`Promise`\<`string`\>

***

### sdk()

> **sdk**(): [`SDKConfig`](SDKConfig.md)

The SDK configuration of the module.

#### Returns

[`SDKConfig`](SDKConfig.md)

***

### sourceRootSubpath()

> **sourceRootSubpath**(): `Promise`\<`string`\>

The path, relative to the context directory, that contains the module's dagger.json.

#### Returns

`Promise`\<`string`\>

***

### sourceSubpath()

> **sourceSubpath**(): `Promise`\<`string`\>

The path to the directory containing the module's source code, relative to the context directory.

#### Returns

`Promise`\<`string`\>

***

### sync()

> **sync**(): `Promise`\<`ModuleSource`\>

Forces evaluation of the module source, including any loading into the engine and associated validation.

#### Returns

`Promise`\<`ModuleSource`\>

***

### toolchains()

> **toolchains**(): `Promise`\<`ModuleSource`[]\>

The toolchains referenced by the module source.

#### Returns

`Promise`\<`ModuleSource`[]\>

***

### userDefaults()

> **userDefaults**(): [`EnvFile`](EnvFile.md)

User-defined defaults read from local .env files

#### Returns

[`EnvFile`](EnvFile.md)

***

### version()

> **version**(): `Promise`\<`string`\>

The specified version of the git repo this source points to.

#### Returns

`Promise`\<`string`\>

***

### with()

> **with**(`arg`): `ModuleSource`

Call the provided function with current ModuleSource.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `ModuleSource`

#### Returns

`ModuleSource`

***

### withBlueprint()

> **withBlueprint**(`blueprint`): `ModuleSource`

Set a blueprint for the module source.

#### Parameters

##### blueprint

`ModuleSource`

The blueprint module to set.

#### Returns

`ModuleSource`

***

### withClient()

> **withClient**(`generator`, `outputDir`): `ModuleSource`

Update the module source with a new client to generate.

#### Parameters

##### generator

`string`

The generator to use

##### outputDir

`string`

The output directory for the generated client.

#### Returns

`ModuleSource`

***

### withDependencies()

> **withDependencies**(`dependencies`): `ModuleSource`

Append the provided dependencies to the module source's dependency list.

#### Parameters

##### dependencies

`ModuleSource`[]

The dependencies to append.

#### Returns

`ModuleSource`

***

### withEngineVersion()

> **withEngineVersion**(`version`): `ModuleSource`

Upgrade the engine version of the module to the given value.

#### Parameters

##### version

`string`

The engine version to upgrade to.

#### Returns

`ModuleSource`

***

### withExperimentalFeatures()

> **withExperimentalFeatures**(`features`): `ModuleSource`

Enable the experimental features for the module source.

#### Parameters

##### features

[`SelfCalls`](../enumerations/ModuleSourceExperimentalFeature.md#selfcalls)[]

The experimental features to enable.

#### Returns

`ModuleSource`

***

### withIncludes()

> **withIncludes**(`patterns`): `ModuleSource`

Update the module source with additional include patterns for files+directories from its context that are required for building it

#### Parameters

##### patterns

`string`[]

The new additional include patterns.

#### Returns

`ModuleSource`

***

### withName()

> **withName**(`name`): `ModuleSource`

Update the module source with a new name.

#### Parameters

##### name

`string`

The name to set.

#### Returns

`ModuleSource`

***

### withoutBlueprint()

> **withoutBlueprint**(): `ModuleSource`

Remove the current blueprint from the module source.

#### Returns

`ModuleSource`

***

### withoutClient()

> **withoutClient**(`path`): `ModuleSource`

Remove a client from the module source.

#### Parameters

##### path

`string`

The path of the client to remove.

#### Returns

`ModuleSource`

***

### withoutDependencies()

> **withoutDependencies**(`dependencies`): `ModuleSource`

Remove the provided dependencies from the module source's dependency list.

#### Parameters

##### dependencies

`string`[]

The dependencies to remove.

#### Returns

`ModuleSource`

***

### withoutExperimentalFeatures()

> **withoutExperimentalFeatures**(`features`): `ModuleSource`

Disable experimental features for the module source.

#### Parameters

##### features

[`SelfCalls`](../enumerations/ModuleSourceExperimentalFeature.md#selfcalls)[]

The experimental features to disable.

#### Returns

`ModuleSource`

***

### withoutToolchains()

> **withoutToolchains**(`toolchains`): `ModuleSource`

Remove the provided toolchains from the module source.

#### Parameters

##### toolchains

`string`[]

The toolchains to remove.

#### Returns

`ModuleSource`

***

### withSDK()

> **withSDK**(`source`): `ModuleSource`

Update the module source with a new SDK.

#### Parameters

##### source

`string`

The SDK source to set.

#### Returns

`ModuleSource`

***

### withSourceSubpath()

> **withSourceSubpath**(`path`): `ModuleSource`

Update the module source with a new source subpath.

#### Parameters

##### path

`string`

The path to set as the source subpath. Must be relative to the module source's source root directory.

#### Returns

`ModuleSource`

***

### withToolchains()

> **withToolchains**(`toolchains`): `ModuleSource`

Add toolchains to the module source.

#### Parameters

##### toolchains

`ModuleSource`[]

The toolchain modules to add.

#### Returns

`ModuleSource`

***

### withUpdateBlueprint()

> **withUpdateBlueprint**(): `ModuleSource`

Update the blueprint module to the latest version.

#### Returns

`ModuleSource`

***

### withUpdatedClients()

> **withUpdatedClients**(`clients`): `ModuleSource`

Update one or more clients.

#### Parameters

##### clients

`string`[]

The clients to update

#### Returns

`ModuleSource`

***

### withUpdateDependencies()

> **withUpdateDependencies**(`dependencies`): `ModuleSource`

Update one or more module dependencies.

#### Parameters

##### dependencies

`string`[]

The dependencies to update.

#### Returns

`ModuleSource`

***

### withUpdateToolchains()

> **withUpdateToolchains**(`toolchains`): `ModuleSource`

Update one or more toolchains.

#### Parameters

##### toolchains

`string`[]

The toolchains to update.

#### Returns

`ModuleSource`
