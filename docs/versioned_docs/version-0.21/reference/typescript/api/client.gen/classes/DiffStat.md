---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: DiffStat

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new DiffStat**(`ctx?`, `_id?`, `_addedLines?`, `_kind?`, `_oldPath?`, `_path?`, `_removedLines?`): `DiffStat`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_addedLines?

`number`

##### \_kind?

[`DiffStatKind`](../enumerations/DiffStatKind.md)

##### \_oldPath?

`string`

##### \_path?

`string`

##### \_removedLines?

`number`

#### Returns

`DiffStat`

#### Overrides

`BaseClient.constructor`

## Methods

### addedLines()

> **addedLines**(): `Promise`\<`number`\>

Number of added lines for this path.

#### Returns

`Promise`\<`number`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this DiffStat.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### kind()

> **kind**(): `Promise`\<[`DiffStatKind`](../enumerations/DiffStatKind.md)\>

Type of change.

#### Returns

`Promise`\<[`DiffStatKind`](../enumerations/DiffStatKind.md)\>

***

### oldPath()

> **oldPath**(): `Promise`\<`string`\>

Previous path of the file, set only for renames.

#### Returns

`Promise`\<`string`\>

***

### path()

> **path**(): `Promise`\<`string`\>

Path of the changed file or directory.

#### Returns

`Promise`\<`string`\>

***

### removedLines()

> **removedLines**(): `Promise`\<`number`\>

Number of removed lines for this path.

#### Returns

`Promise`\<`number`\>
