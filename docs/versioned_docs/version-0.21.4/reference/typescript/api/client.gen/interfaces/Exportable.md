---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Interface: Exportable

An object that can be exported to the host.

Calling export writes the object to a path on the host filesystem and returns the path that was written.

## Methods

### export()

> **export**(`path`): `Promise`\<`string`\>

#### Parameters

##### path

`string`

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
