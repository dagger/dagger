[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / EnvFile

# Class: EnvFile

A collection of environment variables.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new EnvFile**(`ctx?`, `_id?`, `_exists?`, `_get?`): `EnvFile`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`EnvFileID`](../type-aliases/EnvFileID.md)

##### \_exists?

`boolean`

##### \_get?

`string`

#### Returns

`EnvFile`

#### Overrides

`BaseClient.constructor`

## Methods

### asFile()

> **asFile**(): [`File`](File.md)

Return as a file

#### Returns

[`File`](File.md)

***

### exists()

> **exists**(`name`): `Promise`\<`boolean`\>

Check if a variable exists

#### Parameters

##### name

`string`

Variable name

#### Returns

`Promise`\<`boolean`\>

***

### get()

> **get**(`name`, `opts?`): `Promise`\<`string`\>

Lookup a variable (last occurrence wins) and return its value, or an empty string

#### Parameters

##### name

`string`

Variable name

##### opts?

[`EnvFileGetOpts`](../type-aliases/EnvFileGetOpts.md)

#### Returns

`Promise`\<`string`\>

***

### id()

> **id**(): `Promise`\<[`EnvFileID`](../type-aliases/EnvFileID.md)\>

A unique identifier for this EnvFile.

#### Returns

`Promise`\<[`EnvFileID`](../type-aliases/EnvFileID.md)\>

***

### namespace\_()

> **namespace\_**(`prefix`): `EnvFile`

Filters variables by prefix and removes the pref from keys. Variables without the prefix are excluded. For example, with the prefix "MY_APP_" and variables: MY_APP_TOKEN=topsecret MY_APP_NAME=hello FOO=bar the resulting environment will contain: TOKEN=topsecret NAME=hello

#### Parameters

##### prefix

`string`

The prefix to filter by

#### Returns

`EnvFile`

***

### variables()

> **variables**(`opts?`): `Promise`\<[`EnvVariable`](EnvVariable.md)[]\>

Return all variables

#### Parameters

##### opts?

[`EnvFileVariablesOpts`](../type-aliases/EnvFileVariablesOpts.md)

#### Returns

`Promise`\<[`EnvVariable`](EnvVariable.md)[]\>

***

### with()

> **with**(`arg`): `EnvFile`

Call the provided function with current EnvFile.

This is useful for reusability and readability by not breaking the calling chain.

#### Parameters

##### arg

(`param`) => `EnvFile`

#### Returns

`EnvFile`

***

### withoutVariable()

> **withoutVariable**(`name`): `EnvFile`

Remove all occurrences of the named variable

#### Parameters

##### name

`string`

Variable name

#### Returns

`EnvFile`

***

### withVariable()

> **withVariable**(`name`, `value`): `EnvFile`

Add a variable

#### Parameters

##### name

`string`

Variable name

##### value

`string`

Variable value

#### Returns

`EnvFile`
