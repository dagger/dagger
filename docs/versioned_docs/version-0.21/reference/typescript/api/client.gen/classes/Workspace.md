---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Workspace

A Dagger workspace detected from the current working directory.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Workspace**(`ctx?`, `_id?`, `_address?`, `_clientId?`, `_configPath?`, `_findUp?`, `_hasConfig?`, `_initialized?`, `_path?`): `Workspace`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_address?

`string`

##### \_clientId?

`string`

##### \_configPath?

`string`

##### \_findUp?

`string`

##### \_hasConfig?

`boolean`

##### \_initialized?

`boolean`

##### \_path?

`string`

#### Returns

`Workspace`

#### Overrides

`BaseClient.constructor`

## Methods

### address()

> **address**(): `Promise`\<`string`\>

Canonical Dagger address of the workspace directory.

#### Returns

`Promise`\<`string`\>

***

### checks()

> **checks**(`opts?`): [`CheckGroup`](CheckGroup.md)

Return all checks from modules loaded in the workspace.

#### Parameters

##### opts?

[`WorkspaceChecksOpts`](../type-aliases/WorkspaceChecksOpts.md)

#### Returns

[`CheckGroup`](CheckGroup.md)

***

### clientId()

> **clientId**(): `Promise`\<`string`\>

The client ID that owns this workspace's host filesystem.

#### Returns

`Promise`\<`string`\>

***

### configPath()

> **configPath**(): `Promise`\<`string`\>

Path to config.toml relative to the workspace boundary (empty if not initialized).

#### Returns

`Promise`\<`string`\>

***

### directory()

> **directory**(`path`, `opts?`): [`Directory`](Directory.md)

Returns a Directory from the workspace.

Relative paths resolve from the workspace directory. Absolute paths resolve from the workspace boundary.

#### Parameters

##### path

`string`

Location of the directory to retrieve. Relative paths (e.g., "src") resolve from the workspace directory; absolute paths (e.g., "/src") resolve from the workspace boundary.

##### opts?

[`WorkspaceDirectoryOpts`](../type-aliases/WorkspaceDirectoryOpts.md)

#### Returns

[`Directory`](Directory.md)

***

### file()

> **file**(`path`): [`File`](File.md)

Returns a File from the workspace.

Relative paths resolve from the workspace directory. Absolute paths resolve from the workspace boundary.

#### Parameters

##### path

`string`

Location of the file to retrieve. Relative paths (e.g., "go.mod") resolve from the workspace directory; absolute paths (e.g., "/go.mod") resolve from the workspace boundary.

#### Returns

[`File`](File.md)

***

### findUp()

> **findUp**(`name`, `opts?`): `Promise`\<`string`\>

Search for a file or directory by walking up from the start path within the workspace.

Returns the absolute workspace path if found, or null if not found.

Relative start paths resolve from the workspace directory.

The search stops at the workspace boundary and will not traverse above it.

#### Parameters

##### name

`string`

The name of the file or directory to search for.

##### opts?

[`WorkspaceFindUpOpts`](../type-aliases/WorkspaceFindUpOpts.md)

#### Returns

`Promise`\<`string`\>

***

### generators()

> **generators**(`opts?`): [`GeneratorGroup`](GeneratorGroup.md)

Return all generators from modules loaded in the workspace.

#### Parameters

##### opts?

[`WorkspaceGeneratorsOpts`](../type-aliases/WorkspaceGeneratorsOpts.md)

#### Returns

[`GeneratorGroup`](GeneratorGroup.md)

***

### hasConfig()

> **hasConfig**(): `Promise`\<`boolean`\>

Whether a config.toml file exists in the workspace.

#### Returns

`Promise`\<`boolean`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Workspace.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### initialized()

> **initialized**(): `Promise`\<`boolean`\>

Whether .dagger/config.toml exists.

#### Returns

`Promise`\<`boolean`\>

***

### path()

> **path**(): `Promise`\<`string`\>

Workspace directory path relative to the workspace boundary.

#### Returns

`Promise`\<`string`\>

***

### services()

> **services**(`opts?`): [`UpGroup`](UpGroup.md)

Return all services from modules loaded in the workspace.

#### Parameters

##### opts?

[`WorkspaceServicesOpts`](../type-aliases/WorkspaceServicesOpts.md)

#### Returns

[`UpGroup`](UpGroup.md)

***

### update()

> **update**(): [`Changeset`](Changeset.md)

**`Experimental`**

Refresh workspace-managed state and return the resulting changeset.

Currently this refreshes existing lockfile entries only.

#### Returns

[`Changeset`](Changeset.md)
