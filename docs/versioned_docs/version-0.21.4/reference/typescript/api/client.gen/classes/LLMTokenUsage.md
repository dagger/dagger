---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: LLMTokenUsage

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new LLMTokenUsage**(`ctx?`, `_id?`, `_cachedTokenReads?`, `_cachedTokenWrites?`, `_inputTokens?`, `_outputTokens?`, `_totalTokens?`): `LLMTokenUsage`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_cachedTokenReads?

`number`

##### \_cachedTokenWrites?

`number`

##### \_inputTokens?

`number`

##### \_outputTokens?

`number`

##### \_totalTokens?

`number`

#### Returns

`LLMTokenUsage`

#### Overrides

`BaseClient.constructor`

## Methods

### cachedTokenReads()

> **cachedTokenReads**(): `Promise`\<`number`\>

#### Returns

`Promise`\<`number`\>

***

### cachedTokenWrites()

> **cachedTokenWrites**(): `Promise`\<`number`\>

#### Returns

`Promise`\<`number`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this LLMTokenUsage.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### inputTokens()

> **inputTokens**(): `Promise`\<`number`\>

#### Returns

`Promise`\<`number`\>

***

### outputTokens()

> **outputTokens**(): `Promise`\<`number`\>

#### Returns

`Promise`\<`number`\>

***

### totalTokens()

> **totalTokens**(): `Promise`\<`number`\>

#### Returns

`Promise`\<`number`\>
