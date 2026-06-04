[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Check

# Class: Check

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Check**(`ctx?`, `_id?`, `_completed?`, `_description?`, `_name?`, `_passed?`, `_resultEmoji?`): `Check`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`CheckID`](../type-aliases/CheckID.md)

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

### id()

> **id**(): `Promise`\<[`CheckID`](../type-aliases/CheckID.md)\>

A unique identifier for this Check.

#### Returns

`Promise`\<[`CheckID`](../type-aliases/CheckID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

Return the fully qualified name of the check

#### Returns

`Promise`\<`string`\>

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
