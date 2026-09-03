---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: RemoteGitMirror

An internal persistent bare git mirror.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new RemoteGitMirror**(`ctx?`, `_id?`): `RemoteGitMirror`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

#### Returns

`RemoteGitMirror`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this RemoteGitMirror.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>
