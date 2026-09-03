---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Interface: Syncer

An object that can be force-evaluated.

Calling sync ensures that the object's entire dependency DAG has been evaluated, returning the object's ID once complete.

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### sync()

> **sync**(): `Promise`\<`Syncer`\>

#### Returns

`Promise`\<`Syncer`\>
