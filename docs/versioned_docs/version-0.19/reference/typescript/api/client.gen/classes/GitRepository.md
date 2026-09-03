[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / GitRepository

# Class: GitRepository

A git repository.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new GitRepository**(`ctx?`, `_id?`, `_url?`): `GitRepository`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`GitRepositoryID`](../type-aliases/GitRepositoryID.md)

##### \_url?

`string`

#### Returns

`GitRepository`

#### Overrides

`BaseClient.constructor`

## Methods

### branch()

> **branch**(`name`): [`GitRef`](GitRef.md)

Returns details of a branch.

#### Parameters

##### name

`string`

Branch's name (e.g., "main").

#### Returns

[`GitRef`](GitRef.md)

***

### branches()

> **branches**(`opts?`): `Promise`\<`string`[]\>

branches that match any of the given glob patterns.

#### Parameters

##### opts?

[`GitRepositoryBranchesOpts`](../type-aliases/GitRepositoryBranchesOpts.md)

#### Returns

`Promise`\<`string`[]\>

***

### commit()

> **commit**(`id`): [`GitRef`](GitRef.md)

Returns details of a commit.

#### Parameters

##### id

`string`

Identifier of the commit (e.g., "b6315d8f2810962c601af73f86831f6866ea798b").

#### Returns

[`GitRef`](GitRef.md)

***

### head()

> **head**(): [`GitRef`](GitRef.md)

Returns details for HEAD.

#### Returns

[`GitRef`](GitRef.md)

***

### id()

> **id**(): `Promise`\<[`GitRepositoryID`](../type-aliases/GitRepositoryID.md)\>

A unique identifier for this GitRepository.

#### Returns

`Promise`\<[`GitRepositoryID`](../type-aliases/GitRepositoryID.md)\>

***

### latestVersion()

> **latestVersion**(): [`GitRef`](GitRef.md)

Returns details for the latest semver tag.

#### Returns

[`GitRef`](GitRef.md)

***

### ref()

> **ref**(`name`): [`GitRef`](GitRef.md)

Returns details of a ref.

#### Parameters

##### name

`string`

Ref's name (can be a commit identifier, a tag name, a branch name, or a fully-qualified ref).

#### Returns

[`GitRef`](GitRef.md)

***

### tag()

> **tag**(`name`): [`GitRef`](GitRef.md)

Returns details of a tag.

#### Parameters

##### name

`string`

Tag's name (e.g., "v0.3.9").

#### Returns

[`GitRef`](GitRef.md)

***

### tags()

> **tags**(`opts?`): `Promise`\<`string`[]\>

tags that match any of the given glob patterns.

#### Parameters

##### opts?

[`GitRepositoryTagsOpts`](../type-aliases/GitRepositoryTagsOpts.md)

#### Returns

`Promise`\<`string`[]\>

***

### uncommitted()

> **uncommitted**(): [`Changeset`](Changeset.md)

Returns the changeset of uncommitted changes in the git repository.

#### Returns

[`Changeset`](Changeset.md)

***

### url()

> **url**(): `Promise`\<`string`\>

The URL of the git repository.

#### Returns

`Promise`\<`string`\>
