---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: SDKConfig

The SDK config of the module.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new SDKConfig**(`ctx?`, `_id?`, `_debug?`, `_source?`): `SDKConfig`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_debug?

`boolean`

##### \_source?

`string`

#### Returns

`SDKConfig`

#### Overrides

`BaseClient.constructor`

## Methods

### debug()

> **debug**(): `Promise`\<`boolean`\>

Whether to start the SDK runtime in debug mode with an interactive terminal.

#### Returns

`Promise`\<`boolean`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this SDKConfig.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### source()

> **source**(): `Promise`\<`string`\>

Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation.

#### Returns

`Promise`\<`string`\>
