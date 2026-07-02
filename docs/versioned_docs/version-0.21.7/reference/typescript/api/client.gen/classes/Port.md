---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

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

[`ID`](../type-aliases/ID.md)

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

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Port.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

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
