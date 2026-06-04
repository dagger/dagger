[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Secret

# Class: Secret

A reference to a secret value, which can be handled more safely than the value itself.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Secret**(`ctx?`, `_id?`, `_name?`, `_plaintext?`, `_uri?`): `Secret`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`SecretID`](../type-aliases/SecretID.md)

##### \_name?

`string`

##### \_plaintext?

`string`

##### \_uri?

`string`

#### Returns

`Secret`

#### Overrides

`BaseClient.constructor`

## Methods

### id()

> **id**(): `Promise`\<[`SecretID`](../type-aliases/SecretID.md)\>

A unique identifier for this Secret.

#### Returns

`Promise`\<[`SecretID`](../type-aliases/SecretID.md)\>

***

### name()

> **name**(): `Promise`\<`string`\>

The name of this secret.

#### Returns

`Promise`\<`string`\>

***

### plaintext()

> **plaintext**(): `Promise`\<`string`\>

The value of this secret.

#### Returns

`Promise`\<`string`\>

***

### uri()

> **uri**(): `Promise`\<`string`\>

The URI of this secret.

#### Returns

`Promise`\<`string`\>
