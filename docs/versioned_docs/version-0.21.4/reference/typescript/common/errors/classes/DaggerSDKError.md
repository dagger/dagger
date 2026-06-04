---
displayed_sidebar: current
sidebar_label: TypeScript SDK Reference
title: TypeScript SDK Reference
---

# Abstract Class: DaggerSDKError

The base error. Every other error inherits this error.

## Extends

- `Error`

## Extended by

- [`UnknownDaggerError`](UnknownDaggerError.md)
- [`DockerImageRefValidationError`](DockerImageRefValidationError.md)
- [`EngineSessionConnectParamsParseError`](EngineSessionConnectParamsParseError.md)
- [`ExecError`](ExecError.md)
- [`GraphQLRequestError`](GraphQLRequestError.md)
- [`InitEngineSessionBinaryError`](InitEngineSessionBinaryError.md)
- [`TooManyNestedObjectsError`](TooManyNestedObjectsError.md)
- [`EngineSessionError`](EngineSessionError.md)
- [`EngineSessionConnectionTimeoutError`](EngineSessionConnectionTimeoutError.md)
- [`NotAwaitedRequestError`](NotAwaitedRequestError.md)
- [`FunctionNotFound`](FunctionNotFound.md)
- [`IntrospectionError`](IntrospectionError.md)

## Properties

### cause?

> `optional` **cause?**: `Error`

The original error, which caused the DaggerSDKError.

#### Overrides

`Error.cause`

***

### code

> `abstract` `readonly` **code**: `ErrorCodes`

The dagger specific error code.
Use this to identify dagger errors programmatically.

***

### message

> **message**: `string`

#### Inherited from

`Error.message`

***

### name

> `abstract` `readonly` **name**: `"GraphQLRequestError"` \| `"UnknownDaggerError"` \| `"TooManyNestedObjectsError"` \| `"EngineSessionConnectParamsParseError"` \| `"EngineSessionConnectionTimeoutError"` \| `"EngineSessionError"` \| `"InitEngineSessionBinaryError"` \| `"DockerImageRefValidationError"` \| `"NotAwaitedRequestError"` \| `"ExecError"` \| `"IntrospectionError"`

The name of the dagger error.

#### Overrides

`Error.name`

***

### stack?

> `optional` **stack?**: `string`

#### Inherited from

`Error.stack`

## Methods

### printStackTrace()

> **printStackTrace**(): `void`

Pretty prints the error

#### Returns

`void`
