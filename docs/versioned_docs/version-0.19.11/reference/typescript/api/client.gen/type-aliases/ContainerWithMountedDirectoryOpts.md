[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ContainerWithMountedDirectoryOpts

# Type Alias: ContainerWithMountedDirectoryOpts

> **ContainerWithMountedDirectoryOpts** = `object`

## Properties

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo").

***

### owner?

> `optional` **owner?**: `string`

A user:group to set for the mounted directory and its contents.

The user and group can either be an ID (1000:1000) or a name (foo:bar).

If the group is omitted, it defaults to the same as the user.
