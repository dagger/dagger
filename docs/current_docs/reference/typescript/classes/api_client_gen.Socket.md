---
id: "api_client_gen.Socket"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Socket

A Unix or TCP/IP socket that can be mounted into a container.

## Hierarchy

- `BaseClient`

  ↳ **`Socket`**

## Constructors

### constructor

**new Socket**(`parent?`, `_id?`): [`Socket`](api_client_gen.Socket.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`SocketID`](../modules/api_client_gen.md#socketid) |

#### Returns

[`Socket`](api_client_gen.Socket.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`SocketID`](../modules/api_client_gen.md#socketid) = `undefined`

## Methods

### id

**id**(): `Promise`\<[`SocketID`](../modules/api_client_gen.md#socketid)\>

A unique identifier for this Socket.

#### Returns

`Promise`\<[`SocketID`](../modules/api_client_gen.md#socketid)\>
