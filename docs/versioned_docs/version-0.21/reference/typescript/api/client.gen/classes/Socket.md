---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Socket

A Unix or TCP/IP socket that can be mounted into a container.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Socket**(`ctx?`, `_id?`): `Socket`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

#### Returns

`Socket`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Socket.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
