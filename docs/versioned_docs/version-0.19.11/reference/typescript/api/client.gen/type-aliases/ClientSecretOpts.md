[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ClientSecretOpts

# Type Alias: ClientSecretOpts

> **ClientSecretOpts** = `object`

## Properties

### cacheKey?

> `optional` **cacheKey?**: `string`

If set, the given string will be used as the cache key for this secret. This means that any secrets with the same cache key will be considered equivalent in terms of cache lookups, even if they have different URIs or plaintext values.

For example, two secrets with the same cache key provided as secret env vars to other wise equivalent containers will result in the container withExecs hitting the cache for each other.

If not set, the cache key for the secret will be derived from its plaintext value as looked up when the secret is constructed.
