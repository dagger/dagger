---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: Function\_

Function represents a resolver provided by a Module.

A function always evaluates against a parent object and is given a set of named arguments.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Function\_**(`ctx?`, `_id?`, `_deprecated?`, `_description?`, `_name?`, `_sourceModuleName?`): `Function_`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`ID`](../type-aliases/ID.md)

##### \_deprecated?

`string`

##### \_description?

`string`

##### \_name?

`string`

##### \_sourceModuleName?

`string`

#### Returns

`Function_`

#### Overrides

`BaseClient.constructor`

## Methods

### args()

> **args**(): `Promise`\<[`FunctionArg`](FunctionArg.md)[]\>

Arguments accepted by the function, if any.

#### Returns

`Promise`\<[`FunctionArg`](FunctionArg.md)[]\>

***

### deprecated()

> **deprecated**(): `Promise`\<`string`\>

The reason this function is deprecated, if any.

#### Returns

`Promise`\<`string`\>

***

### description()

> **description**(): `Promise`\<`string`\>

A doc string for the function, if any.

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`ID`](../type-aliases/ID.md)\>

A unique identifier for this Function.

#### Returns

`Promise`\<[`ID`](../type-aliases/ID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of the function.

#### Returns

`Promise`\<`string`\>

***

### returnType()

> **returnType**(): [`TypeDef`](TypeDef.md)

The type returned by the function.

#### Returns

[`TypeDef`](TypeDef.md)

***

### sourceMap()

> **sourceMap**(): [`SourceMap`](SourceMap.md)

The location of this function declaration.

#### Returns

[`SourceMap`](SourceMap.md)

***

### sourceModuleName()

> **sourceModuleName**(): `Promise`\<`string`\>

If this function is provided by a module, the name of the module. Unset otherwise.

#### Returns

`Promise`\<`string`\>

***

### with()

> **with**(`arg`): `Function_`

Call the provided function with current Function.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `Function_`

#### Returns

`Function_`

***

### withArg()

> **withArg**(`name`, `typeDef`, `opts?`): `Function_`

Returns the function with the provided argument

#### Parameters

##### name

`string`

The name of the argument

##### typeDef

[`TypeDef`](TypeDef.md)

The type of the argument

##### opts?

[`FunctionWithArgOpts`](../type-aliases/FunctionWithArgOpts.md)

#### Returns

`Function_`

***

### withCachePolicy()

> **withCachePolicy**(`policy`, `opts?`): `Function_`

Returns the function updated to use the provided cache policy.

#### Parameters

##### policy

[`FunctionCachePolicy`](../enumerations/FunctionCachePolicy.md)

The cache policy to use.

##### opts?

[`FunctionWithCachePolicyOpts`](../type-aliases/FunctionWithCachePolicyOpts.md)

#### Returns

`Function_`

***

### withCheck()

> **withCheck**(): `Function_`

Returns the function with a flag indicating it's a check.

#### Returns

`Function_`

***

### withDeprecated()

> **withDeprecated**(`opts?`): `Function_`

Returns the function with the provided deprecation reason.

#### Parameters

##### opts?

[`FunctionWithDeprecatedOpts`](../type-aliases/FunctionWithDeprecatedOpts.md)

#### Returns

`Function_`

***

### withDescription()

> **withDescription**(`description`): `Function_`

Returns the function with the given doc string.

#### Parameters

##### description

`string`

The doc string to set.

#### Returns

`Function_`

***

### withGenerator()

> **withGenerator**(): `Function_`

Returns the function with a flag indicating it's a generator.

#### Returns

`Function_`

***

### withSourceMap()

> **withSourceMap**(`sourceMap`): `Function_`

Returns the function with the given source map.

#### Parameters

##### sourceMap

[`SourceMap`](SourceMap.md)

The source map for the function definition.

#### Returns

`Function_`

***

### withUp()

> **withUp**(): `Function_`

Returns the function with a flag indicating it returns a service for dagger up.

#### Returns

`Function_`
