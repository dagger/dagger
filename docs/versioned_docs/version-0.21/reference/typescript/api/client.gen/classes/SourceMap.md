---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: SourceMap

Source location information.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new SourceMap**(`ctx?`, `_id?`, `_column?`, `_filename?`, `_line?`, `_module?`, `_url?`): `SourceMap`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_column?

`number`

##### \_filename?

`string`

##### \_line?

`number`

##### \_module?

`string`

##### \_url?

`string`

#### Returns

`SourceMap`

#### Overrides

`BaseClient.constructor`

## Methods

### column()

> **column**(): `Promise`\<`number`\>

The column number within the line.

#### Returns

`Promise`\<`number`\>

***

### filename()

> **filename**(): `Promise`\<`string`\>

The filename from the module source.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this SourceMap.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### line()

> **line**(): `Promise`\<`number`\>

The line number within the filename.

#### Returns

`Promise`\<`number`\>

***

### module\_()

> **module\_**(): `Promise`\<`string`\>

The module dependency this was declared in.

#### Returns

`Promise`\<`string`\>

***

### url()

> **url**(): `Promise`\<`string`\>

The URL to the file, if any. This can be used to link to the source map in the browser.

#### Returns

`Promise`\<`string`\>
