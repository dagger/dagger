[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / DirectoryWithFileOpts

# Type Alias: DirectoryWithFileOpts

> **DirectoryWithFileOpts** = `object`

## Properties

### owner?

> `optional` **owner?**: `string`

A user:group to set for the copied directory and its contents.

The user and group must be an ID (1000:1000), not a name (foo:bar).

If the group is omitted, it defaults to the same as the user.

***

### permissions?

> `optional` **permissions?**: `number`

Permission given to the copied file (e.g., 0600).
