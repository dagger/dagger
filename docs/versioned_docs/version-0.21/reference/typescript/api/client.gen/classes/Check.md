---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Check

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Check**(`ctx?`, `_id?`, `_checkType?`, `_completed?`, `_description?`, `_name?`, `_passed?`, `_resultEmoji?`): `Check`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_checkType?

`string`

##### \_completed?

`boolean`

##### \_description?

`string`

##### \_name?

`string`

##### \_passed?

`boolean`

##### \_resultEmoji?

`string`

#### Returns

`Check`

#### Overrides

`BaseClient.constructor`

## Methods

### checkType()

> **checkType**(): `Promise`\<`string`\>

The type of check: 'check' for annotated checks, 'generate' for generate-as-checks

#### Returns

`Promise`\<`string`\>

***

### completed()

> **completed**(): `Promise`\<`boolean`\>

Whether the check completed

#### Returns

`Promise`\<`boolean`\>

***

### description()

> **description**(): `Promise`\<`string`\>

The description of the check

#### Returns

`Promise`\<`string`\>

***

### error()

> **error**(): [`Error`](Error.md)

If the check failed, this is the error

#### Returns

[`Error`](Error.md)

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Check.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

Return the fully qualified name of the check

#### Returns

`Promise`\<`string`\>

***

### originalModule()

> **originalModule**(): [`Module_`](Module.md)

The original module in which the check has been defined

#### Returns

[`Module_`](Module.md)

***

### passed()

> **passed**(): `Promise`\<`boolean`\>

Whether the check passed

#### Returns

`Promise`\<`boolean`\>

***

### path()

> **path**(): `Promise`\<`string`[]\>

The path of the check within its module

#### Returns

`Promise`\<`string`[]\>

***

### resultEmoji()

> **resultEmoji**(): `Promise`\<`string`\>

An emoji representing the result of the check

#### Returns

`Promise`\<`string`\>

***

### run()

> **run**(): `Check`

Execute the check

#### Returns

`Check`

***

### with()

> **with**(`arg`): `Check`

Call the provided function with current Check.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Check`

#### Returns

`Check`
