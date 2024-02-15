---
id: "common_errors.EngineSessionConnectParamsParseError"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[common/errors](../modules/common_errors.md).EngineSessionConnectParamsParseError

This error is thrown if the EngineSession does not manage to parse the required connection parameters from the session binary

## Hierarchy

- [`DaggerSDKError`](common_errors.DaggerSDKError.md)

  ↳ **`EngineSessionConnectParamsParseError`**

## Properties

### cause

 `Optional` **cause**: `Error`

The original error, which caused the DaggerSDKError.

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[cause](common_errors.DaggerSDKError.md#cause)

___

### code

 **code**: ``"D103"``

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

 **name**: ``"EngineSessionConnectParamsParseError"``

The name of the dagger error.

#### Overrides

[DaggerSDKError](common_errors.DaggerSDKError.md).[name](common_errors.DaggerSDKError.md#name)

___

### parsedLine

 **parsedLine**: `string`

the line, which caused the error during parsing, if the error was caused because of parsing.

___

### stack

 `Optional` **stack**: `string`

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[stack](common_errors.DaggerSDKError.md#stack)

## Methods

### printStackTrace

**printStackTrace**(): `void`

Pretty prints the error

#### Returns

`void`

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[printStackTrace](common_errors.DaggerSDKError.md#printstacktrace)
