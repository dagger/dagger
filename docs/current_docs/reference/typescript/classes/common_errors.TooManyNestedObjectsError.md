---
id: "common_errors.TooManyNestedObjectsError"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[common/errors](../modules/common_errors.md).TooManyNestedObjectsError

Dagger only expects one response value from the engine. If the engine returns more than one value this error is thrown.

## Hierarchy

- [`DaggerSDKError`](common_errors.DaggerSDKError.md)

  ↳ **`TooManyNestedObjectsError`**

## Properties

### cause

 `Optional` **cause**: `Error`

The original error, which caused the DaggerSDKError.

#### Inherited from

[DaggerSDKError](common_errors.DaggerSDKError.md).[cause](common_errors.DaggerSDKError.md#cause)

___

### code

 **code**: ``"D102"``

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

 **name**: ``"TooManyNestedObjectsError"``

The name of the dagger error.

#### Overrides

[DaggerSDKError](common_errors.DaggerSDKError.md).[name](common_errors.DaggerSDKError.md#name)

___

### response

 **response**: `unknown`

the response containing more than one value.

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
