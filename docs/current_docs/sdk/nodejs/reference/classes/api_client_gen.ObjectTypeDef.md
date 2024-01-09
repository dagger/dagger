---
id: "api_client_gen.ObjectTypeDef"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).ObjectTypeDef

A definition of a custom object defined in a Module.

## Hierarchy

- `BaseClient`

  ↳ **`ObjectTypeDef`**

## Constructors

### constructor

**new ObjectTypeDef**(`parent?`, `_description?`, `_name?`, `_sourceModuleName?`): [`ObjectTypeDef`](api_client_gen.ObjectTypeDef.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_description?` | `string` |
| `_name?` | `string` |
| `_sourceModuleName?` | `string` |

#### Returns

[`ObjectTypeDef`](api_client_gen.ObjectTypeDef.md)

#### Overrides

BaseClient.constructor

## Properties

### \_description

 `Private` `Optional` `Readonly` **\_description**: `string` = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

___

### \_sourceModuleName

 `Private` `Optional` `Readonly` **\_sourceModuleName**: `string` = `undefined`

## Methods

### constructor\_

**constructor_**(): [`Function_`](api_client_gen.Function_.md)

The function used to construct new instances of this object, if any

#### Returns

[`Function_`](api_client_gen.Function_.md)

___

### description

**description**(): `Promise`\<`string`\>

The doc string for the object, if any

#### Returns

`Promise`\<`string`\>

___

### fields

**fields**(): `Promise`\<[`FieldTypeDef`](api_client_gen.FieldTypeDef.md)[]\>

Static fields defined on this object, if any

#### Returns

`Promise`\<[`FieldTypeDef`](api_client_gen.FieldTypeDef.md)[]\>

___

### functions

**functions**(): `Promise`\<[`Function_`](api_client_gen.Function_.md)[]\>

Functions defined on this object, if any

#### Returns

`Promise`\<[`Function_`](api_client_gen.Function_.md)[]\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the object

#### Returns

`Promise`\<`string`\>

___

### sourceModuleName

**sourceModuleName**(): `Promise`\<`string`\>

If this ObjectTypeDef is associated with a Module, the name of the module. Unset otherwise.

#### Returns

`Promise`\<`string`\>
