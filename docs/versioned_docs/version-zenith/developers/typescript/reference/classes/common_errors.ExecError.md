---
id: "common_errors.ExecError"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "zenith"
---

[common/errors](../modules/common_errors.md).ExecError

API error from an exec operation in a pipeline.

## Hierarchy

- [`DaggerSDKError`](common_errors.DaggerSDKError.md)

  ↳ **`ExecError`**

## Properties

### cause

 `Optional` **cause**: `Error`

The original error, which caused the DaggerSDKError.

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[cause](common_errors.DaggerSDKError.md#cause)

___

### cmd

 **cmd**: `string`[]

The command that caused the error.

___

### code

 **code**: ``"D109"``

The dagger specific error code.
Use this to identify dagger errors programmatically.

#### Overrides

[DaggerSDKError](common_errors.DaggerSDKError.md).[code](common_errors.DaggerSDKError.md#code)

___

### exitCode

 **exitCode**: `number`

The exit code of the command.

___

### message

 **message**: `string`

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[message](common_errors.DaggerSDKError.md#message)

___

### name

 **name**: ``"ExecError"``

The name of the dagger error.

#### Overrides

[DaggerSDKError](common_errors.DaggerSDKError.md).[name](common_errors.DaggerSDKError.md#name)

___

### stack

 `Optional` **stack**: `string`

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[stack](common_errors.DaggerSDKError.md#stack)

___

### stderr

 **stderr**: `string`

The stderr of the command.

___

### stdout

 **stdout**: `string`

The stdout of the command.

## Methods

### printStackTrace

**printStackTrace**(): `void`

Pretty prints the error

#### Returns

`void`

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[printStackTrace](common_errors.DaggerSDKError.md#printstacktrace)

___

### toString

**toString**(): `string`

#### Returns

`string`
