---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Terminal

An interactive terminal that clients can connect to.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Terminal**(`ctx?`, `_id?`, `_sync?`): `Terminal`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_sync?

[`ID`](../type-aliases/ID.md)

#### Returns

`Terminal`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Terminal.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### sync()

> **sync**(): `Promise`\<`Terminal`\>

Forces evaluation of the pipeline in the engine.

It doesn't run the default command if no exec has been set.

#### Returns

`Promise`\<`Terminal`\>
