[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / ClientModuleSourceOpts

# Type Alias: ClientModuleSourceOpts

> **ClientModuleSourceOpts** = `object`

## Properties

### allowNotExists?

> `optional` **allowNotExists?**: `boolean`

If true, do not error out if the provided ref string is a local path and does not exist yet. Useful when initializing new modules in directories that don't exist yet.

***

### disableFindUp?

> `optional` **disableFindUp?**: `boolean`

If true, do not attempt to find dagger.json in a parent directory of the provided path. Only relevant for local module sources.

***

### refPin?

> `optional` **refPin?**: `string`

The pinned version of the module source

***

### requireKind?

> `optional` **requireKind?**: [`ModuleSourceKind`](../enumerations/ModuleSourceKind.md)

If set, error out if the ref string is not of the provided requireKind.
