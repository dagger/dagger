[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / SDKConfig

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

[`SDKConfigID`](../type-aliases/SDKConfigID.md)

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

> **id**(): `Promise`\<[`SDKConfigID`](../type-aliases/SDKConfigID.md)\>

A unique identifier for this SDKConfig.

#### Returns

`Promise`\<[`SDKConfigID`](../type-aliases/SDKConfigID.md)\>

***

### source()

> **source**(): `Promise`\<`string`\>

Source of the SDK. Either a name of a builtin SDK or a module source ref string pointing to the SDK's implementation.

#### Returns

`Promise`\<`string`\>
