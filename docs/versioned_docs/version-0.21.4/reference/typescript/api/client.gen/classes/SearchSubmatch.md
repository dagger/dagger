---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: SearchSubmatch

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new SearchSubmatch**(`ctx?`, `_id?`, `_end?`, `_start?`, `_text?`): `SearchSubmatch`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_end?

`number`

##### \_start?

`number`

##### \_text?

`string`

#### Returns

`SearchSubmatch`

#### Overrides

`BaseClient.constructor`

## Methods

### end()

> **end**(): `Promise`\<`number`\>

The match's end offset within the matched lines.

#### Returns

`Promise`\<`number`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this SearchSubmatch.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### start()

> **start**(): `Promise`\<`number`\>

The match's start offset within the matched lines.

#### Returns

`Promise`\<`number`\>

***

### text()

> **text**(): `Promise`\<`string`\>

The matched text.

#### Returns

`Promise`\<`string`\>
