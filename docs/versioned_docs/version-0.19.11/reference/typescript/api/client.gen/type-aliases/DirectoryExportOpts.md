[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / DirectoryExportOpts

# Type Alias: DirectoryExportOpts

> **DirectoryExportOpts** = `object`

## Properties

### wipe?

> `optional` **wipe?**: `boolean`

If true, then the host directory will be wiped clean before exporting so that it exactly matches the directory being exported; this means it will delete any files on the host that aren't in the exported dir. If false (the default), the contents of the directory will be merged with any existing contents of the host directory, leaving any existing files on the host that aren't in the exported directory alone.
