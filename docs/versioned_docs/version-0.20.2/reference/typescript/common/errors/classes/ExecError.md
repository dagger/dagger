[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [common/errors](../README.md) / ExecError

# Class: ExecError

API error from an exec operation in a pipeline.

## Extends

- [`DaggerSDKError`](DaggerSDKError.md)

## Properties

### cause?

> `optional` **cause?**: `Error`

The original error, which caused the DaggerSDKError.

#### Inherited from

[`DaggerSDKError`](DaggerSDKError.md).[`cause`](DaggerSDKError.md#cause)

***

### cmd

> **cmd**: `string`[]

The command that caused the error.

***

### code

> **code**: `"D109"` = `ERROR_CODES.ExecError`

The dagger specific error code.
Use this to identify dagger errors programmatically.

#### Overrides

[`DaggerSDKError`](DaggerSDKError.md).[`code`](DaggerSDKError.md#code)

***

### exitCode

> **exitCode**: `number`

The exit code of the command.

***

### extensions?

> `optional` **extensions?**: `any`

GraphQL error extensions

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

***

### stderr

> **stderr**: `string`

The stderr of the command.

***

### stdout

> **stdout**: `string`

The stdout of the command.

## Methods

### printStackTrace()

> **printStackTrace**(): `void`

Pretty prints the error

#### Returns

`void`

#### Inherited from

[`DaggerSDKError`](DaggerSDKError.md).[`printStackTrace`](DaggerSDKError.md#printstacktrace)
