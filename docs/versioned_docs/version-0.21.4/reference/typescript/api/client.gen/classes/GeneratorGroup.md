---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: GeneratorGroup

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new GeneratorGroup**(`ctx?`, `_id?`, `_isEmpty?`): `GeneratorGroup`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_isEmpty?

`boolean`

#### Returns

`GeneratorGroup`

#### Overrides

`BaseClient.constructor`

## Methods

### changes()

> **changes**(`opts?`): [`Changeset`](Changeset.md)

The combined changes from the last run of the generators

If any conflict occurs, for instance if the same file is modified by multiple generators, or if a file is both modified and deleted, an error is raised and the merge of the changesets will failed.

Set 'continueOnConflicts' flag to force to merge the changes in a 'last write wins' strategy.

#### Parameters

##### opts?

[`GeneratorGroupChangesOpts`](../type-aliases/GeneratorGroupChangesOpts.md)

#### Returns

[`Changeset`](Changeset.md)

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this GeneratorGroup.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### isEmpty()

> **isEmpty**(): `Promise`\<`boolean`\>

Whether the generated changeset from the last run is empty or not

#### Returns

`Promise`\<`boolean`\>

***

### list()

> **list**(): `Promise`\<[`Generator`](Generator.md)[]\>

Return a list of individual generators and their details

#### Returns

`Promise`\<[`Generator`](Generator.md)[]\>

***

### run()

> **run**(): `GeneratorGroup`

Execute all selected generators

#### Returns

`GeneratorGroup`

***

### with()

> **with**(`arg`): `GeneratorGroup`

Call the provided function with current GeneratorGroup.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `GeneratorGroup`

#### Returns

`GeneratorGroup`
