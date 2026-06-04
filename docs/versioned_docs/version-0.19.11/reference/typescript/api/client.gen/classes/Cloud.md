[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Cloud

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

[`CloudID`](../type-aliases/CloudID.md)

##### \_traceURL?

`string`

#### Returns

`Cloud`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`CloudID`](../type-aliases/CloudID.md)\>

A unique identifier for this Cloud.

#### Returns

`Promise`\<[`CloudID`](../type-aliases/CloudID.md)\>

***

### traceURL()

> **traceURL**(): `Promise`\<`string`\>

The trace URL for the current session

#### Returns

`Promise`\<`string`\>
