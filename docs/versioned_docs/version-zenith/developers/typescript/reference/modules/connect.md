---
id: "connect"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
sidebar_position: 0
custom_edit_url: null
displayed_sidebar: "zenith"
---

## Type Aliases

### CallbackFct

 **CallbackFct**: (`client`: [`Client`](../classes/api_client_gen.Client.md)) => `Promise`\<`void`\>

#### Type declaration

(`client`): `Promise`\<`void`\>

##### Parameters

| Name | Type |
| :------ | :------ |
| `client` | [`Client`](../classes/api_client_gen.Client.md) |

##### Returns

`Promise`\<`void`\>

## Functions

### close

**close**(): `void`

Close global client connection

#### Returns

`void`

___

### connect

**connect**(`cb`, `config?`): `Promise`\<`void`\>

connect runs GraphQL server and initializes a
GraphQL client to execute query on it through its callback.
This implementation is based on the existing Go SDK.

#### Parameters

| Name | Type |
| :------ | :------ |
| `cb` | [`CallbackFct`](connect.md#callbackfct) |
| `config` | `ConnectOpts` |

#### Returns

`Promise`\<`void`\>

___

### connection

**connection**(`fct`, `cfg?`): `Promise`\<`void`\>

connection executes the given function using the default global Dagger client.

#### Parameters

| Name | Type |
| :------ | :------ |
| `fct` | () => `Promise`\<`void`\> |
| `cfg` | `ConnectOpts` |

#### Returns

`Promise`\<`void`\>

**`Example`**

```ts
await connection(
  async () => {
    await dag
      .container()
      .from("alpine")
      .withExec(["apk", "add", "curl"])
      .withExec(["curl", "https://dagger.io/"])
      .sync()
  }, { LogOutput: process.stderr }
)
```
