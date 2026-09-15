[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / CheckGroup

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

[`CheckGroupID`](../type-aliases/CheckGroupID.md)

#### Returns

`CheckGroup`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`CheckGroupID`](../type-aliases/CheckGroupID.md)\>

A unique identifier for this CheckGroup.

#### Returns

`Promise`\<[`CheckGroupID`](../type-aliases/CheckGroupID.md)\>

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

> **run**(): `CheckGroup`

Execute all selected checks

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
