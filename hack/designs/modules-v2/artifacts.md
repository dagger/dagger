# Artifacts

## Status: Designed

Depends on: Workspace plumbing (done)

## Table of Contents

- [Problem](#problem)
- [Solution](#solution)
- [Selector Model](#selector-model)
- [Selector Algebra](#selector-algebra)
- [CLI](#cli)
- [Schema](#schema)
- [Notes](#notes)
- [Initial Scope](#initial-scope)
- [Implementation Notes](#implementation-notes)
- [Open Questions](#open-questions)

## Problem

1. **Selection is fragmented** - Today, targeting is split across schema-path
   notation, command-specific behavior, and whatever structure happens to exist
   in a module.
2. **Non-collection objects still need disambiguation** - Checks, generators,
   and other verb-reachable objects may need typed selectors even when they do
   not come from collections.
3. **Collections should not define the base model** - Collections add useful
   keyed dimensions later, but the selector model must already work for
   ordinary workspace/module structure.

## Solution

A filterable, introspection-driven view over workspace objects. The CLI is a
generic client — no per-workspace codegen. `Workspace.artifacts` returns an
`Artifacts` type with chainable filters, dimension discovery, and verb methods
that compile to [Execution Plans](./plans.md).

Artifacts owns the general selector model:
- workspace/module dimensions such as `module`
- synthesized non-collection selector dimensions such as `check`
- later, collection-provided dimensions

### Identity

Artifacts are identified by scope-relative coordinates. There is no separate
public address layer.

- **Artifacts live in selector space.** The noun quality comes from the
  `Artifact` type plus its scope-relative coordinates, not from a separate
  identity layer.
- **Blessed scalar types** (WorkspacePath, HTTPAddress, etc.) carry parsing and
  rendering semantics for filter values, not identity semantics.
- **Collections extend the selector model.** They are not the basis of it.

## Selector Model

Artifacts owns the public selector model. A selector dimension may come from:

1. **Workspace structure** — for example `module`
2. **Non-collection field selectors** — synthesized from ordinary object
   traversal when needed to target verbs generically, for example `check`
3. **Collections** — keyed dimensions added later

Target UX:

- users discover dimensions with `dagger list` and `dagger <verb> --help`
- users target work with repeatable `--<dimension>=<value>` flags
- existing schema-path notation is compat-only; it is not the intended
  steady-state UX

Artifacts has two related scope kinds:

- **Root discovery scope** — `workspace.artifacts`, `dagger list`, and
  `dagger list --help`. This scope is not verb-scoped.
- **Verb scopes** — `workspace.artifacts.filterCheck`,
  `workspace.artifacts.filterGenerate`, and so on. These scopes project the
  root scope through one verb and synthesize the selector space needed for that
  verb's reachable leaves.

The engine should synthesize enough public selector dimensions and values to
target current artifacts/actions without requiring a separate path grammar.

### Example: Non-Collection Disambiguation

Suppose the current check tree contains:

```text
helm:test
helm:lint
typescript-sdk:test-bun
typescript-sdk:test-node-lts
```

Those are not collection items. They still need typed selectors.

CLI discovery:

```bash
$ dagger check --help
  --module=<name>       Filter by module (see: dagger list modules)
  --check=<name>        Filter by check (see: dagger list checks)

$ dagger list checks --module=typescript-sdk
test-bun
test-node-lts

$ dagger check --module=typescript-sdk --check=test-bun
```

API shape:

```text
workspace.artifacts
  .filterCheck
  .dimensions
# => module, check

workspace.artifacts
  .filterCheck
  .filterBy("module", ["typescript-sdk"])
  .filterBy("check", ["test-bun"])
  .check
```

Collections later add more dimensions to the same model; see
[collections.md](./collections.md).

## Selector Algebra

This section defines how selector dimensions are synthesized from ordinary
workspace and module structure.

For root discovery (`workspace.artifacts.dimensions`, `dagger list`,
`dagger list --help`), the raw input is the exposed module tree across the
current workspace modules, with no verb pruning.

For a verb scope such as `workspace.artifacts.filterCheck`, the raw input is
that same tree projected through one verb:

- start from the current workspace modules
- reparent each module under the workspace `module` dimension
- walk only exposed zero-arg members
- keep only leaves reachable by the current verb (`check`, `generate`, etc.)

For a check scope, each reachable leaf has a module-rooted structural path in
CLI case:

```text
hello:passing-check
hello:failing-check
hello:test:lint
hello:test:unit
hello:release:lint
```

### Candidate Dimensions

Each reachable leaf contributes candidate selector pairs:

1. `module=<module-name>`
2. `<verb>=<leaf-name>` such as `check=lint`
3. for each object-valued field/function hop on the path,
   `<field-name>=<selected-child-name>`

Using the tree above:

```text
hello:passing-check  -> module=hello, check=passing-check
hello:test:lint      -> module=hello, check=lint, test=lint
hello:test:unit      -> module=hello, check=unit, test=unit
hello:release:lint   -> module=hello, check=lint, release=lint
```

Rules:

- non-collection field dimensions are named from the field/function name, in
  normal CLI case
- their keys are always `String`
- their values are the selected child segment, also in CLI case
- scalar fields do not create dimensions
- members requiring arguments do not create dimensions

### Synthesis Algorithm

Public dimensions are synthesized per `Artifacts` scope.

1. Start with the explicit root dimension and the verb dimension:
   `module`, `check`, `generate`, `ship`, or `up`.
2. Enumerate all reachable leaves and compute their candidate selector pairs.
3. Group leaves by the conjunction of currently public selector pairs.
4. For each ambiguous group, inspect the shallowest structural divergence:
   - if the group diverges under a shared object-valued field/function, add
     that field as a public dimension
   - if the group diverges by taking different sibling fields/functions, add
     one public dimension for each of those sibling fields/functions
5. Repeat until every reachable leaf is uniquely selectable by a conjunction
   of positive filters.
6. Elide dimensions that never distinguish any leaves in the current scope.
   A one-value dimension may still remain public if that value is the only
   positive selector for some artifacts.

Root discovery uses the unscoped tree. Its public dimensions are the union of:

- root dimensions such as `module`
- dimensions introduced by the current verb scopes, such as `check` or
  `generate`

That lets `dagger list --help` enumerate the selector space without requiring
the user to pick a verb first, while `dagger check --help` and
`dagger generate --help` still show the narrower verb-specific view.

Two examples:

```text
platforms:linux:run
platforms:darwin:run
```

Both checks collide on `check=run`, but they diverge under the shared field
`platforms`, so the public dimensions are:

```text
module, platforms, check
```

and selection is:

```bash
$ dagger check --module=go --platforms=linux --check=run
```

```text
test:lint
release:lint
```

Both checks collide on `check=lint`, and there is no shared selector field
between them. They diverge by taking different sibling fields, so both fields
become public dimensions:

```text
module, check, test, release
```

and selection is:

```bash
$ dagger check --module=hello --test=lint
$ dagger check --module=hello --release=lint
```

### Naming and Qualification

Dimension names are deterministic:

- use the CLI-cased field/function name for non-collection field dimensions
- use the verb name for the terminal verb dimension (`check`, `generate`, ...)
- use `module` for the workspace root dimension

If two synthesized dimensions would have the same public name in one scope,
qualify them with the shortest unique ancestor prefix:

```text
frontend:test:lint
backend:test:lint
```

becomes:

```text
frontend-test, backend-test
```

The same qualification rule applies if a synthesized field dimension would
collide with an existing public dimension such as `module` or `check`.

### Coordinates

`Artifact.coordinates` uses the synthesized public dimensions for the current
scope. Coordinates are not required to be minimal; they must be sufficient to
select the artifact positively within its scope.

Using the earlier example:

```text
hello:test:lint
  -> module=hello, test=lint, check=lint

hello:release:lint
  -> module=hello, release=lint, check=lint
```

Pinned dimensions and trivial singleton dimensions may still be elided from
coordinates when they add no information in the current scope.

### Edge Cases

- If leaf names are already unique in scope, only `module` and the verb
  dimension are public.
- If one branch needs an extra field dimension, that dimension is public for
  the whole scope, not only for the ambiguous leaves.
- A synthesized dimension may have only one value and still remain public if
  it is needed for unique positive selection.
- If a module's structure forces awkward field dimensions, the engine should
  still expose them mechanically. Module authors who want better selector UX
  should rename leaves, insert a named object boundary, or later use a
  collection.
- Collections extend this exact mechanism by adding their own candidate
  dimensions; see [collections.md](./collections.md).

## CLI

One filter model across all commands: `--<dimension>=<value>`, repeatable, no
comma-separated values. Flags are named directly from the public selector
dimension. Each flag points to
`dagger list` for discovery:

```
$ dagger check --help
  --module=<name>       Filter by module (see: dagger list modules)
  --check=<name>        Filter by check (see: dagger list checks)
  --test=<name>         Filter by test subtree (when synthesized)
```

The CLI generates flags from the current Artifacts scope's `dimensions` (for
example `workspace.artifacts.filterCheck.dimensions` for `dagger check`),
parses user input into `filterBy` chains, and calls verb methods or `items` on
the result. Collection-provided dimensions are an extension to this same model;
see [collections.md](./collections.md).

### CLI Mappings

```
dagger list                → workspace.artifacts.dimensions
dagger list --help         → workspace.artifacts.dimensions
dagger list modules        → workspace.artifacts.filterBy("module").items
dagger list checks         → workspace.artifacts
                               .filterCheck
                               .filterBy("check")
                               .items
dagger check --module=go   → workspace.artifacts
                               .filterBy("module", ["go"])
                               .check.run
dagger check --module=typescript-sdk \
  --check=test-bun         → workspace.artifacts
                               .filterCheck
                               .filterBy("module", ["typescript-sdk"])
                               .filterBy("check", ["test-bun"])
                               .check.run
dagger check --module=hello \
  --test=lint              → workspace.artifacts
                               .filterCheck
                               .filterBy("module", ["hello"])
                               .filterBy("test", ["lint"])
                               .check.run
dagger check --module=go \
  --plan                   → workspace.artifacts
                               .filterBy("module", ["go"])
                               .check.nodes  (display the plan)
dagger check --help        → workspace.artifacts.filterCheck.dimensions
```

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
  """Filter name as used in CLI flags. Example: "check"."""
  name: String!

  """Type of this dimension's keys. Determines parsing and rendering."""
  keyType: TypeDef!
}

"""One artifact in the workspace."""
type Artifact {
  """
  Ordered coordinate path from the outermost dimension in scope to this
  artifact. The last element is the artifact's own position; preceding
  elements trace the path through enclosing dimensions.

  Coordinates are scope-relative: which dimensions appear depends on the
  current Artifacts scope. Coordinates are not required to be minimal, but
  they must be sufficient to select the artifact positively within that scope.
  A dimension that is pinned by filters or trivial (only one value exists) may
  be elided. Coordinates are guaranteed unique within the artifact's scope,
  but not necessarily unique in a broader scope with fewer filters applied.
  """
  coordinates: [ArtifactCoordinate!]!

  """
  The Artifacts scope that produced this artifact. Coordinates are unique
  within this scope. Use to navigate back to siblings, inspect which
  dimensions and filters are active, or further narrow the view.
  """
  scope: Artifacts!

  """Fields on the underlying object, for inspection."""
  fields: [FieldValue!]!

  """Available function names on the underlying object."""
  actions: [String!]!

  """Create an action targeting this single artifact."""
  action(name: String!): Action!
}

"""A position along one dimension of the artifact graph."""
type ArtifactCoordinate {
  """Which dimension. Example: "check"."""
  dimension: String!

  """The key value, rendered per its key type. Example: "test-bun"."""
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

## Notes

- **Static (schema-only, no runtime cost):** root dimensions, verb-scoped
  dimensions, action names, filter flag generation
- **Dynamic (reads selector values):** items, item counts, selector value
  discovery, plan compilation, plan execution

### Filter Algebra

- Every chained `filterBy` / `filterCheck` / etc. is **AND**
- Multiple values within one `filterBy` call is **OR** (list of values)
- `filterBy("X")` (no values) → select all items of dimension X
- `filterBy("X", ["a"]).filterBy("X", ["b"])` → intersection (AND) — empty if
  no overlap
- `filterCheck` / `filterShip` / etc. → narrow to verb-reachable paths

## Initial Scope

Pre-Collections, the initial public selector dimensions are not just `module`.

- `workspace.artifacts.dimensions` returns `module` plus the union of public
  dimensions contributed by current verb scopes
- `workspace.artifacts.filterCheck.dimensions` returns `module`, `check`, and
  any synthesized non-collection field dimensions needed for unique positive
  selection
- `workspace.artifacts.filterGenerate.dimensions` returns `module`,
  `generate`, and any synthesized non-collection field dimensions needed for
  generators
- `Artifacts.items` returns artifacts in the current selector scope, not only
  top-level modules

Concretely:

```bash
$ dagger list checks --module=typescript-sdk
test-bun
test-node-lts

$ dagger check --module=typescript-sdk --check=test-bun
```

Collections later add additional selector dimensions on top of this base; see
[collections.md](./collections.md).

## Implementation Notes

Initial implementation should keep the selector model and the CLI surface
aligned:

- wire `Workspace.artifacts` in the engine schema
- compute root discovery dimensions from the unscoped module tree
- compute verb-scoped reachable leaves from the existing module tree
- synthesize public dimensions from the reachable leaves using the algorithm
  above
- generate CLI flags from the current scope's `dimensions`
- migrate `dagger check` to `workspace.artifacts.check.run`
- expose `--module`, `--check`, and `--plan`
- expose `dagger list` / `dagger list checks`
- if existing schema-path args remain temporarily, keep them as a thin alias
  only; do not shape new APIs around them

## Locked Decisions

- **`--mod` and `--module` coexist with distinct long forms.** `--mod` is the
  global module-loader flag; `--module` is the artifact dimension filter. No
  rename.

## Open Questions

1. How cross-artifact reference ordering interacts with the filter system.
