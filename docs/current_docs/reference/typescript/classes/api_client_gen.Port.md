---
id: "api_client_gen.Port"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Port

A port exposed by a container.

## Hierarchy

- `BaseClient`

  ↳ **`Port`**

## Constructors

### constructor

**new Port**(`parent?`, `_id?`, `_description?`, `_experimentalSkipHealthcheck?`, `_port?`, `_protocol?`): [`Port`](api_client_gen.Port.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`PortID`](../modules/api_client_gen.md#portid) |
| `_description?` | `string` |
| `_experimentalSkipHealthcheck?` | `boolean` |
| `_port?` | `number` |
| `_protocol?` | [`NetworkProtocol`](../enums/api_client_gen.NetworkProtocol.md) |

#### Returns

[`Port`](api_client_gen.Port.md)

#### Overrides

BaseClient.constructor

## Properties

### \_description

 `Private` `Optional` `Readonly` **\_description**: `string` = `undefined`

___

### \_experimentalSkipHealthcheck

 `Private` `Optional` `Readonly` **\_experimentalSkipHealthcheck**: `boolean` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`PortID`](../modules/api_client_gen.md#portid) = `undefined`

___

### \_port

 `Private` `Optional` `Readonly` **\_port**: `number` = `undefined`

___

### \_protocol

 `Private` `Optional` `Readonly` **\_protocol**: [`NetworkProtocol`](../enums/api_client_gen.NetworkProtocol.md) = `undefined`

## Methods

### description

**description**(): `Promise`\<`string`\>

The port description.

#### Returns

`Promise`\<`string`\>

___

### experimentalSkipHealthcheck

**experimentalSkipHealthcheck**(): `Promise`\<`boolean`\>

Skip the health check when run as a service.

#### Returns

`Promise`\<`boolean`\>

___

### id

**id**(): `Promise`\<[`PortID`](../modules/api_client_gen.md#portid)\>

A unique identifier for this Port.

#### Returns

`Promise`\<[`PortID`](../modules/api_client_gen.md#portid)\>

___

### port

**port**(): `Promise`\<`number`\>

The port number.

#### Returns

`Promise`\<`number`\>

___

### protocol

**protocol**(): `Promise`\<[`NetworkProtocol`](../enums/api_client_gen.NetworkProtocol.md)\>

The transport layer protocol.

#### Returns

`Promise`\<[`NetworkProtocol`](../enums/api_client_gen.NetworkProtocol.md)\>
