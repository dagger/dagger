[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Directory

# Class: Directory

A directory.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Directory**(`ctx?`, `_id?`, `_digest?`, `_exists?`, `_export?`, `_findUp?`, `_name?`, `_sync?`): `Directory`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`DirectoryID`](../type-aliases/DirectoryID.md)

##### \_digest?

`string`

##### \_exists?

`boolean`

##### \_export?

`string`

##### \_findUp?

`string`

##### \_name?

`string`

##### \_sync?

[`DirectoryID`](../type-aliases/DirectoryID.md)

#### Returns

`Directory`

#### Overrides

`BaseClient.constructor`

## Methods

### asGit()

> **asGit**(): [`GitRepository`](GitRepository.md)

Converts this directory to a local git repository

#### Returns

[`GitRepository`](GitRepository.md)

***

### asModule()

> **asModule**(`opts?`): [`Module_`](Module.md)

Load the directory as a Dagger module source

#### Parameters

##### opts?

[`DirectoryAsModuleOpts`](../type-aliases/DirectoryAsModuleOpts.md)

#### Returns

[`Module_`](Module.md)

***

### asModuleSource()

> **asModuleSource**(`opts?`): [`ModuleSource`](ModuleSource.md)

Load the directory as a Dagger module source

#### Parameters

##### opts?

[`DirectoryAsModuleSourceOpts`](../type-aliases/DirectoryAsModuleSourceOpts.md)

#### Returns

[`ModuleSource`](ModuleSource.md)

***

### changes()

> **changes**(`from`): [`Changeset`](Changeset.md)

Return the difference between this directory and another directory, typically an older snapshot.

The difference is encoded as a changeset, which also tracks removed files, and can be applied to other directories.

#### Parameters

##### from

`Directory`

The base directory snapshot to compare against

#### Returns

[`Changeset`](Changeset.md)

***

### chown()

> **chown**(`path`, `owner`): `Directory`

Change the owner of the directory contents recursively.

#### Parameters

##### path

`string`

Path of the directory to change ownership of (e.g., "/").

##### owner

`string`

A user:group to set for the mounted directory and its contents.

The user and group must be an ID (1000:1000), not a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

#### Returns

`Directory`

***

### diff()

> **diff**(`other`): `Directory`

Return the difference between this directory and an another directory. The difference is encoded as a directory.

#### Parameters

##### other

`Directory`

The directory to compare against

#### Returns

`Directory`

***

### digest()

> **digest**(): `Promise`\<`string`\>

Return the directory's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.

#### Returns

`Promise`\<`string`\>

***

### directory()

> **directory**(`path`): `Directory`

Retrieves a directory at the given path.

#### Parameters

##### path

`string`

Location of the directory to retrieve. Example: "/src"

#### Returns

`Directory`

***

### dockerBuild()

> **dockerBuild**(`opts?`): [`Container`](Container.md)

Use Dockerfile compatibility to build a container from this directory. Only use this function for Dockerfile compatibility. Otherwise use the native Container type directly, it is feature-complete and supports all Dockerfile features.

#### Parameters

##### opts?

[`DirectoryDockerBuildOpts`](../type-aliases/DirectoryDockerBuildOpts.md)

#### Returns

[`Container`](Container.md)

***

### entries()

> **entries**(`opts?`): `Promise`\<`string`[]\>

Returns a list of files and directories at the given path.

#### Parameters

##### opts?

[`DirectoryEntriesOpts`](../type-aliases/DirectoryEntriesOpts.md)

#### Returns

`Promise`\<`string`[]\>

***

### exists()

> **exists**(`path`, `opts?`): `Promise`\<`boolean`\>

check if a file or directory exists

#### Parameters

##### path

`string`

Path to check (e.g., "/file.txt").

##### opts?

[`DirectoryExistsOpts`](../type-aliases/DirectoryExistsOpts.md)

#### Returns

`Promise`\<`boolean`\>

***

### export()

> **export**(`path`, `opts?`): `Promise`\<`string`\>

Writes the contents of the directory to a path on the host.

#### Parameters

##### path

`string`

Location of the copied directory (e.g., "logs/").

##### opts?

[`DirectoryExportOpts`](../type-aliases/DirectoryExportOpts.md)

#### Returns

`Promise`\<`string`\>

***

### file()

> **file**(`path`): [`File`](File.md)

Retrieve a file at the given path.

#### Parameters

##### path

`string`

Location of the file to retrieve (e.g., "README.md").

#### Returns

[`File`](File.md)

***

### filter()

> **filter**(`opts?`): `Directory`

Return a snapshot with some paths included or excluded

#### Parameters

##### opts?

[`DirectoryFilterOpts`](../type-aliases/DirectoryFilterOpts.md)

#### Returns

`Directory`

***

### findUp()

> **findUp**(`name`, `start`): `Promise`\<`string`\>

Search up the directory tree for a file or directory, and return its path. If no match, return null

#### Parameters

##### name

`string`

The name of the file or directory to search for

##### start

`string`

The path to start the search from

#### Returns

`Promise`\<`string`\>

***

### glob()

> **glob**(`pattern`): `Promise`\<`string`[]\>

Returns a list of files and directories that matche the given pattern.

#### Parameters

##### pattern

`string`

Pattern to match (e.g., "*.md").

#### Returns

`Promise`\<`string`[]\>

***

### id()

> **id**(): `Promise`\<[`DirectoryID`](../type-aliases/DirectoryID.md)\>

A unique identifier for this Directory.

#### Returns

`Promise`\<[`DirectoryID`](../type-aliases/DirectoryID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

Returns the name of the directory.

#### Returns

`Promise`\<`string`\>

***

### search()

> **search**(`opts?`): `Promise`\<[`SearchResult`](SearchResult.md)[]\>

Searches for content matching the given regular expression or literal string.

Uses Rust regex syntax; escape literal ., [, ], \{, \}, | with backslashes.

#### Parameters

##### opts?

[`DirectorySearchOpts`](../type-aliases/DirectorySearchOpts.md)

#### Returns

`Promise`\<[`SearchResult`](SearchResult.md)[]\>

***

### stat()

> **stat**(`path`, `opts?`): [`Stat`](Stat.md)

Return file status

#### Parameters

##### path

`string`

Path to stat (e.g., "/file.txt").

##### opts?

[`DirectoryStatOpts`](../type-aliases/DirectoryStatOpts.md)

#### Returns

[`Stat`](Stat.md)

***

### sync()

> **sync**(): `Promise`\<`Directory`\>

Force evaluation in the engine.

#### Returns

`Promise`\<`Directory`\>

***

### terminal()

> **terminal**(`opts?`): `Directory`

Opens an interactive terminal in new container with this directory mounted inside.

#### Parameters

##### opts?

[`DirectoryTerminalOpts`](../type-aliases/DirectoryTerminalOpts.md)

#### Returns

`Directory`

***

### with()

> **with**(`arg`): `Directory`

Call the provided function with current Directory.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Directory`

#### Returns

`Directory`

***

### withChanges()

> **withChanges**(`changes`): `Directory`

Return a directory with changes from another directory applied to it.

#### Parameters

##### changes

[`Changeset`](Changeset.md)

Changes to apply to the directory

#### Returns

`Directory`

***

### withDirectory()

> **withDirectory**(`path`, `source`, `opts?`): `Directory`

Return a snapshot with a directory added

#### Parameters

##### path

`string`

Location of the written directory (e.g., "/src/").

##### source

`Directory`

Identifier of the directory to copy.

##### opts?

[`DirectoryWithDirectoryOpts`](../type-aliases/DirectoryWithDirectoryOpts.md)

#### Returns

`Directory`

***

### withError()

> **withError**(`err`): `Directory`

Raise an error.

#### Parameters

##### err

`string`

Message of the error to raise. If empty, the error will be ignored.

#### Returns

`Directory`

***

### withFile()

> **withFile**(`path`, `source`, `opts?`): `Directory`

Retrieves this directory plus the contents of the given file copied to the given path.

#### Parameters

##### path

`string`

Location of the copied file (e.g., "/file.txt").

##### source

[`File`](File.md)

Identifier of the file to copy.

##### opts?

[`DirectoryWithFileOpts`](../type-aliases/DirectoryWithFileOpts.md)

#### Returns

`Directory`

***

### withFiles()

> **withFiles**(`path`, `sources`, `opts?`): `Directory`

Retrieves this directory plus the contents of the given files copied to the given path.

#### Parameters

##### path

`string`

Location where copied files should be placed (e.g., "/src").

##### sources

[`File`](File.md)[]

Identifiers of the files to copy.

##### opts?

[`DirectoryWithFilesOpts`](../type-aliases/DirectoryWithFilesOpts.md)

#### Returns

`Directory`

***

### withNewDirectory()

> **withNewDirectory**(`path`, `opts?`): `Directory`

Retrieves this directory plus a new directory created at the given path.

#### Parameters

##### path

`string`

Location of the directory created (e.g., "/logs").

##### opts?

[`DirectoryWithNewDirectoryOpts`](../type-aliases/DirectoryWithNewDirectoryOpts.md)

#### Returns

`Directory`

***

### withNewFile()

> **withNewFile**(`path`, `contents`, `opts?`): `Directory`

Return a snapshot with a new file added

#### Parameters

##### path

`string`

Path of the new file. Example: "foo/bar.txt"

##### contents

`string`

Contents of the new file. Example: "Hello world!"

##### opts?

[`DirectoryWithNewFileOpts`](../type-aliases/DirectoryWithNewFileOpts.md)

#### Returns

`Directory`

***

### withoutDirectory()

> **withoutDirectory**(`path`): `Directory`

Return a snapshot with a subdirectory removed

#### Parameters

##### path

`string`

Path of the subdirectory to remove. Example: ".github/workflows"

#### Returns

`Directory`

***

### withoutFile()

> **withoutFile**(`path`): `Directory`

Return a snapshot with a file removed

#### Parameters

##### path

`string`

Path of the file to remove (e.g., "/file.txt").

#### Returns

`Directory`

***

### withoutFiles()

> **withoutFiles**(`paths`): `Directory`

Return a snapshot with files removed

#### Parameters

##### paths

`string`[]

Paths of the files to remove (e.g., ["/file.txt"]).

#### Returns

`Directory`

***

### withPatch()

> **withPatch**(`patch`): `Directory`

**`Experimental`**

Retrieves this directory with the given Git-compatible patch applied.

#### Parameters

##### patch

`string`

Patch to apply (e.g., "diff --git a/file.txt b/file.txt\nindex 1234567..abcdef8 100644\n--- a/file.txt\n+++ b/file.txt\n@@ -1,1 +1,1 @@\n-Hello\n+World\n").

#### Returns

`Directory`

***

### withPatchFile()

> **withPatchFile**(`patch`): `Directory`

**`Experimental`**

Retrieves this directory with the given Git-compatible patch file applied.

#### Parameters

##### patch

[`File`](File.md)

File containing the patch to apply

#### Returns

`Directory`

***

### withSymlink()

> **withSymlink**(`target`, `linkName`): `Directory`

Return a snapshot with a symlink

#### Parameters

##### target

`string`

Location of the file or directory to link to (e.g., "/existing/file").

##### linkName

`string`

Location where the symbolic link will be created (e.g., "/new-file-link").

#### Returns

`Directory`

***

### withTimestamps()

> **withTimestamps**(`timestamp`): `Directory`

Retrieves this directory with all file/dir timestamps set to the given time.

#### Parameters

##### timestamp

`number`

Timestamp to set dir/files in.

Formatted in seconds following Unix epoch (e.g., 1672531199).

#### Returns

`Directory`
