---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Stat

A file or directory status object.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Stat**(`ctx?`, `_id?`, `_fileType?`, `_name?`, `_permissions?`, `_size?`): `Stat`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_fileType?

[`FileType`](../enumerations/FileType.md)

##### \_name?

`string`

##### \_permissions?

`number`

##### \_size?

`number`

#### Returns

`Stat`

#### Overrides

`BaseClient.constructor`

## Methods

### fileType()

> **fileType**(): `Promise`\<[`FileType`](../enumerations/FileType.md)\>

file type

#### Returns

`Promise`\<[`FileType`](../enumerations/FileType.md)\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Stat.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

file name

#### Returns

`Promise`\<`string`\>

***

### permissions()

> **permissions**(): `Promise`\<`number`\>

permission bits

#### Returns

`Promise`\<`number`\>

***

### size()

> **size**(): `Promise`\<`number`\>

file size

#### Returns

`Promise`\<`number`\>
