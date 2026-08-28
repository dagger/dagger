---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: CurrentModule

Reflective module API provided to functions at runtime.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new CurrentModule**(`ctx?`, `_id?`, `_name?`): `CurrentModule`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_name?

`string`

#### Returns

`CurrentModule`

#### Overrides

`BaseClient.constructor`

## Methods

### dependencies()

> **dependencies**(): `Promise`\<[`Module_`](Module.md)[]\>

The dependencies of the module.

#### Returns

`Promise`\<[`Module_`](Module.md)[]\>

***

### generatedContextDirectory()

> **generatedContextDirectory**(): [`Directory`](Directory.md)

The generated files and directories made on top of the module source's context directory.

#### Returns

[`Directory`](Directory.md)

***

### generators()

> **generators**(`opts?`): [`GeneratorGroup`](GeneratorGroup.md)

**`Experimental`**

Return all generators defined by the module

#### Parameters

##### opts?

[`CurrentModuleGeneratorsOpts`](../type-aliases/CurrentModuleGeneratorsOpts.md)

#### Returns

[`GeneratorGroup`](GeneratorGroup.md)

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this CurrentModule.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the module being executed in

#### Returns

`Promise`\<`string`\>

***

### source()

> **source**(): [`Directory`](Directory.md)

The directory containing the module's source code loaded into the engine (plus any generated code that may have been created).

#### Returns

[`Directory`](Directory.md)

***

### workdir()

> **workdir**(`path`, `opts?`): [`Directory`](Directory.md)

Load a directory from the module's scratch working directory, including any changes that may have been made to it during module function execution.

#### Parameters

##### path

`string`

Location of the directory to access (e.g., ".").

##### opts?

[`CurrentModuleWorkdirOpts`](../type-aliases/CurrentModuleWorkdirOpts.md)

#### Returns

[`Directory`](Directory.md)

***

### workdirFile()

> **workdirFile**(`path`): [`File`](File.md)

Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.Load a file from the module's scratch working directory, including any changes that may have been made to it during module function execution.

#### Parameters

##### path

`string`

Location of the file to retrieve (e.g., "README.md").

#### Returns

[`File`](File.md)
