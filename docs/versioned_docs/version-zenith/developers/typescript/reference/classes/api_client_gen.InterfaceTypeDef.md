---
id: "api_client_gen.InterfaceTypeDef"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[api/client.gen](../modules/api_client_gen.md).InterfaceTypeDef

A definition of a custom interface defined in a Module.

## Hierarchy

- `BaseClient`

  ↳ **`InterfaceTypeDef`**

## Constructors

### constructor

**new InterfaceTypeDef**(`parent?`, `_id?`, `_description?`, `_name?`, `_sourceModuleName?`): [`InterfaceTypeDef`](api_client_gen.InterfaceTypeDef.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`InterfaceTypeDefID`](../modules/api_client_gen.md#interfacetypedefid) |
| `_description?` | `string` |
| `_name?` | `string` |
| `_sourceModuleName?` | `string` |

#### Returns

[`InterfaceTypeDef`](api_client_gen.InterfaceTypeDef.md)

#### Overrides

BaseClient.constructor

## Properties

### \_description

 `Private` `Optional` `Readonly` **\_description**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`InterfaceTypeDefID`](../modules/api_client_gen.md#interfacetypedefid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_sourceModuleName

 `Private` `Optional` `Readonly` **\_sourceModuleName**: `string` = `undefined`

## Methods

### description

**description**(): `Promise`\<`string`\>

The doc string for the interface, if any.

#### Returns

`Promise`\<`string`\>

___

### functions

**functions**(): `Promise`\<[`Function_`](api_client_gen.Function_.md)[]\>

Functions defined on this interface, if any.

#### Returns

`Promise`\<[`Function_`](api_client_gen.Function_.md)[]\>

___

### id

**id**(): `Promise`\<[`InterfaceTypeDefID`](../modules/api_client_gen.md#interfacetypedefid)\>

A unique identifier for this InterfaceTypeDef.

#### Returns

`Promise`\<[`InterfaceTypeDefID`](../modules/api_client_gen.md#interfacetypedefid)\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the interface.

#### Returns

`Promise`\<`string`\>

___

### sourceModuleName

**sourceModuleName**(): `Promise`\<`string`\>

If this InterfaceTypeDef is associated with a Module, the name of the module. Unset otherwise.

#### Returns

`Promise`\<`string`\>
