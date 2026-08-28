[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Workspace

# Class: Workspace

A Dagger workspace detected from the current working directory.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Workspace**(`ctx?`, `_id?`, `_clientId?`, `_findUp?`, `_root?`): `Workspace`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`WorkspaceID`](../type-aliases/WorkspaceID.md)

##### \_clientId?

`string`

##### \_findUp?

`string`

##### \_root?

`string`

#### Returns

`Workspace`

#### Overrides

`BaseClient.constructor`

## Methods

### clientId()

> **clientId**(): `Promise`\<`string`\>

The client ID that owns this workspace's host filesystem.

#### Returns

`Promise`\<`string`\>

***

### directory()

> **directory**(`path`, `opts?`): [`Directory`](Directory.md)

Returns a Directory from the workspace.

Path is relative to workspace root. Use "." for the root directory.

#### Parameters

##### path

`string`

Location of the directory to retrieve, relative to the workspace root (e.g., "src", ".").

##### opts?

[`WorkspaceDirectoryOpts`](../type-aliases/WorkspaceDirectoryOpts.md)

#### Returns

[`Directory`](Directory.md)

***

### file()

> **file**(`path`): [`File`](File.md)

Returns a File from the workspace.

Path is relative to workspace root.

#### Parameters

##### path

`string`

Location of the file to retrieve, relative to the workspace root (e.g., "go.mod").

#### Returns

[`File`](File.md)

***

### findUp()

> **findUp**(`name`, `opts?`): `Promise`\<`string`\>

Search for a file or directory by walking up from the start path within the workspace.

Returns the path relative to the workspace root if found, or null if not found.

The search stops at the workspace root and will not traverse above it.

#### Parameters

##### name

`string`

The name of the file or directory to search for.

##### opts?

[`WorkspaceFindUpOpts`](../type-aliases/WorkspaceFindUpOpts.md)

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`WorkspaceID`](../type-aliases/WorkspaceID.md)\>

A unique identifier for this Workspace.

#### Returns

`Promise`\<[`WorkspaceID`](../type-aliases/WorkspaceID.md)\>

***

### root()

> **root**(): `Promise`\<`string`\>

Absolute path to the workspace root directory.

#### Returns

`Promise`\<`string`\>
