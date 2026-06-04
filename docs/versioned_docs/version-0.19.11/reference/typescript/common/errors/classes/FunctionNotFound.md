[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [common/errors](../README.md) / FunctionNotFound

# Class: FunctionNotFound

The base error. Every other error inherits this error.

## Extends

- [`DaggerSDKError`](DaggerSDKError.md)

## Constructors

### Constructor

> **new FunctionNotFound**(`message`, `options?`): `FunctionNotFound`

#### Parameters

##### message

`string`

##### options?

`DaggerSDKErrorOptions`

#### Returns

`FunctionNotFound`

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

> **code**: `"D109"` = `ERROR_CODES.ExecError`

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

> **name**: `"ExecError"` = `ERROR_NAMES.ExecError`

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
