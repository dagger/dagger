[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Port

# Class: Port

A port exposed by a container.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Port**(`ctx?`, `_id?`, `_description?`, `_experimentalSkipHealthcheck?`, `_port?`, `_protocol?`): `Port`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`PortID`](../type-aliases/PortID.md)

##### \_description?

`string`

##### \_experimentalSkipHealthcheck?

`boolean`

##### \_port?

`number`

##### \_protocol?

[`NetworkProtocol`](../enumerations/NetworkProtocol.md)

#### Returns

`Port`

#### Overrides

`BaseClient.constructor`

## Methods

### description()

> **description**(): `Promise`\<`string`\>

The port description.

#### Returns

`Promise`\<`string`\>

***

### experimentalSkipHealthcheck()

> **experimentalSkipHealthcheck**(): `Promise`\<`boolean`\>

Skip the health check when run as a service.

#### Returns

`Promise`\<`boolean`\>

***

### id()

> **id**(): `Promise`\<[`PortID`](../type-aliases/PortID.md)\>

A unique identifier for this Port.

#### Returns

`Promise`\<[`PortID`](../type-aliases/PortID.md)\>

***

### port()

> **port**(): `Promise`\<`number`\>

The port number.

#### Returns

`Promise`\<`number`\>

***

### protocol()

> **protocol**(): `Promise`\<[`NetworkProtocol`](../enumerations/NetworkProtocol.md)\>

The transport layer protocol.

#### Returns

`Promise`\<[`NetworkProtocol`](../enumerations/NetworkProtocol.md)\>
