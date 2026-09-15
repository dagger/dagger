[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Env

# Class: Env

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Env**(`ctx?`, `_id?`): `Env`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`EnvID`](../type-aliases/EnvID.md)

#### Returns

`Env`

#### Overrides

`BaseClient.constructor`

## Methods

### check()

> **check**(`name`): [`Check`](Check.md)

**`Experimental`**

Return the check with the given name from the installed modules. Must match exactly one check.

#### Parameters

##### name

`string`

The name of the check to retrieve

#### Returns

[`Check`](Check.md)

***

### checks()

> **checks**(`opts?`): [`CheckGroup`](CheckGroup.md)

**`Experimental`**

Return all checks defined by the installed modules

#### Parameters

##### opts?

[`EnvChecksOpts`](../type-aliases/EnvChecksOpts.md)

#### Returns

[`CheckGroup`](CheckGroup.md)

***

### id()

> **id**(): `Promise`\<[`EnvID`](../type-aliases/EnvID.md)\>

A unique identifier for this Env.

#### Returns

`Promise`\<[`EnvID`](../type-aliases/EnvID.md)\>

***

### input()

> **input**(`name`): [`Binding`](Binding.md)

Retrieves an input binding by name

#### Parameters

##### name

`string`

#### Returns

[`Binding`](Binding.md)

***

### inputs()

> **inputs**(): `Promise`\<[`Binding`](Binding.md)[]\>

Returns all input bindings provided to the environment

#### Returns

`Promise`\<[`Binding`](Binding.md)[]\>

***

### output()

> **output**(`name`): [`Binding`](Binding.md)

Retrieves an output binding by name

#### Parameters

##### name

`string`

#### Returns

[`Binding`](Binding.md)

***

### outputs()

> **outputs**(): `Promise`\<[`Binding`](Binding.md)[]\>

Returns all declared output bindings for the environment

#### Returns

`Promise`\<[`Binding`](Binding.md)[]\>

***

### with()

> **with**(`arg`): `Env`

Call the provided function with current Env.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Env`

#### Returns

`Env`

***

### withAddressInput()

> **withAddressInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Address in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Address`](Address.md)

The Address value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withAddressOutput()

> **withAddressOutput**(`name`, `description`): `Env`

Declare a desired Address output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withCacheVolumeInput()

> **withCacheVolumeInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type CacheVolume in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`CacheVolume`](CacheVolume.md)

The CacheVolume value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withCacheVolumeOutput()

> **withCacheVolumeOutput**(`name`, `description`): `Env`

Declare a desired CacheVolume output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withChangesetInput()

> **withChangesetInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Changeset in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Changeset`](Changeset.md)

The Changeset value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withChangesetOutput()

> **withChangesetOutput**(`name`, `description`): `Env`

Declare a desired Changeset output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withCheckGroupInput()

> **withCheckGroupInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type CheckGroup in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`CheckGroup`](CheckGroup.md)

The CheckGroup value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withCheckGroupOutput()

> **withCheckGroupOutput**(`name`, `description`): `Env`

Declare a desired CheckGroup output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withCheckInput()

> **withCheckInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Check in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Check`](Check.md)

The Check value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withCheckOutput()

> **withCheckOutput**(`name`, `description`): `Env`

Declare a desired Check output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withCloudInput()

> **withCloudInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Cloud in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Cloud`](Cloud.md)

The Cloud value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withCloudOutput()

> **withCloudOutput**(`name`, `description`): `Env`

Declare a desired Cloud output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withContainerInput()

> **withContainerInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Container in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Container`](Container.md)

The Container value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withContainerOutput()

> **withContainerOutput**(`name`, `description`): `Env`

Declare a desired Container output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withCurrentModule()

> **withCurrentModule**(): `Env`

Installs the current module into the environment, exposing its functions to the model

Contextual path arguments will be populated using the environment's workspace.

#### Returns

`Env`

***

### withDirectoryInput()

> **withDirectoryInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Directory in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Directory`](Directory.md)

The Directory value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withDirectoryOutput()

> **withDirectoryOutput**(`name`, `description`): `Env`

Declare a desired Directory output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withEnvFileInput()

> **withEnvFileInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type EnvFile in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`EnvFile`](EnvFile.md)

The EnvFile value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withEnvFileOutput()

> **withEnvFileOutput**(`name`, `description`): `Env`

Declare a desired EnvFile output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withEnvInput()

> **withEnvInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Env in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

`Env`

The Env value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withEnvOutput()

> **withEnvOutput**(`name`, `description`): `Env`

Declare a desired Env output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withFileInput()

> **withFileInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type File in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`File`](File.md)

The File value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withFileOutput()

> **withFileOutput**(`name`, `description`): `Env`

Declare a desired File output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withGeneratorGroupInput()

> **withGeneratorGroupInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type GeneratorGroup in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`GeneratorGroup`](GeneratorGroup.md)

The GeneratorGroup value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withGeneratorGroupOutput()

> **withGeneratorGroupOutput**(`name`, `description`): `Env`

Declare a desired GeneratorGroup output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withGeneratorInput()

> **withGeneratorInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Generator in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Generator`](Generator.md)

The Generator value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withGeneratorOutput()

> **withGeneratorOutput**(`name`, `description`): `Env`

Declare a desired Generator output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withGitRefInput()

> **withGitRefInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type GitRef in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`GitRef`](GitRef.md)

The GitRef value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withGitRefOutput()

> **withGitRefOutput**(`name`, `description`): `Env`

Declare a desired GitRef output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withGitRepositoryInput()

> **withGitRepositoryInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type GitRepository in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`GitRepository`](GitRepository.md)

The GitRepository value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withGitRepositoryOutput()

> **withGitRepositoryOutput**(`name`, `description`): `Env`

Declare a desired GitRepository output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withJSONValueInput()

> **withJSONValueInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type JSONValue in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`JSONValue`](JSONValue.md)

The JSONValue value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withJSONValueOutput()

> **withJSONValueOutput**(`name`, `description`): `Env`

Declare a desired JSONValue output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withMainModule()

> **withMainModule**(`module_`): `Env`

Sets the main module for this environment (the project being worked on)

Contextual path arguments will be populated using the environment's workspace.

#### Parameters

##### module\_

[`Module_`](Module.md)

#### Returns

`Env`

***

### ~~withModule()~~

> **withModule**(`module_`): `Env`

Installs a module into the environment, exposing its functions to the model

Contextual path arguments will be populated using the environment's workspace.

#### Parameters

##### module\_

[`Module_`](Module.md)

#### Returns

`Env`

#### Deprecated

Use withMainModule instead

***

### withModuleConfigClientInput()

> **withModuleConfigClientInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type ModuleConfigClient in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`ModuleConfigClient`](ModuleConfigClient.md)

The ModuleConfigClient value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withModuleConfigClientOutput()

> **withModuleConfigClientOutput**(`name`, `description`): `Env`

Declare a desired ModuleConfigClient output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withModuleInput()

> **withModuleInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Module in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Module_`](Module.md)

The Module value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withModuleOutput()

> **withModuleOutput**(`name`, `description`): `Env`

Declare a desired Module output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withModuleSourceInput()

> **withModuleSourceInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type ModuleSource in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`ModuleSource`](ModuleSource.md)

The ModuleSource value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withModuleSourceOutput()

> **withModuleSourceOutput**(`name`, `description`): `Env`

Declare a desired ModuleSource output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withoutOutputs()

> **withoutOutputs**(): `Env`

Returns a new environment without any outputs

#### Returns

`Env`

***

### withSearchResultInput()

> **withSearchResultInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type SearchResult in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`SearchResult`](SearchResult.md)

The SearchResult value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withSearchResultOutput()

> **withSearchResultOutput**(`name`, `description`): `Env`

Declare a desired SearchResult output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withSearchSubmatchInput()

> **withSearchSubmatchInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type SearchSubmatch in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`SearchSubmatch`](SearchSubmatch.md)

The SearchSubmatch value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withSearchSubmatchOutput()

> **withSearchSubmatchOutput**(`name`, `description`): `Env`

Declare a desired SearchSubmatch output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withSecretInput()

> **withSecretInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Secret in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Secret`](Secret.md)

The Secret value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withSecretOutput()

> **withSecretOutput**(`name`, `description`): `Env`

Declare a desired Secret output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withServiceInput()

> **withServiceInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Service in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Service`](Service.md)

The Service value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withServiceOutput()

> **withServiceOutput**(`name`, `description`): `Env`

Declare a desired Service output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withSocketInput()

> **withSocketInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Socket in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Socket`](Socket.md)

The Socket value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withSocketOutput()

> **withSocketOutput**(`name`, `description`): `Env`

Declare a desired Socket output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withStatInput()

> **withStatInput**(`name`, `value`, `description`): `Env`

Create or update a binding of type Stat in the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

[`Stat`](Stat.md)

The Stat value to assign to the binding

##### description

`string`

The purpose of the input

#### Returns

`Env`

***

### withStatOutput()

> **withStatOutput**(`name`, `description`): `Env`

Declare a desired Stat output to be assigned in the environment

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

A description of the desired value of the binding

#### Returns

`Env`

***

### withStringInput()

> **withStringInput**(`name`, `value`, `description`): `Env`

Provides a string input binding to the environment

#### Parameters

##### name

`string`

The name of the binding

##### value

`string`

The string value to assign to the binding

##### description

`string`

The description of the input

#### Returns

`Env`

***

### withStringOutput()

> **withStringOutput**(`name`, `description`): `Env`

Declares a desired string output binding

#### Parameters

##### name

`string`

The name of the binding

##### description

`string`

The description of the output

#### Returns

`Env`

***

### withWorkspace()

> **withWorkspace**(`workspace`): `Env`

Returns a new environment with the provided workspace

#### Parameters

##### workspace

[`Directory`](Directory.md)

The directory to set as the host filesystem

#### Returns

`Env`

***

### workspace()

> **workspace**(): [`Directory`](Directory.md)

#### Returns

[`Directory`](Directory.md)
