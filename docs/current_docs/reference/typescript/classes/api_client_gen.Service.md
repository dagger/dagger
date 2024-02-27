---
id: "api_client_gen.Service"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Service

A content-addressed service providing TCP connectivity.

## Hierarchy

- `BaseClient`

  ↳ **`Service`**

## Constructors

### constructor

**new Service**(`parent?`, `_id?`, `_endpoint?`, `_hostname?`, `_start?`, `_stop?`, `_up?`): [`Service`](api_client_gen.Service.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`ServiceID`](../modules/api_client_gen.md#serviceid) |
| `_endpoint?` | `string` |
| `_hostname?` | `string` |
| `_start?` | [`ServiceID`](../modules/api_client_gen.md#serviceid) |
| `_stop?` | [`ServiceID`](../modules/api_client_gen.md#serviceid) |
| `_up?` | [`Void`](../modules/api_client_gen.md#void) |

#### Returns

[`Service`](api_client_gen.Service.md)

#### Overrides

BaseClient.constructor

## Properties

### \_endpoint

 `Private` `Optional` `Readonly` **\_endpoint**: `string` = `undefined`

___

### \_hostname

 `Private` `Optional` `Readonly` **\_hostname**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`ServiceID`](../modules/api_client_gen.md#serviceid) = `undefined`

___

### \_start

 `Private` `Optional` `Readonly` **\_start**: [`ServiceID`](../modules/api_client_gen.md#serviceid) = `undefined`

___

### \_stop

 `Private` `Optional` `Readonly` **\_stop**: [`ServiceID`](../modules/api_client_gen.md#serviceid) = `undefined`

___

### \_up

 `Private` `Optional` `Readonly` **\_up**: [`Void`](../modules/api_client_gen.md#void) = `undefined`

## Methods

### endpoint

**endpoint**(`opts?`): `Promise`\<`string`\>

Retrieves an endpoint that clients can use to reach this container.

If no port is specified, the first exposed port is used. If none exist an error is returned.

If a scheme is specified, a URL is returned. Otherwise, a host:port pair is returned.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ServiceEndpointOpts`](../modules/api_client_gen.md#serviceendpointopts) |

#### Returns

`Promise`\<`string`\>

___

### hostname

**hostname**(): `Promise`\<`string`\>

Retrieves a hostname which can be used by clients to reach this container.

#### Returns

`Promise`\<`string`\>

___

### id

**id**(): `Promise`\<[`ServiceID`](../modules/api_client_gen.md#serviceid)\>

A unique identifier for this Service.

#### Returns

`Promise`\<[`ServiceID`](../modules/api_client_gen.md#serviceid)\>

___

### ports

**ports**(): `Promise`\<[`Port`](api_client_gen.Port.md)[]\>

Retrieves the list of ports provided by the service.

#### Returns

`Promise`\<[`Port`](api_client_gen.Port.md)[]\>

___

### start

**start**(): `Promise`\<[`Service`](api_client_gen.Service.md)\>

Start the service and wait for its health checks to succeed.

Services bound to a Container do not need to be manually started.

#### Returns

`Promise`\<[`Service`](api_client_gen.Service.md)\>

___

### stop

**stop**(`opts?`): `Promise`\<[`Service`](api_client_gen.Service.md)\>

Stop the service.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ServiceStopOpts`](../modules/api_client_gen.md#servicestopopts) |

#### Returns

`Promise`\<[`Service`](api_client_gen.Service.md)\>

___

### up

**up**(`opts?`): `Promise`\<[`Void`](../modules/api_client_gen.md#void)\>

Creates a tunnel that forwards traffic from the caller's network to this service.

#### Parameters

| Name | Type |
| :------ | :------ |
| `opts?` | [`ServiceUpOpts`](../modules/api_client_gen.md#serviceupopts) |

#### Returns

`Promise`\<[`Void`](../modules/api_client_gen.md#void)\>
