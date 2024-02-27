---
id: "common_errors.DaggerSDKError"
title: "TypeScript SDK Reference"
sidebar_label: "TypeScript SDK Reference"
custom_edit_url: null
displayed_sidebar: "current"
---

[common/errors](../modules/common_errors.md).DaggerSDKError

The base error. Every other error inherits this error.

## Hierarchy

- `Error`

  ↳ **`DaggerSDKError`**

  ↳↳ [`UnknownDaggerError`](common_errors.UnknownDaggerError.md)

  ↳↳ [`DockerImageRefValidationError`](common_errors.DockerImageRefValidationError.md)

  ↳↳ [`EngineSessionConnectParamsParseError`](common_errors.EngineSessionConnectParamsParseError.md)

  ↳↳ [`ExecError`](common_errors.ExecError.md)

  ↳↳ [`GraphQLRequestError`](common_errors.GraphQLRequestError.md)

  ↳↳ [`InitEngineSessionBinaryError`](common_errors.InitEngineSessionBinaryError.md)

  ↳↳ [`TooManyNestedObjectsError`](common_errors.TooManyNestedObjectsError.md)

  ↳↳ [`EngineSessionError`](common_errors.EngineSessionError.md)

  ↳↳ [`EngineSessionConnectionTimeoutError`](common_errors.EngineSessionConnectionTimeoutError.md)

  ↳↳ [`NotAwaitedRequestError`](common_errors.NotAwaitedRequestError.md)

## Properties

### cause

 `Optional` **cause**: `Error`

The original error, which caused the DaggerSDKError.

#### Overrides

Error.cause

___

### code

 `Readonly` `Abstract` **code**: `ErrorCodes`

The dagger specific error code.
Use this to identify dagger errors programmatically.

___

### message

 **message**: `string`

#### Inherited from

Error.message

___

### name

 `Readonly` `Abstract` **name**: ``"GraphQLRequestError"`` \| ``"UnknownDaggerError"`` \| ``"TooManyNestedObjectsError"`` \| ``"EngineSessionConnectParamsParseError"`` \| ``"EngineSessionConnectionTimeoutError"`` \| ``"EngineSessionError"`` \| ``"InitEngineSessionBinaryError"`` \| ``"DockerImageRefValidationError"`` \| ``"NotAwaitedRequestError"`` \| ``"ExecError"``

The name of the dagger error.

#### Overrides

Error.name

___

### stack

 `Optional` **stack**: `string`

#### Inherited from

Error.stack

## Methods

### printStackTrace

**printStackTrace**(): `void`

Pretty prints the error

#### Returns

`void`
