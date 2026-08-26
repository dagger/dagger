[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Changeset

# Class: Changeset

A comparison between two directories representing changes that can be applied.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Changeset**(`ctx?`, `_id?`, `_export?`, `_isEmpty?`, `_sync?`): `Changeset`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ChangesetID`](../type-aliases/ChangesetID.md)

##### \_export?

`string`

##### \_isEmpty?

`boolean`

##### \_sync?

[`ChangesetID`](../type-aliases/ChangesetID.md)

#### Returns

`Changeset`

#### Overrides

`BaseClient.constructor`

## Methods

### addedPaths()

> **addedPaths**(): `Promise`\<`string`[]\>

Files and directories that were added in the newer directory.

#### Returns

`Promise`\<`string`[]\>

***

### after()

> **after**(): [`Directory`](Directory.md)

The newer/upper snapshot.

#### Returns

[`Directory`](Directory.md)

***

### asPatch()

> **asPatch**(): [`File`](File.md)

Return a Git-compatible patch of the changes

#### Returns

[`File`](File.md)

***

### before()

> **before**(): [`Directory`](Directory.md)

The older/lower snapshot to compare against.

#### Returns

[`Directory`](Directory.md)

***

### export()

> **export**(`path`): `Promise`\<`string`\>

Applies the diff represented by this changeset to a path on the host.

#### Parameters

##### path

`string`

Location of the copied directory (e.g., "logs/").

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ChangesetID`](../type-aliases/ChangesetID.md)\>

A unique identifier for this Changeset.

#### Returns

`Promise`\<[`ChangesetID`](../type-aliases/ChangesetID.md)\>

***

### isEmpty()

> **isEmpty**(): `Promise`\<`boolean`\>

Returns true if the changeset is empty (i.e. there are no changes).

#### Returns

`Promise`\<`boolean`\>

***

### layer()

> **layer**(): [`Directory`](Directory.md)

Return a snapshot containing only the created and modified files

#### Returns

[`Directory`](Directory.md)

***

### modifiedPaths()

> **modifiedPaths**(): `Promise`\<`string`[]\>

Files and directories that existed before and were updated in the newer directory.

#### Returns

`Promise`\<`string`[]\>

***

### removedPaths()

> **removedPaths**(): `Promise`\<`string`[]\>

Files and directories that were removed. Directories are indicated by a trailing slash, and their child paths are not included.

#### Returns

`Promise`\<`string`[]\>

***

### sync()

> **sync**(): `Promise`\<`Changeset`\>

Force evaluation in the engine.

#### Returns

`Promise`\<`Changeset`\>

***

### with()

> **with**(`arg`): `Changeset`

Call the provided function with current Changeset.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Changeset`

#### Returns

`Changeset`

***

### withChangeset()

> **withChangeset**(`changes`, `opts?`): `Changeset`

Add changes to an existing changeset

By default the operation will fail in case of conflicts, for instance a file modified in both changesets. The behavior can be adjusted using onConflict argument

#### Parameters

##### changes

`Changeset`

Changes to merge into the actual changeset

##### opts?

[`ChangesetWithChangesetOpts`](../type-aliases/ChangesetWithChangesetOpts.md)

#### Returns

`Changeset`

***

### withChangesets()

> **withChangesets**(`changes`, `opts?`): `Changeset`

Add changes from multiple changesets using git octopus merge strategy

This is more efficient than chaining multiple withChangeset calls when merging many changesets.

Only FAIL and FAIL_EARLY conflict strategies are supported (octopus merge cannot use -X ours/theirs).

#### Parameters

##### changes

`Changeset`[]

List of changesets to merge into the actual changeset

##### opts?

[`ChangesetWithChangesetsOpts`](../type-aliases/ChangesetWithChangesetsOpts.md)

#### Returns

`Changeset`
