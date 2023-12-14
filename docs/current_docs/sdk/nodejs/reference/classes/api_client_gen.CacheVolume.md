---
id: "api_client_gen.CacheVolume"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).CacheVolume

A directory whose contents persist across runs.

## Hierarchy

- `BaseClient`

  ↳ **`CacheVolume`**

## Constructors

### constructor

**new CacheVolume**(`parent?`, `_id?`): [`CacheVolume`](api_client_gen.CacheVolume.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`CacheVolumeID`](../modules/api_client_gen.md#cachevolumeid) |

#### Returns

[`CacheVolume`](api_client_gen.CacheVolume.md)

#### Overrides

BaseClient.constructor

## Properties

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`CacheVolumeID`](../modules/api_client_gen.md#cachevolumeid) = `undefined`

## Methods

### id

**id**(): `Promise`\<[`CacheVolumeID`](../modules/api_client_gen.md#cachevolumeid)\>

#### Returns

`Promise`\<[`CacheVolumeID`](../modules/api_client_gen.md#cachevolumeid)\>
