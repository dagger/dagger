[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / SearchResult

# Class: SearchResult

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new SearchResult**(`ctx?`, `_id?`, `_absoluteOffset?`, `_filePath?`, `_lineNumber?`, `_matchedLines?`): `SearchResult`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`SearchResultID`](../type-aliases/SearchResultID.md)

##### \_absoluteOffset?

`number`

##### \_filePath?

`string`

##### \_lineNumber?

`number`

##### \_matchedLines?

`string`

#### Returns

`SearchResult`

#### Overrides

`BaseClient.constructor`

## Methods

### absoluteOffset()

> **absoluteOffset**(): `Promise`\<`number`\>

The byte offset of this line within the file.

#### Returns

`Promise`\<`number`\>

***

### filePath()

> **filePath**(): `Promise`\<`string`\>

The path to the file that matched.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`SearchResultID`](../type-aliases/SearchResultID.md)\>

A unique identifier for this SearchResult.

#### Returns

`Promise`\<[`SearchResultID`](../type-aliases/SearchResultID.md)\>

***

### lineNumber()

> **lineNumber**(): `Promise`\<`number`\>

The first line that matched.

#### Returns

`Promise`\<`number`\>

***

### matchedLines()

> **matchedLines**(): `Promise`\<`string`\>

The line content that matched.

#### Returns

`Promise`\<`string`\>

***

### submatches()

> **submatches**(): `Promise`\<[`SearchSubmatch`](SearchSubmatch.md)[]\>

Sub-match positions and content within the matched lines.

#### Returns

`Promise`\<[`SearchSubmatch`](SearchSubmatch.md)[]\>
