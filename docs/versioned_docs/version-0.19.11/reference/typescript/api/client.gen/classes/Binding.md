[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Binding

# Class: Binding

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Binding**(`ctx?`, `_id?`, `_asString?`, `_digest?`, `_isNull?`, `_name?`, `_typeName?`): `Binding`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`BindingID`](../type-aliases/BindingID.md)

##### \_asString?

`string`

##### \_digest?

`string`

##### \_isNull?

`boolean`

##### \_name?

`string`

##### \_typeName?

`string`

#### Returns

`Binding`

#### Overrides

`BaseClient.constructor`

## Methods

### asAddress()

> **asAddress**(): [`Address`](Address.md)

Retrieve the binding value, as type Address

#### Returns

[`Address`](Address.md)

***

### asCacheVolume()

> **asCacheVolume**(): [`CacheVolume`](CacheVolume.md)

Retrieve the binding value, as type CacheVolume

#### Returns

[`CacheVolume`](CacheVolume.md)

***

### asChangeset()

> **asChangeset**(): [`Changeset`](Changeset.md)

Retrieve the binding value, as type Changeset

#### Returns

[`Changeset`](Changeset.md)

***

### asCheck()

> **asCheck**(): [`Check`](Check.md)

Retrieve the binding value, as type Check

#### Returns

[`Check`](Check.md)

***

### asCheckGroup()

> **asCheckGroup**(): [`CheckGroup`](CheckGroup.md)

Retrieve the binding value, as type CheckGroup

#### Returns

[`CheckGroup`](CheckGroup.md)

***

### asCloud()

> **asCloud**(): [`Cloud`](Cloud.md)

Retrieve the binding value, as type Cloud

#### Returns

[`Cloud`](Cloud.md)

***

### asContainer()

> **asContainer**(): [`Container`](Container.md)

Retrieve the binding value, as type Container

#### Returns

[`Container`](Container.md)

***

### asDirectory()

> **asDirectory**(): [`Directory`](Directory.md)

Retrieve the binding value, as type Directory

#### Returns

[`Directory`](Directory.md)

***

### asEnv()

> **asEnv**(): [`Env`](Env.md)

Retrieve the binding value, as type Env

#### Returns

[`Env`](Env.md)

***

### asEnvFile()

> **asEnvFile**(): [`EnvFile`](EnvFile.md)

Retrieve the binding value, as type EnvFile

#### Returns

[`EnvFile`](EnvFile.md)

***

### asFile()

> **asFile**(): [`File`](File.md)

Retrieve the binding value, as type File

#### Returns

[`File`](File.md)

***

### asGenerator()

> **asGenerator**(): [`Generator`](Generator.md)

Retrieve the binding value, as type Generator

#### Returns

[`Generator`](Generator.md)

***

### asGeneratorGroup()

> **asGeneratorGroup**(): [`GeneratorGroup`](GeneratorGroup.md)

Retrieve the binding value, as type GeneratorGroup

#### Returns

[`GeneratorGroup`](GeneratorGroup.md)

***

### asGitRef()

> **asGitRef**(): [`GitRef`](GitRef.md)

Retrieve the binding value, as type GitRef

#### Returns

[`GitRef`](GitRef.md)

***

### asGitRepository()

> **asGitRepository**(): [`GitRepository`](GitRepository.md)

Retrieve the binding value, as type GitRepository

#### Returns

[`GitRepository`](GitRepository.md)

***

### asJSONValue()

> **asJSONValue**(): [`JSONValue`](JSONValue.md)

Retrieve the binding value, as type JSONValue

#### Returns

[`JSONValue`](JSONValue.md)

***

### asModule()

> **asModule**(): [`Module_`](Module.md)

Retrieve the binding value, as type Module

#### Returns

[`Module_`](Module.md)

***

### asModuleConfigClient()

> **asModuleConfigClient**(): [`ModuleConfigClient`](ModuleConfigClient.md)

Retrieve the binding value, as type ModuleConfigClient

#### Returns

[`ModuleConfigClient`](ModuleConfigClient.md)

***

### asModuleSource()

> **asModuleSource**(): [`ModuleSource`](ModuleSource.md)

Retrieve the binding value, as type ModuleSource

#### Returns

[`ModuleSource`](ModuleSource.md)

***

### asSearchResult()

> **asSearchResult**(): [`SearchResult`](SearchResult.md)

Retrieve the binding value, as type SearchResult

#### Returns

[`SearchResult`](SearchResult.md)

***

### asSearchSubmatch()

> **asSearchSubmatch**(): [`SearchSubmatch`](SearchSubmatch.md)

Retrieve the binding value, as type SearchSubmatch

#### Returns

[`SearchSubmatch`](SearchSubmatch.md)

***

### asSecret()

> **asSecret**(): [`Secret`](Secret.md)

Retrieve the binding value, as type Secret

#### Returns

[`Secret`](Secret.md)

***

### asService()

> **asService**(): [`Service`](Service.md)

Retrieve the binding value, as type Service

#### Returns

[`Service`](Service.md)

***

### asSocket()

> **asSocket**(): [`Socket`](Socket.md)

Retrieve the binding value, as type Socket

#### Returns

[`Socket`](Socket.md)

***

### asStat()

> **asStat**(): [`Stat`](Stat.md)

Retrieve the binding value, as type Stat

#### Returns

[`Stat`](Stat.md)

***

### asString()

> **asString**(): `Promise`\<`string`\>

Returns the binding's string value

#### Returns

`Promise`\<`string`\>

***

### digest()

> **digest**(): `Promise`\<`string`\>

Returns the digest of the binding value

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`BindingID`](../type-aliases/BindingID.md)\>

A unique identifier for this Binding.

#### Returns

`Promise`\<[`BindingID`](../type-aliases/BindingID.md)\>

***

### isNull()

> **isNull**(): `Promise`\<`boolean`\>

Returns true if the binding is null

#### Returns

`Promise`\<`boolean`\>

***

### name()

> **name**(): `Promise`\<`string`\>

Returns the binding name

#### Returns

`Promise`\<`string`\>

***

### typeName()

> **typeName**(): `Promise`\<`string`\>

Returns the binding type

#### Returns

`Promise`\<`string`\>
