---
id: "api_client_gen.Socket"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Socket

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

The content-addressed identifier of the socket.

#### Returns

`Promise`\<[`SocketID`](../modules/api_client_gen.md#socketid)\>
