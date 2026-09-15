[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Generator

# Class: Generator

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Generator**(`ctx?`, `_id?`, `_completed?`, `_description?`, `_isEmpty?`, `_name?`): `Generator`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`GeneratorID`](../type-aliases/GeneratorID.md)

##### \_completed?

`boolean`

##### \_description?

`string`

##### \_isEmpty?

`boolean`

##### \_name?

`string`

#### Returns

`Generator`

#### Overrides

`BaseClient.constructor`

## Methods

### changes()

> **changes**(): [`Changeset`](Changeset.md)

The generated changeset

#### Returns

[`Changeset`](Changeset.md)

***

### completed()

> **completed**(): `Promise`\<`boolean`\>

Whether the generator complete

#### Returns

`Promise`\<`boolean`\>

***

### description()

> **description**(): `Promise`\<`string`\>

Return the description of the generator

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`GeneratorID`](../type-aliases/GeneratorID.md)\>

A unique identifier for this Generator.

#### Returns

`Promise`\<[`GeneratorID`](../type-aliases/GeneratorID.md)\>

***

### isEmpty()

> **isEmpty**(): `Promise`\<`boolean`\>

Wether changeset from the generator execution is empty or not

#### Returns

`Promise`\<`boolean`\>

***

### name()

> **name**(): `Promise`\<`string`\>

Return the fully qualified name of the generator

#### Returns

`Promise`\<`string`\>

***

### originalModule()

> **originalModule**(): [`Module_`](Module.md)

The original module in which the generator has been defined

#### Returns

[`Module_`](Module.md)

***

### path()

> **path**(): `Promise`\<`string`[]\>

The path of the generator within its module

#### Returns

`Promise`\<`string`[]\>

***

### run()

> **run**(): `Generator`

Execute the generator

#### Returns

`Generator`

***

### with()

> **with**(`arg`): `Generator`

Call the provided function with current Generator.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Generator`

#### Returns

`Generator`
