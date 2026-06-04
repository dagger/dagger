[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Module\_

# Class: Module\_

A Dagger module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Module\_**(`ctx?`, `_id?`, `_description?`, `_name?`, `_serve?`, `_sync?`): `Module_`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ModuleID`](../type-aliases/ModuleID.md)

##### \_description?

`string`

##### \_name?

`string`

##### \_serve?

[`Void`](../type-aliases/Void.md)

##### \_sync?

[`ModuleID`](../type-aliases/ModuleID.md)

#### Returns

`Module_`

#### Overrides

`BaseClient.constructor`

## Methods

### check()

> **check**(`name`): [`Check`](Check.md)

**`Experimental`**

Return the check defined by the module with the given name. Must match to exactly one check.

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

Return all checks defined by the module

#### Parameters

##### opts?

[`ModuleChecksOpts`](../type-aliases/ModuleChecksOpts.md)

#### Returns

[`CheckGroup`](CheckGroup.md)

***

### dependencies()

> **dependencies**(): `Promise`\<`Module_`[]\>

The dependencies of the module.

#### Returns

`Promise`\<`Module_`[]\>

***

### description()

> **description**(): `Promise`\<`string`\>

The doc string of the module, if any

#### Returns

`Promise`\<`string`\>

***

### enums()

> **enums**(): `Promise`\<[`TypeDef`](TypeDef.md)[]\>

Enumerations served by this module.

#### Returns

`Promise`\<[`TypeDef`](TypeDef.md)[]\>

***

### generatedContextDirectory()

> **generatedContextDirectory**(): [`Directory`](Directory.md)

The generated files and directories made on top of the module source's context directory.

#### Returns

[`Directory`](Directory.md)

***

### generator()

> **generator**(`name`): [`Generator`](Generator.md)

**`Experimental`**

Return the generator defined by the module with the given name. Must match to exactly one generator.

#### Parameters

##### name

`string`

The name of the generator to retrieve

#### Returns

[`Generator`](Generator.md)

***

### generators()

> **generators**(`opts?`): [`GeneratorGroup`](GeneratorGroup.md)

**`Experimental`**

Return all generators defined by the module

#### Parameters

##### opts?

[`ModuleGeneratorsOpts`](../type-aliases/ModuleGeneratorsOpts.md)

#### Returns

[`GeneratorGroup`](GeneratorGroup.md)

***

### id()

> **id**(): `Promise`\<[`ModuleID`](../type-aliases/ModuleID.md)\>

A unique identifier for this Module.

#### Returns

`Promise`\<[`ModuleID`](../type-aliases/ModuleID.md)\>

***

### interfaces()

> **interfaces**(): `Promise`\<[`TypeDef`](TypeDef.md)[]\>

Interfaces served by this module.

#### Returns

`Promise`\<[`TypeDef`](TypeDef.md)[]\>

***

### introspectionSchemaJSON()

> **introspectionSchemaJSON**(): [`File`](File.md)

The introspection schema JSON file for this module.

This file represents the schema visible to the module's source code, including all core types and those from the dependencies.

Note: this is in the context of a module, so some core types may be hidden.

#### Returns

[`File`](File.md)

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the module

#### Returns

`Promise`\<`string`\>

***

### objects()

> **objects**(): `Promise`\<[`TypeDef`](TypeDef.md)[]\>

Objects served by this module.

#### Returns

`Promise`\<[`TypeDef`](TypeDef.md)[]\>

***

### runtime()

> **runtime**(): [`Container`](Container.md)

The container that runs the module's entrypoint. It will fail to execute if the module doesn't compile.

#### Returns

[`Container`](Container.md)

***

### sdk()

> **sdk**(): [`SDKConfig`](SDKConfig.md)

The SDK config used by this module.

#### Returns

[`SDKConfig`](SDKConfig.md)

***

### serve()

> **serve**(`opts?`): `Promise`\<`void`\>

Serve a module's API in the current session.

Note: this can only be called once per session. In the future, it could return a stream or service to remove the side effect.

#### Parameters

##### opts?

[`ModuleServeOpts`](../type-aliases/ModuleServeOpts.md)

#### Returns

`Promise`\<`void`\>

***

### source()

> **source**(): [`ModuleSource`](ModuleSource.md)

The source for the module.

#### Returns

[`ModuleSource`](ModuleSource.md)

***

### sync()

> **sync**(): `Promise`\<`Module_`\>

Forces evaluation of the module, including any loading into the engine and associated validation.

#### Returns

`Promise`\<`Module_`\>

***

### userDefaults()

> **userDefaults**(): [`EnvFile`](EnvFile.md)

User-defined default values, loaded from local .env files.

#### Returns

[`EnvFile`](EnvFile.md)

***

### with()

> **with**(`arg`): `Module_`

Call the provided function with current Module.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Module_`

#### Returns

`Module_`

***

### withDescription()

> **withDescription**(`description`): `Module_`

Retrieves the module with the given description

#### Parameters

##### description

`string`

The description to set

#### Returns

`Module_`

***

### withEnum()

> **withEnum**(`enum_`): `Module_`

This module plus the given Enum type and associated values

#### Parameters

##### enum\_

[`TypeDef`](TypeDef.md)

#### Returns

`Module_`

***

### withInterface()

> **withInterface**(`iface`): `Module_`

This module plus the given Interface type and associated functions

#### Parameters

##### iface

[`TypeDef`](TypeDef.md)

#### Returns

`Module_`

***

### withObject()

> **withObject**(`object`): `Module_`

This module plus the given Object type and associated functions.

#### Parameters

##### object

[`TypeDef`](TypeDef.md)

#### Returns

`Module_`
