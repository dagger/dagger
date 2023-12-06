---
id: "connect.ConnectOpts"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[connect](../modules/connect.md).ConnectOpts

ConnectOpts defines option used to connect to an engine.

## Properties

### LogOutput

 `Optional` **LogOutput**: `any`

Enable logs output

**`Example`**

LogOutput
```ts
connect(async (client: Client) => {
 const source = await client.host().workdir().id()
 ...
 }, {LogOutput: process.stdout})
 ```

___

### Workdir

 `Optional` **Workdir**: `string`

Use to overwrite Dagger workdir

**`Default Value`**

process.cwd()
