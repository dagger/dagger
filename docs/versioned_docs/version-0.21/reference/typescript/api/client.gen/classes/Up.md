---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Up

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Up**(`ctx?`, `_id?`, `_description?`, `_name?`): `Up`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_description?

`string`

##### \_name?

`string`

#### Returns

`Up`

#### Overrides

`BaseClient.constructor`

## Methods

### description()

> **description**(): `Promise`\<`string`\>

The description of the service

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Up.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

Return the fully qualified name of the service

#### Returns

`Promise`\<`string`\>

***

### originalModule()

> **originalModule**(): [`Module_`](Module.md)

The original module in which the service has been defined

#### Returns

[`Module_`](Module.md)

***

### path()

> **path**(): `Promise`\<`string`[]\>

The path of the service within its module

#### Returns

`Promise`\<`string`[]\>

***

### run()

> **run**(): `Up`

Execute the service function

#### Returns

`Up`

***

### with()

> **with**(`arg`): `Up`

Call the provided function with current Up.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Up`

#### Returns

`Up`
