---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: CheckGroup

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new CheckGroup**(`ctx?`, `_id?`): `CheckGroup`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

#### Returns

`CheckGroup`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this CheckGroup.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### list()

> **list**(): `Promise`\<[`Check`](Check.md)[]\>

Return a list of individual checks and their details

#### Returns

`Promise`\<[`Check`](Check.md)[]\>

***

### report()

> **report**(): [`File`](File.md)

Generate a markdown report

#### Returns

[`File`](File.md)

***

### run()

> **run**(`opts?`): `CheckGroup`

Execute all selected checks

#### Parameters

##### opts?

[`CheckGroupRunOpts`](../type-aliases/CheckGroupRunOpts.md)

#### Returns

`CheckGroup`

***

### with()

> **with**(`arg`): `CheckGroup`

Call the provided function with current CheckGroup.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `CheckGroup`

#### Returns

`CheckGroup`
