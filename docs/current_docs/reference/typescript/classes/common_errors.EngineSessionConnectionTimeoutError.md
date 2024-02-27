---
id: "common_errors.EngineSessionConnectionTimeoutError"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[common/errors](../modules/common_errors.md).EngineSessionConnectionTimeoutError

This error is thrown if the EngineSession does not manage to parse the required port successfully because the sessions connection timed out.

## Hierarchy

- [`DaggerSDKError`](common_errors.DaggerSDKError.md)

  ↳ **`EngineSessionConnectionTimeoutError`**

## Properties

### cause

 `Optional` **cause**: `Error`

The original error, which caused the DaggerSDKError.

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[cause](common_errors.DaggerSDKError.md#cause)

___

### code

 **code**: ``"D104"``

The dagger specific error code.
Use this to identify dagger errors programmatically.

#### Overrides

[DaggerSDKError](common_errors.DaggerSDKError.md).[code](common_errors.DaggerSDKError.md#code)

___

### message

 **message**: `string`

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[message](common_errors.DaggerSDKError.md#message)

___

### name

 **name**: ``"EngineSessionConnectionTimeoutError"``

The name of the dagger error.

#### Overrides

[DaggerSDKError](common_errors.DaggerSDKError.md).[name](common_errors.DaggerSDKError.md#name)

___

### stack

 `Optional` **stack**: `string`

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[stack](common_errors.DaggerSDKError.md#stack)

___

### timeOutDuration

 **timeOutDuration**: `number`

The duration until the timeout occurred in ms.

## Methods

### printStackTrace

**printStackTrace**(): `void`

Pretty prints the error

#### Returns

`void`

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[printStackTrace](common_errors.DaggerSDKError.md#printstacktrace)
