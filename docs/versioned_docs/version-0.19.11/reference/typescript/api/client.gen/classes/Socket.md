[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Socket

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

[`SocketID`](../type-aliases/SocketID.md)

#### Returns

`Socket`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`SocketID`](../type-aliases/SocketID.md)\>

A unique identifier for this Socket.

#### Returns

`Promise`\<[`SocketID`](../type-aliases/SocketID.md)\>
