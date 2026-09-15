---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Class: UnknownDaggerError

This error is thrown if the dagger SDK does not identify the error and just wraps the cause.

## Extends

- [`DaggerSDKError`](DaggerSDKError.md)

## Properties

### cause?

> `optional` **cause?**: `Error`

The original error, which caused the DaggerSDKError.

#### Inherited from

[`DaggerSDKError`](DaggerSDKError.md).[`cause`](DaggerSDKError.md#cause)

***

### code

> **code**: `"D101"` = `ERROR_CODES.UnknownDaggerError`

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

> **name**: `"UnknownDaggerError"` = `ERROR_NAMES.UnknownDaggerError`

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
