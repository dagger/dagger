---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Cloud

Dagger Cloud configuration and state

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Cloud**(`ctx?`, `_id?`, `_traceURL?`): `Cloud`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_traceURL?

`string`

#### Returns

`Cloud`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Cloud.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### traceURL()

> **traceURL**(): `Promise`\<`string`\>

The trace URL for the current session

#### Returns

`Promise`\<`string`\>
