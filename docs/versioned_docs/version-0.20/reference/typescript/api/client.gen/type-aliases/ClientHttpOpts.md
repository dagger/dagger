[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ClientHttpOpts

# Type Alias: ClientHttpOpts

> **ClientHttpOpts** = `object`

## Properties

### authHeader?

> `optional` **authHeader?**: [`Secret`](../classes/Secret.md)

Secret used to populate the Authorization HTTP header

***

### experimentalServiceHost?

> `optional` **experimentalServiceHost?**: [`Service`](../classes/Service.md)

A service which must be started before the URL is fetched.

***

### name?

> `optional` **name?**: `string`

File name to use for the file. Defaults to the last part of the URL.

***

### permissions?

> `optional` **permissions?**: `number`

Permissions to set on the file.
