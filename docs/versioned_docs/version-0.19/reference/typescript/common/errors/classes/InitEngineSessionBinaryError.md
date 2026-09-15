[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [common/errors](../README.md) / InitEngineSessionBinaryError

# Class: InitEngineSessionBinaryError

This error is thrown if the dagger binary cannot be copied from the dagger docker image and copied to the local host.

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

> **code**: `"D106"` = `ERROR_CODES.InitEngineSessionBinaryError`

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

> **name**: `"InitEngineSessionBinaryError"` = `ERROR_NAMES.InitEngineSessionBinaryError`

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
