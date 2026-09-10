# GraphQL SDL diff

Compare two SDL files, or Git `revision:path` objects, as a unified diff:

```sh
git fetch upstream main
go run ./hack/sdl-diff upstream/main:docs/docs-graphql/schema.graphqls docs/docs-graphql/schema.graphqls
```

The tool parses and reformats both inputs before comparing them. It omits comments
and descriptions by default, so API changes remain readable in PR descriptions.
Use `-descriptions` to include description changes and `-context N` to adjust the
number of unchanged lines shown around changes.

All declared types, fields (including internal fields), arguments, defaults,
directives, and extensions are retained. Declaration order is preserved within
each SDL definition category; this is a normalized SDL diff, not a breaking-change
classifier. Inputs need only parse, so partial schemas are supported.

Exit status is zero on success, including when differences are found, one on a
read/parse/output error, and two for invalid usage. Identical normalized schemas
produce no output.
