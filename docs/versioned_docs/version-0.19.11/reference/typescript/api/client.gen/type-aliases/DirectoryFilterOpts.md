[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / DirectoryFilterOpts

# Type Alias: DirectoryFilterOpts

> **DirectoryFilterOpts** = `object`

## Properties

### exclude?

> `optional` **exclude?**: `string`[]

If set, paths matching one of these glob patterns is excluded from the new snapshot. Example: ["node_modules/", ".git*", ".env"]

***

### gitignore?

> `optional` **gitignore?**: `boolean`

If set, apply .gitignore rules when filtering the directory.

***

### include?

> `optional` **include?**: `string`[]

If set, only paths matching one of these glob patterns is included in the new snapshot. Example: (e.g., ["app/", "package.*"]).
