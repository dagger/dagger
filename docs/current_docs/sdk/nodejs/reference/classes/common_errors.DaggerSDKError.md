---
id: "common_errors.DaggerSDKError"
title: "Dagger NodeJS SDK"
sidebar_label: "Reference"
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

___

### prepareStackTrace

 `Static` `Optional` **prepareStackTrace**: (`err`: `Error`, `stackTraces`: `CallSite`[]) => `any`

#### Type declaration

(`err`, `stackTraces`): `any`

Optional override for formatting stack traces

##### Parameters

| Name | Type |
| :------ | :------ |
| `err` | `Error` |
| `stackTraces` | `CallSite`[] |

##### Returns

`any`

**`See`**

https://v8.dev/docs/stack-trace-api#customizing-stack-traces

#### Inherited from

Error.prepareStackTrace

___

### stackTraceLimit

 `Static` **stackTraceLimit**: `number`

#### Inherited from

Error.stackTraceLimit

## Methods

### printStackTrace

**printStackTrace**(): `void`

Pretty prints the error

#### Returns

`void`

___

### captureStackTrace

**captureStackTrace**(`targetObject`, `constructorOpt?`): `void`

Create .stack property on a target object

#### Parameters

| Name | Type |
| :------ | :------ |
| `targetObject` | `object` |
| `constructorOpt?` | `Function` |

#### Returns

`void`

#### Inherited from

Error.captureStackTrace
