[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ContainerExistsOpts

# Type Alias: ContainerExistsOpts

> **ContainerExistsOpts** = `object`

## Properties

### doNotFollowSymlinks?

> `optional` **doNotFollowSymlinks?**: `boolean`

If specified, do not follow symlinks.

***

### expectedType?

> `optional` **expectedType?**: [`ExistsType`](../enumerations/ExistsType.md)

If specified, also validate the type of file (e.g. "REGULAR_TYPE", "DIRECTORY_TYPE", or "SYMLINK_TYPE").
