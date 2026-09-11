# GraphQL SDL diff

Compare two SDL files, or Git `revision:path` objects, semantically:

```sh
git fetch upstream main
go run ./hack/sdl-diff upstream/main:docs/docs-graphql/schema.graphqls docs/docs-graphql/schema.graphqls
```

New types and directives appear as full SDL declarations. Added fields, enum
values, union members, and implemented interfaces appear in `extend` declarations:

```graphql
extend type Workspace {
  snapshot: Workspace!
  withConfigEnvironment(name: String!): Workspace!
}
```

SDL has no deletion or replacement syntax. Removed declarations appear in
`# Removed:` comment blocks; modified declarations appear in paired
`# Changed (before):` and `# Changed (after):` comment blocks. The output is a
review summary, not an executable schema migration.

All declared types, fields (including internal fields), arguments, defaults,
directives, and extensions are compared. The tool combines type extensions and
ignores formatting, comments, descriptions, and ordering of named members. Use
`-descriptions` to include description changes. Directive application order and
list default order are preserved because they can affect behavior. Inputs need
only parse, so partial schemas are supported. This is not a breaking-change
classifier.

Output follows the new schema’s declaration order within each section; removed
APIs follow in old schema order. Members in additions and “after” blocks retain
new schema order, while removals and “before” blocks retain old schema order.

Exit status is zero on success, including when differences are found, one on a
read/parse/output error, and two for invalid usage. Semantically equal schemas
produce no output.
