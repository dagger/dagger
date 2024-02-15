---
id: "api_client_gen.GitModuleSource"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).GitModuleSource

Module source originating from a git repo.

## Hierarchy

- `BaseClient`

  ↳ **`GitModuleSource`**

## Constructors

### constructor

**new GitModuleSource**(`parent?`, `_id?`, `_cloneURL?`, `_commit?`, `_htmlURL?`, `_sourceSubpath?`, `_version?`): [`GitModuleSource`](api_client_gen.GitModuleSource.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`GitModuleSourceID`](../modules/api_client_gen.md#gitmodulesourceid) |
| `_cloneURL?` | `string` |
| `_commit?` | `string` |
| `_htmlURL?` | `string` |
| `_sourceSubpath?` | `string` |
| `_version?` | `string` |

#### Returns

[`GitModuleSource`](api_client_gen.GitModuleSource.md)

#### Overrides

BaseClient.constructor

## Properties

### \_cloneURL

 `Private` `Optional` `Readonly` **\_cloneURL**: `string` = `undefined`

___

### \_commit

 `Private` `Optional` `Readonly` **\_commit**: `string` = `undefined`

___

### \_htmlURL

 `Private` `Optional` `Readonly` **\_htmlURL**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`GitModuleSourceID`](../modules/api_client_gen.md#gitmodulesourceid) = `undefined`

___

### \_sourceSubpath

 `Private` `Optional` `Readonly` **\_sourceSubpath**: `string` = `undefined`

___

### \_version

 `Private` `Optional` `Readonly` **\_version**: `string` = `undefined`

## Methods

### cloneURL

**cloneURL**(): `Promise`\<`string`\>

The URL from which the source's git repo can be cloned.

#### Returns

`Promise`\<`string`\>

___

### commit

**commit**(): `Promise`\<`string`\>

The resolved commit of the git repo this source points to.

#### Returns

`Promise`\<`string`\>

___

### htmlURL

**htmlURL**(): `Promise`\<`string`\>

The URL to the source's git repo in a web browser

#### Returns

`Promise`\<`string`\>

___

### id

**id**(): `Promise`\<[`GitModuleSourceID`](../modules/api_client_gen.md#gitmodulesourceid)\>

A unique identifier for this GitModuleSource.

#### Returns

`Promise`\<[`GitModuleSourceID`](../modules/api_client_gen.md#gitmodulesourceid)\>

___

### sourceSubpath

**sourceSubpath**(): `Promise`\<`string`\>

The path to the module source code dir specified by this source relative to the source's root directory.

#### Returns

`Promise`\<`string`\>

___

### version

**version**(): `Promise`\<`string`\>

The specified version of the git repo this source points to.

#### Returns

`Promise`\<`string`\>
