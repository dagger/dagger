---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: ModuleConfigClient

The client generated for the module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new ModuleConfigClient**(`ctx?`, `_id?`, `_directory?`, `_generator?`): `ModuleConfigClient`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_directory?

`string`

##### \_generator?

`string`

#### Returns

`ModuleConfigClient`

#### Overrides

`BaseClient.constructor`

## Methods

### directory()

> **directory**(): `Promise`\<`string`\>

The directory the client is generated in.

#### Returns

`Promise`\<`string`\>

***

### generator()

> **generator**(): `Promise`\<`string`\>

The generator to use

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this ModuleConfigClient.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
