[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / File

# Class: File

A file.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new File**(`ctx?`, `_id?`, `_contents?`, `_digest?`, `_export?`, `_name?`, `_size?`, `_sync?`): `File`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`FileID`](../type-aliases/FileID.md)

##### \_contents?

`string`

##### \_digest?

`string`

##### \_export?

`string`

##### \_name?

`string`

##### \_size?

`number`

##### \_sync?

[`FileID`](../type-aliases/FileID.md)

#### Returns

`File`

#### Overrides

`BaseClient.constructor`

## Methods

### asEnvFile()

> **asEnvFile**(`opts?`): [`EnvFile`](EnvFile.md)

Parse as an env file

#### Parameters

##### opts?

[`FileAsEnvFileOpts`](../type-aliases/FileAsEnvFileOpts.md)

#### Returns

[`EnvFile`](EnvFile.md)

***

### asJSON()

> **asJSON**(): [`JSONValue`](JSONValue.md)

Parse the file contents as JSON.

#### Returns

[`JSONValue`](JSONValue.md)

***

### chown()

> **chown**(`owner`): `File`

Change the owner of the file recursively.

#### Parameters

##### owner

`string`

A user:group to set for the file.

The user and group must be an ID (1000:1000), not a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

#### Returns

`File`

***

### contents()

> **contents**(`opts?`): `Promise`\<`string`\>

Retrieves the contents of the file.

#### Parameters

##### opts?

[`FileContentsOpts`](../type-aliases/FileContentsOpts.md)

#### Returns

`Promise`\<`string`\>

***

### digest()

> **digest**(`opts?`): `Promise`\<`string`\>

Return the file's digest. The format of the digest is not guaranteed to be stable between releases of Dagger. It is guaranteed to be stable between invocations of the same Dagger engine.

#### Parameters

##### opts?

[`FileDigestOpts`](../type-aliases/FileDigestOpts.md)

#### Returns

`Promise`\<`string`\>

***

### export()

> **export**(`path`, `opts?`): `Promise`\<`string`\>

Writes the file to a file path on the host.

#### Parameters

##### path

`string`

Location of the written directory (e.g., "output.txt").

##### opts?

[`FileExportOpts`](../type-aliases/FileExportOpts.md)

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`FileID`](../type-aliases/FileID.md)\>

A unique identifier for this File.

#### Returns

`Promise`\<[`FileID`](../type-aliases/FileID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

Retrieves the name of the file.

#### Returns

`Promise`\<`string`\>

***

### search()

> **search**(`pattern`, `opts?`): `Promise`\<[`SearchResult`](SearchResult.md)[]\>

Searches for content matching the given regular expression or literal string.

Uses Rust regex syntax; escape literal ., [, ], \{, \}, | with backslashes.

#### Parameters

##### pattern

`string`

The text to match.

##### opts?

[`FileSearchOpts`](../type-aliases/FileSearchOpts.md)

#### Returns

`Promise`\<[`SearchResult`](SearchResult.md)[]\>

***

### size()

> **size**(): `Promise`\<`number`\>

Retrieves the size of the file, in bytes.

#### Returns

`Promise`\<`number`\>

***

### stat()

> **stat**(): [`Stat`](Stat.md)

Return file status

#### Returns

[`Stat`](Stat.md)

***

### sync()

> **sync**(): `Promise`\<`File`\>

Force evaluation in the engine.

#### Returns

`Promise`\<`File`\>

***

### with()

> **with**(`arg`): `File`

Call the provided function with current File.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `File`

#### Returns

`File`

***

### withName()

> **withName**(`name`): `File`

Retrieves this file with its name set to the given name.

#### Parameters

##### name

`string`

Name to set file to.

#### Returns

`File`

***

### withReplaced()

> **withReplaced**(`search`, `replacement`, `opts?`): `File`

Retrieves the file with content replaced with the given text.

If 'all' is true, all occurrences of the pattern will be replaced.

If 'firstAfter' is specified, only the first match starting at the specified line will be replaced.

If neither are specified, and there are multiple matches for the pattern, this will error.

If there are no matches for the pattern, this will error.

#### Parameters

##### search

`string`

The text to match.

##### replacement

`string`

The text to match.

##### opts?

[`FileWithReplacedOpts`](../type-aliases/FileWithReplacedOpts.md)

#### Returns

`File`

***

### withTimestamps()

> **withTimestamps**(`timestamp`): `File`

Retrieves this file with its created/modified timestamps set to the given time.

#### Parameters

##### timestamp

`number`

Timestamp to set dir/files in.

Formatted in seconds following Unix epoch (e.g., 1672531199).

#### Returns

`File`
