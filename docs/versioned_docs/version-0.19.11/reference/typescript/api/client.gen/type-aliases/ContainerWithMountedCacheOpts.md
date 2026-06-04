[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ContainerWithMountedCacheOpts

# Type Alias: ContainerWithMountedCacheOpts

> **ContainerWithMountedCacheOpts** = `object`

## Properties

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").

***

### owner?

> `optional` **owner?**: `string`

A user:group to set for the mounted cache directory.

Note that this changes the ownership of the specified mount along with the initial filesystem provided by source (if any). It does not have any effect if/when the cache has already been created.

The user and group can either be an ID (1000:1000) or a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

***

### sharing?

> `optional` **sharing?**: [`CacheSharingMode`](../enumerations/CacheSharingMode.md)

Sharing mode of the cache volume.

***

### source?

> `optional` **source?**: [`Directory`](../classes/Directory.md)

Identifier of the directory to use as the cache volume's root.
