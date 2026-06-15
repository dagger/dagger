[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Terminal

# Class: Terminal

An interactive terminal that clients can connect to.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Terminal**(`ctx?`, `_id?`, `_sync?`): `Terminal`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`TerminalID`](../type-aliases/TerminalID.md)

##### \_sync?

[`TerminalID`](../type-aliases/TerminalID.md)

#### Returns

`Terminal`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`TerminalID`](../type-aliases/TerminalID.md)\>

A unique identifier for this Terminal.

#### Returns

`Promise`\<[`TerminalID`](../type-aliases/TerminalID.md)\>

***

### sync()

> **sync**(): `Promise`\<`Terminal`\>

Forces evaluation of the pipeline in the engine.

It doesn't run the default command if no exec has been set.

#### Returns

`Promise`\<`Terminal`\>
