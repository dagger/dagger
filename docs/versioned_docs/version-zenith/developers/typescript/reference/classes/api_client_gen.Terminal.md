---
id: "api_client_gen.Terminal"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).Terminal

An interactive terminal that clients can connect to.

## Hierarchy

- `BaseClient`

  ↳ **`Terminal`**

## Constructors

### constructor

**new Terminal**(`parent?`, `_id?`, `_websocketEndpoint?`): [`Terminal`](api_client_gen.Terminal.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`TerminalID`](../modules/api_client_gen.md#terminalid) |
| `_websocketEndpoint?` | `string` |

#### Returns

[`Terminal`](api_client_gen.Terminal.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`TerminalID`](../modules/api_client_gen.md#terminalid) = `undefined`

___

### \_websocketEndpoint

 `Private` `Optional` `Readonly` **\_websocketEndpoint**: `string` = `undefined`

## Methods

### id

**id**(): `Promise`\<[`TerminalID`](../modules/api_client_gen.md#terminalid)\>

A unique identifier for this Terminal.

#### Returns

`Promise`\<[`TerminalID`](../modules/api_client_gen.md#terminalid)\>

___

### websocketEndpoint

**websocketEndpoint**(): `Promise`\<`string`\>

An http endpoint at which this terminal can be connected to over a websocket.

#### Returns

`Promise`\<`string`\>
