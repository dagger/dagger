[**@dagger.io/dagger**](../../../README.md)

***

[@dagger.io/dagger](../../../modules.md) / [api/client.gen](../README.md) / FileSearchOpts

# Type Alias: FileSearchOpts

> **FileSearchOpts** = `object`

## Properties

### dotall?

> `optional` **dotall?**: `boolean`

Allow the . pattern to match newlines in multiline mode.

***

### filesOnly?

> `optional` **filesOnly?**: `boolean`

Only return matching files, not lines and content

***

### globs?

> `optional` **globs?**: `string`[]

***

### insensitive?

> `optional` **insensitive?**: `boolean`

Enable case-insensitive matching.

***

### limit?

> `optional` **limit?**: `number`

Limit the number of results to return

***

### literal?

> `optional` **literal?**: `boolean`

Interpret the pattern as a literal string instead of a regular expression.

***

### multiline?

> `optional` **multiline?**: `boolean`

Enable searching across multiple lines.

***

### paths?

> `optional` **paths?**: `string`[]

***

### skipHidden?

> `optional` **skipHidden?**: `boolean`

Skip hidden files (files starting with .).

***

### skipIgnored?

> `optional` **skipIgnored?**: `boolean`

Honor .gitignore, .ignore, and .rgignore files.
