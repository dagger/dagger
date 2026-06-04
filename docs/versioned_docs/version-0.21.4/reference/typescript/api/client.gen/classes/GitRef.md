---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

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

[`ID`](../type-aliases/ID.md)

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

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this GitRef.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

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
