---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: HTTPState

An internal persistent HTTP state.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new HTTPState**(`ctx?`, `_id?`): `HTTPState`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

#### Returns

`HTTPState`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this HTTPState.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
