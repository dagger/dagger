---
id: "api_client_gen.Function_"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[api/client.gen](../modules/api_client_gen.md).Function_

Function represents a resolver provided by a Module.

A function always evaluates against a parent object and is given a set of named arguments.

## Hierarchy

- `BaseClient`

  ↳ **`Function_`**

## Constructors

### constructor

**new Function_**(`parent?`, `_id?`, `_description?`, `_name?`): [`Function_`](api_client_gen.Function_.md)

Constructor is used for internal usage only, do not create object from it.

#### Parameters

| Name | Type |
| :------ | :------ |
| `parent?` | `Object` |
| `parent.ctx` | `Context` |
| `parent.queryTree?` | `QueryTree`[] |
| `_id?` | [`FunctionID`](../modules/api_client_gen.md#functionid) |
| `_description?` | `string` |
| `_name?` | `string` |

#### Returns

[`Function_`](api_client_gen.Function_.md)

#### Overrides

BaseClient.constructor

## Properties

### \_description

 `Private` `Optional` `Readonly` **\_description**: `string` = `undefined`

___

### \_id

 `Private` `Optional` `Readonly` **\_id**: [`FunctionID`](../modules/api_client_gen.md#functionid) = `undefined`

___

### \_name

 `Private` `Optional` `Readonly` **\_name**: `string` = `undefined`

## Methods

### args

**args**(): `Promise`\<[`FunctionArg`](api_client_gen.FunctionArg.md)[]\>

Arguments accepted by the function, if any.

#### Returns

`Promise`\<[`FunctionArg`](api_client_gen.FunctionArg.md)[]\>

___

### description

**description**(): `Promise`\<`string`\>

A doc string for the function, if any.

#### Returns

`Promise`\<`string`\>

___

### id

**id**(): `Promise`\<[`FunctionID`](../modules/api_client_gen.md#functionid)\>

A unique identifier for this Function.

#### Returns

`Promise`\<[`FunctionID`](../modules/api_client_gen.md#functionid)\>

___

### name

**name**(): `Promise`\<`string`\>

The name of the function.

#### Returns

`Promise`\<`string`\>

___

### returnType

**returnType**(): [`TypeDef`](api_client_gen.TypeDef.md)

The type returned by the function.

#### Returns

[`TypeDef`](api_client_gen.TypeDef.md)

___

### with

**with**(`arg`): [`Function_`](api_client_gen.Function_.md)

Call the provided function with current Function.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

| Name | Type |
| :------ | :------ |
| `arg` | (`param`: [`Function_`](api_client_gen.Function_.md)) => [`Function_`](api_client_gen.Function_.md) |

#### Returns

[`Function_`](api_client_gen.Function_.md)

___

### withArg

**withArg**(`name`, `typeDef`, `opts?`): [`Function_`](api_client_gen.Function_.md)

Returns the function with the provided argument

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `name` | `string` | The name of the argument |
| `typeDef` | [`TypeDef`](api_client_gen.TypeDef.md) | The type of the argument |
| `opts?` | [`FunctionWithArgOpts`](../modules/api_client_gen.md#functionwithargopts) | - |

#### Returns

[`Function_`](api_client_gen.Function_.md)

___

### withDescription

**withDescription**(`description`): [`Function_`](api_client_gen.Function_.md)

Returns the function with the given doc string.

#### Parameters

| Name | Type | Description |
| :------ | :------ | :------ |
| `description` | `string` | The doc string to set. |

#### Returns

[`Function_`](api_client_gen.Function_.md)
