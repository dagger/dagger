---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Function: connect()

> **connect**(`cb`, `config?`): `Promise`\<`void`\>

connect runs GraphQL server and initializes a
GraphQL client to execute query on it through its callback.
This implementation is based on the existing Go SDK.

## Parameters

### cb

[`CallbackFct`](../type-aliases/CallbackFct.md)

### config?

`ConnectOpts` = `{}`

## Returns

`Promise`\<`void`\>
