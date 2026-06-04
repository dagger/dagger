[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / LLMTokenUsage

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

[`LLMTokenUsageID`](../type-aliases/LLMTokenUsageID.md)

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

> **id**(): `Promise`\<[`LLMTokenUsageID`](../type-aliases/LLMTokenUsageID.md)\>

A unique identifier for this LLMTokenUsage.

#### Returns

`Promise`\<[`LLMTokenUsageID`](../type-aliases/LLMTokenUsageID.md)\>

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
