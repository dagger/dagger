# GraphQL SDL diff

Compare two SDL files, or Git `revision:path` objects, semantically:

```sh
git fetch upstream main
go -C hack/sdl-diff run . upstream/main:docs/docs-graphql/schema.graphqls ../../docs/docs-graphql/schema.graphqls
```

The primary output is one annotated **after** SDL fragment, with unchanged APIs
omitted. New types and directives appear as full declarations. Added and changed
members of existing types appear together in `extend` declarations:

```graphql
# Added: implements Syncer
extend type Workspace implements Syncer {
  # Added
  sync: ID! @expectedType(name: "Workspace")
  # Changed: previously withConfigEnvironment(name: String): Workspace!
  withConfigEnvironment(name: String!): Workspace!
  # Removed: reloaded: Workspace!
}
```

SDL has no deletion or replacement syntax. Concise `# Removed:` comments identify
removals; `# Changed: previously ...` comments show prior signatures. Description-
only edits use `# Description changed` (identifying affected arguments when
applicable), followed by the new descriptions as normal SDL. Comments never
interrupt description strings. The output is a review summary, not an executable
schema migration or a complete schema.

PR sections keep this fragment in a `graphql` fence for syntax highlighting and
include a collapsed **Detailed diff** with a `diff` fence. The detailed view shows
exact before/after lines of selected, normalized SDL fragments, with shared
context once. Named members are canonicalized there so reordering does not appear
as a change, even alongside real edits. Line comparison is bounded per fragment,
not a quadratic full-schema diff. The file/revision CLI continues to emit just
the primary SDL fragment.

All declared types, fields (including internal fields), arguments, defaults,
directives, and extensions are compared. The tool combines type extensions and
ignores formatting, comments, and ordering of named members. Descriptions are
included by default; use `-descriptions=false` to omit description changes.
Directive application order and list default order are preserved because they
can affect behavior. Inputs need only parse, so partial schemas are supported.
This is not a breaking-change classifier.

The primary fragment follows the new schema’s declaration and member order;
removed APIs follow in old schema order. The detailed diff uses canonical named-
member order on both sides, while retaining directive application and list order.

Exit status is zero on success, including when differences are found, one on a
read/parse/output error, and two for invalid usage. Semantically equal schemas
produce no output.
