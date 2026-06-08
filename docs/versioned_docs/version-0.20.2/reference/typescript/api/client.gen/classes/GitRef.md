[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / GitRef

# Class: GitRef

A git ref (tag, branch, or commit).

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new GitRef**(`ctx?`, `_id?`, `_commit?`, `_ref?`): `GitRef`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`GitRefID`](../type-aliases/GitRefID.md)

##### \_commit?

`string`

##### \_ref?

`string`

#### Returns

`GitRef`

#### Overrides

`BaseClient.constructor`

## Methods

### commit()

> **commit**(): `Promise`\<`string`\>

The resolved commit id at this ref.

#### Returns

`Promise`\<`string`\>

***

### commonAncestor()

> **commonAncestor**(`other`): `GitRef`

Find the best common ancestor between this ref and another ref.

#### Parameters

##### other

`GitRef`

The other ref to compare against.

#### Returns

`GitRef`

***

### id()

> **id**(): `Promise`\<[`GitRefID`](../type-aliases/GitRefID.md)\>

A unique identifier for this GitRef.

#### Returns

`Promise`\<[`GitRefID`](../type-aliases/GitRefID.md)\>

***

### ref()

> **ref**(): `Promise`\<`string`\>

The resolved ref name at this ref.

#### Returns

`Promise`\<`string`\>

***

### tree()

> **tree**(`opts?`): [`Directory`](Directory.md)

The filesystem tree at this ref.

#### Parameters

##### opts?

[`GitRefTreeOpts`](../type-aliases/GitRefTreeOpts.md)

#### Returns

[`Directory`](Directory.md)

***

### with()

> **with**(`arg`): `GitRef`

Call the provided function with current GitRef.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `GitRef`

#### Returns

`GitRef`
