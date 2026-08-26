[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ContainerWithNewFileOpts

# Type Alias: ContainerWithNewFileOpts

> **ContainerWithNewFileOpts** = `object`

## Properties

### expand?

> `optional` **expand?**: `boolean`

Replace "$\{VAR\}" or "$VAR" in the value of path according to the current environment variables defined in the container (e.g. "/$VAR/foo.txt").

***

### owner?

> `optional` **owner?**: `string`

A user:group to set for the file.

The user and group can either be an ID (1000:1000) or a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

***

### permissions?

> `optional` **permissions?**: `number`

Permissions of the new file. Example: 0600
