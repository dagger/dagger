[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [common/errors](../README.md) / GraphQLRequestError

# Class: GraphQLRequestError

This error originates from the dagger engine. It means that some error was thrown and sent back via GraphQL.

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

> **code**: `"D100"` = `ERROR_CODES.GraphQLRequestError`

The dagger specific error code.
Use this to identify dagger errors programmatically.

#### Overrides

[`DaggerSDKError`](DaggerSDKError.md).[`code`](DaggerSDKError.md#code)

***

### extensions?

> `optional` **extensions?**: `any`

The GraphQL error extentions.

***

### message

> **message**: `string`

#### Inherited from

[`DaggerSDKError`](DaggerSDKError.md).[`message`](DaggerSDKError.md#message)

***

### name

> **name**: `"GraphQLRequestError"` = `ERROR_NAMES.GraphQLRequestError`

The name of the dagger error.

#### Overrides

[`DaggerSDKError`](DaggerSDKError.md).[`name`](DaggerSDKError.md#name)

***

### requestContext

> **requestContext**: `ClientError`

The query and variables, which caused the error.

***

### response

> **response**: `ClientError`

the GraphQL response containing the error.

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
