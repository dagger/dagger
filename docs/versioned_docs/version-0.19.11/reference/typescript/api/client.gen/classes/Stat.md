[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Stat

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

[`StatID`](../type-aliases/StatID.md)

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

> **id**(): `Promise`\<[`StatID`](../type-aliases/StatID.md)\>

A unique identifier for this Stat.

#### Returns

`Promise`\<[`StatID`](../type-aliases/StatID.md)\>

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
