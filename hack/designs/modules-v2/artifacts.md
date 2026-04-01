# Artifacts

## Status: Designed

Depends on: Workspace plumbing (done)

## Summary

A filterable, introspection-driven view over workspace objects. The CLI is a
generic client — no per-workspace codegen. `Workspace.artifacts` returns an
`Artifacts` type with chainable filters, dimension discovery, and verb methods
that compile to [Execution Plans](./plans.md).

## No Artifact Addresses

Typed collection filters collapse the need for a separate artifact address
concept. See [../do-we-need-artifact-addresses.md](../do-we-need-artifact-addresses.md)
for the full analysis.

- **Artifacts are collection items.** The noun quality comes from the Artifact
  type in the API, not from a separate identity layer.
- **Blessed scalar types** (WorkspacePath, HTTPAddress, etc.) carry parsing and
  rendering semantics for filter values, not identity semantics.

## Schema

```graphql
"""Entry point to the artifact system."""
extend type Workspace {
  """A filterable view of all artifacts in this workspace."""
  artifacts: Artifacts!
}

"""
A scoped, filterable view over workspace artifacts.
Chainable: every filter returns a narrowed Artifacts.
"""
type Artifacts {
  """Narrow by a dimension, optionally to specific values."""
  filterBy(dimension: String!, values: [String!]): Artifacts!

  """Narrow to artifacts reachable by check handlers."""
  filterCheck: Artifacts!

  """Narrow to artifacts reachable by generate handlers."""
  filterGenerate: Artifacts!

  """Narrow to artifacts reachable by ship handlers."""
  filterShip: Artifacts!

  """Narrow to artifacts reachable by up handlers."""
  filterUp: Artifacts!

  """Filterable dimensions available in the current scope. Static."""
  dimensions: [ArtifactDimension!]!

  """Artifacts matching the current filters."""
  items: [Artifact!]!

  """Union of available function names across all in-scope artifacts. Static."""
  actions: [String!]!

  """Create an action targeting all in-scope artifacts."""
  action(name: String!): Action!

  """Compile a check execution plan for the current scope."""
  check: Plan!

  """Compile a generate execution plan for the current scope."""
  generate: Plan!

  """Compile a ship execution plan for the current scope."""
  ship: Plan!

  """Compile an up execution plan for the current scope."""
  up: Plan!
}

"""A filterable axis of the artifact graph."""
type ArtifactDimension {
  """Filter name as used in CLI flags. Example: "go-module"."""
  name: String!

  """Type of this dimension's keys. Determines parsing and rendering."""
  keyType: TypeDef!
}

"""One artifact in the workspace."""
type Artifact {
  """This artifact's key within its immediate collection."""
  key: String!

  """Ancestor collection coordinates tracing the path from root."""
  ancestors: [ArtifactCoordinate!]!

  """Fields on the underlying object, for inspection."""
  fields: [FieldValue!]!

  """Available function names on the underlying object."""
  actions: [String!]!

  """Create an action targeting this single artifact."""
  action(name: String!): Action!
}

"""A position along one dimension of the artifact graph."""
type ArtifactCoordinate {
  """Which dimension. Example: "go-module"."""
  dimension: String!

  """The key value, rendered per its key type. Example: "./cmd/api"."""
  value: String!
}

"""One field on an artifact's underlying object."""
type FieldValue {
  """Field name."""
  name: String!

  """Field type."""
  typeDef: TypeDef!

  """Raw value as JSON."""
  json: JSON!

  """Human-readable rendering."""
  display: String!
}
```

For the Action and Plan types, see [plans.md](./plans.md).

## Filter Model

One filter model across all commands: `--<dimension>=<value>`, repeatable, no
comma-separated values. Named by item type (singular). Each flag points to
`dagger list` for discovery:

```
$ dagger check --help
  --module=<name>       Filter by module (see: dagger list modules)
  --go-module=<name>    Filter by go module (see: dagger list go-modules)
  --go-test=<name>      Filter by go test (see: dagger list go-tests)
```

The CLI generates flags from `Artifacts.dimensions`, parses user input into
`filterBy` chains, and calls verb methods or `items` on the result.

## CLI Mappings

```
dagger list                → workspace.artifacts.dimensions
dagger list modules        → workspace.artifacts.filterBy("module").items
dagger list go-modules     → workspace.artifacts.filterBy("go-module").items
dagger list go-tests       → workspace.artifacts.filterBy("go-test").items
dagger list go-tests \
  --go-module=./cmd/api    → workspace.artifacts
                               .filterBy("go-test")
                               .filterBy("go-module", ["./cmd/api"])
                               .items
dagger check --module=go   → workspace.artifacts
                               .filterBy("module", ["go"])
                               .check.run
dagger check --module=go \
  --plan                   → workspace.artifacts
                               .filterBy("module", ["go"])
                               .check.nodes  (display the plan)
dagger check --help        → workspace.artifacts.filterCheck.dimensions
dagger check \
  --go-test=Foo \
  --go-test=Bar            → workspace.artifacts
                               .filterBy("go-test", ["Foo", "Bar"])
                               .check.run
```

## Static vs Dynamic

- **Static (schema-only, no runtime cost):** dimensions, verb-scoped
  dimensions, action names, filter flag generation
- **Dynamic (reads collection keys):** items, item counts, plan compilation,
  plan execution

## Filter Algebra

- Every chained `filterBy` / `filterCheck` / etc. is **AND**
- Multiple values within one `filterBy` call is **OR** (list of values)
- `filterBy("X")` (no values) → select all items of dimension X
- `filterBy("X", ["a"]).filterBy("X", ["b"])` → intersection (AND) — empty if
  no overlap
- `filterCheck` / `filterShip` / etc. → narrow to verb-reachable paths

## Open Questions

1. Exact rules for automatic column disambiguation when non-collection fields
   create ambiguous paths.
2. How cross-artifact reference ordering interacts with the filter system.
3. Whether schema-path notation (`go:lint`) should be deprecated in favor of
   typed filters.
4. `--mod` (global module loader) vs `--module` (artifact dimension filter) —
   distinct long forms, but worth cleaning up `--mod` as a true global flag
   now that workspace-plumbing moved module loading to the engine.
