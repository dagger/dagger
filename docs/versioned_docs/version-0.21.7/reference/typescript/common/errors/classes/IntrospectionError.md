---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: IntrospectionError

The base error. Every other error inherits this error.

## Extends

- [`DaggerSDKError`](DaggerSDKError.md)

## Constructors

### Constructor

> **new IntrospectionError**(`message`, `options?`): `IntrospectionError`

#### Parameters

##### message

`string`

##### options?

`DaggerSDKErrorOptions`

#### Returns

`IntrospectionError`

#### Overrides

`DaggerSDKError.constructor`

## Properties

### cause?

> `optional` **cause?**: `Error`

The original error, which caused the DaggerSDKError.

#### Inherited from

[`DaggerSDKError`](DaggerSDKError.md).[`cause`](DaggerSDKError.md#cause)

***

### code

> **code**: `"D110"` = `ERROR_CODES.IntrospectionError`

The dagger specific error code.
Use this to identify dagger errors programmatically.

#### Overrides

[`DaggerSDKError`](DaggerSDKError.md).[`code`](DaggerSDKError.md#code)

***

### message

> **message**: `string`

#### Inherited from

[`DaggerSDKError`](DaggerSDKError.md).[`message`](DaggerSDKError.md#message)

***

### name

> **name**: `"IntrospectionError"` = `ERROR_NAMES.IntrospectionError`

The name of the dagger error.

#### Overrides

[`DaggerSDKError`](DaggerSDKError.md).[`name`](DaggerSDKError.md#name)

***

### stack?

> `optional` **stack?**: `string`

#### Inherited from

[`DaggerSDKError`](DaggerSDKError.md).[`stack`](DaggerSDKError.md#stack)

## Methods

### printStackTrace()

> **printStackTrace**(): `void`

Pretty prints the error

#### Returns

`void`

#### Inherited from

[`DaggerSDKError`](DaggerSDKError.md).[`printStackTrace`](DaggerSDKError.md#printstacktrace)
