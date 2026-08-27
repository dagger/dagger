[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / Address

# Class: Address

A standardized address to load containers, directories, secrets, and other object types. Address format depends on the type, and is validated at type selection.

## Extends

- `BaseClient`

## Constructors

### Constructor

> **new Address**(`ctx?`, `_id?`, `_value?`): `Address`

Constructor is used for internal usage only, do not create object from it.

#### Parameters

##### ctx?

`Context`

##### \_id?

[`AddressID`](../type-aliases/AddressID.md)

##### \_value?

`string`

#### Returns

`Address`

#### Overrides

`BaseClient.constructor`

## Methods

### container()

> **container**(): [`Container`](Container.md)

Load a container from the address.

#### Returns

[`Container`](Container.md)

***

### directory()

> **directory**(`opts?`): [`Directory`](Directory.md)

Load a directory from the address.

#### Parameters

##### opts?

[`AddressDirectoryOpts`](../type-aliases/AddressDirectoryOpts.md)

#### Returns

[`Directory`](Directory.md)

***

### file()

> **file**(`opts?`): [`File`](File.md)

Load a file from the address.

#### Parameters

##### opts?

[`AddressFileOpts`](../type-aliases/AddressFileOpts.md)

#### Returns

[`File`](File.md)

***

### gitRef()

> **gitRef**(): [`GitRef`](GitRef.md)

Load a git ref (branch, tag or commit) from the address.

#### Returns

[`GitRef`](GitRef.md)

***

### gitRepository()

> **gitRepository**(): [`GitRepository`](GitRepository.md)

Load a git repository from the address.

#### Returns

[`GitRepository`](GitRepository.md)

***

### id()

> **id**(): `Promise`\<[`AddressID`](../type-aliases/AddressID.md)\>

A unique identifier for this Address.

#### Returns

`Promise`\<[`AddressID`](../type-aliases/AddressID.md)\>

***

### secret()

> **secret**(): [`Secret`](Secret.md)

Load a secret from the address.

#### Returns

[`Secret`](Secret.md)

***

### service()

> **service**(): [`Service`](Service.md)

Load a service from the address.

#### Returns

[`Service`](Service.md)

***

### socket()

> **socket**(): [`Socket`](Socket.md)

Load a local socket from the address.

#### Returns

[`Socket`](Socket.md)

***

### value()

> **value**(): `Promise`\<`string`\>

The address value

#### Returns

`Promise`\<`string`\>
