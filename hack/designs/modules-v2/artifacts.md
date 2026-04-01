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
- synthesized non-collection object-field selector dimensions
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
2. **Non-collection object-field selectors** — synthesized from ordinary
   object traversal when needed to keep distinct artifact objects targetable
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
  verb's reachable artifact objects.

Selector dimensions choose **artifacts**. Actions remain separate:

- `Artifacts.dimensions` and `Artifact.coordinates` describe object selection
- `Artifacts.actions` and `Artifact.actions` describe callable function names
- verb scopes such as `filterCheck()` narrow which artifacts and actions are in
  play, but they do not introduce new artifact dimensions on their own

If a future CLI wants action-name filtering such as `--check=<name>`, that
should layer on top of artifact selection and action discovery. It is not part
of the artifact selector algebra.

The engine should synthesize enough public selector dimensions and values to
target current artifact objects without requiring a separate path grammar.

### Example: Non-Collection Disambiguation

Suppose the current object tree contains:

```text
go:platforms:linux
go:platforms:darwin
```

and the selected artifacts expose actions:

```text
go:platforms:linux   -> run
go:platforms:darwin  -> run
```

Those are not collection items. The artifact objects still need typed
selectors.

CLI discovery:

```bash
$ dagger check --help
  --module=<name>       Filter by module (see: dagger list modules)
  --platforms=<name>    Filter by platform (see: dagger list platforms)

$ dagger list platforms --module=go
linux
darwin

$ dagger check --module=go --platforms=linux
```

API shape:

```text
workspace.artifacts
  .filterCheck
  .dimensions
# => module, platforms

workspace.artifacts
  .filterCheck
  .filterBy("module", ["go"])
  .filterBy("platforms", ["linux"])
  .check

workspace.artifacts
  .filterCheck
  .filterBy("module", ["go"])
  .filterBy("platforms", ["linux"])
  .actions
# => run
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
- walk only exposed zero-arg object-valued members
- keep only artifact objects that expose or can reach the current verb
  (`check`, `generate`, etc.)

For a check scope, each reachable artifact object has a module-rooted
structural path in CLI case:

```text
hello
hello:test
hello:release
go:platforms:linux
go:platforms:darwin
```

### Candidate Dimensions

Each reachable artifact object contributes candidate selector pairs:

1. `module=<module-name>`
2. for each object-valued field/function hop on the path,
   `<field-name>=<selected-child-name>`

Using the tree above:

```text
hello                -> module=hello
hello:test           -> module=hello, test=test
hello:release        -> module=hello, release=release
go:platforms:linux   -> module=go, platforms=linux
go:platforms:darwin  -> module=go, platforms=darwin
```

Rules:

- non-collection field dimensions are named from the field/function name, in
  normal CLI case
- their keys are always `String`
- their values are the selected child segment, also in CLI case
- scalar fields do not create dimensions
- members requiring arguments do not create dimensions
- actions such as `lint` or `unit` do not create artifact dimensions

### Synthesis Algorithm

Public dimensions are synthesized per `Artifacts` scope.

1. Start with the explicit root dimension: `module`.
2. Enumerate all reachable artifact objects and compute their candidate
   selector pairs.
3. Group artifact objects by the conjunction of currently public selector
   pairs.
4. For each ambiguous group, inspect the shallowest structural divergence:
   - if the group diverges under a shared object-valued field/function, add
     that field as a public dimension
   - if the group diverges by taking different sibling fields/functions, add
     one public dimension for each of those sibling fields/functions
5. Repeat until every reachable artifact object is uniquely selectable by a
   conjunction of positive filters.
6. Elide dimensions that never distinguish any artifact objects in the current
   scope.
   A one-value dimension may still remain public if that value is the only
   positive selector for some artifacts.

Root discovery uses the unscoped object tree and exposes only artifact
dimensions valid in that root scope. `dagger list --help` should enumerate
artifact selection axes only. Verb-specific help such as `dagger check --help`
shows the artifact dimensions valid for that verb-projected scope.

Two examples:

```text
platforms:linux
platforms:darwin
```

Both artifact objects collide on `module=go`, but they diverge under the
shared field `platforms`, so the public dimensions are:

```text
module, platforms
```

and selection is:

```bash
$ dagger check --module=go --platforms=linux
```

```text
test
release
```

Both artifact objects collide on `module=hello`, and there is no shared
selector field between them. They diverge by taking different sibling fields,
so both fields become public dimensions:

```text
module, test, release
```

Formal selector pairs are:

```text
hello:test    -> module=hello, test=test
hello:release -> module=hello, release=release
```

### Naming and Qualification

Dimension names are deterministic:

- use the CLI-cased field/function name for non-collection field dimensions
- use `module` for the workspace root dimension

If two synthesized dimensions would have the same public name in one scope,
qualify them with the shortest unique ancestor prefix:

```text
frontend:test
backend:test
```

becomes:

```text
frontend-test, backend-test
```

The same qualification rule applies if a synthesized field dimension would
collide with an existing public dimension such as `module`.

### Coordinates

`Artifact.coordinates` uses the synthesized public dimensions for the current
scope. Coordinates are not required to be minimal; they must be sufficient to
select the artifact positively within its scope.

Using the earlier example:

```text
hello:test
  -> module=hello, test=test

hello:release
  -> module=hello, release=release
```

Pinned dimensions and trivial singleton dimensions may still be elided from
coordinates when they add no information in the current scope.

### Edge Cases

- If artifact objects are already unique in scope, only `module` may be
  public.
- If one branch needs an extra field dimension, that dimension is public for
  the whole scope, not only for the ambiguous artifacts.
- A synthesized dimension may have only one value and still remain public if
  it is needed for unique positive selection.
- Actions available on the selected artifacts are discovered through
  `Artifacts.actions` / `Artifact.actions`. They do not become artifact
  dimensions.
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
  --platforms=<name>    Filter by platform (when synthesized)
```

The CLI generates flags from the current Artifacts scope's `dimensions` (for
example `workspace.artifacts.filterCheck.dimensions` for `dagger check`),
parses user input into `filterBy` chains, and calls verb methods or `items` on
the result.

Action names come from `Artifacts.actions` / `Artifact.actions`, not from
`Artifacts.dimensions`. If verb-specific action-name filters are added later,
they should be described as plan/action filtering rather than artifact
selection.

Collection-provided dimensions are an extension to this same model; see
[collections.md](./collections.md).

### CLI Mappings

```
dagger list                → workspace.artifacts.dimensions
dagger list --help         → workspace.artifacts.dimensions
dagger list modules        → workspace.artifacts.filterBy("module").items
dagger list platforms      → workspace.artifacts
                               .filterBy("platforms")
                               .items
dagger check --module=go \
  --platforms=linux        → workspace.artifacts
                               .filterCheck
                               .filterBy("module", ["go"])
                               .filterBy("platforms", ["linux"])
                               .check.run
dagger check --module=go \
  --plan                   → workspace.artifacts
                               .filterCheck
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

  """Narrow to artifacts that expose or can reach check handlers."""
  filterCheck: Artifacts!

  """Narrow to artifacts that expose or can reach generate handlers."""
  filterGenerate: Artifacts!

  """Narrow to artifacts that expose or can reach ship handlers."""
  filterShip: Artifacts!

  """Narrow to artifacts that expose or can reach up handlers."""
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
  """Filter name as used in CLI flags. Example: "platforms"."""
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

  Coordinates describe artifact selection only. Action names remain separate.
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
  """Which dimension. Example: "platforms"."""
  dimension: String!

  """The key value, rendered per its key type. Example: "linux"."""
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
  artifact dimensions, action names, filter flag generation
- **Dynamic:** items, item counts, collection-derived selector value discovery,
  plan compilation, plan execution

Non-collection selector synthesis is static. It should require only workspace
module discovery plus schema/object introspection; it should not require
evaluating user objects.

### Filter Algebra

- Every chained `filterBy` / `filterCheck` / etc. is **AND**
- Multiple values within one `filterBy` call is **OR** (list of values)
- `filterBy("X")` (no values) → select all items of dimension X
- `filterBy("X", ["a"]).filterBy("X", ["b"])` → intersection (AND) — empty if
  no overlap
- `filterCheck` / `filterShip` / etc. → narrow to artifact objects relevant to
  that verb

## Initial Scope

Pre-Collections, the initial public selector dimensions are not just `module`.

- `workspace.artifacts.dimensions` returns `module` plus any synthesized
  non-collection object-field dimensions needed to distinguish artifact
  objects in root scope
- `workspace.artifacts.filterCheck.dimensions` returns the artifact dimensions
  valid in the check-projected scope; it does not add `check` as a dimension
- `workspace.artifacts.filterGenerate.dimensions` returns the artifact
  dimensions valid in the generate-projected scope; it does not add
  `generate` as a dimension
- `Artifacts.items` returns object artifacts in the current selector scope,
  not action leaves
- `Artifacts.actions` returns available function names separately from
  `dimensions`

Concretely:

```bash
$ dagger list platforms --module=go
linux
darwin

$ dagger check --module=go --platforms=linux
```

Collections later add additional selector dimensions on top of this base; see
[collections.md](./collections.md).

## Implementation Notes

Initial implementation should keep the selector model and the CLI surface
aligned:

- wire `Workspace.artifacts` in the engine schema
- compute root discovery dimensions from the unscoped module tree
- compute verb-scoped artifact objects from the existing module tree
- synthesize public artifact dimensions from the reachable object graph using
  the algorithm above
- generate CLI flags from the current scope's `dimensions`
- migrate `dagger check` to `workspace.artifacts.check.run`
- expose `--module`, any synthesized object-field filters, and `--plan`
- expose `dagger list` over artifact dimensions
- if existing schema-path args remain temporarily, keep them as a thin alias
  only; do not shape new APIs around them

## Locked Decisions

- **`--mod` and `--module` coexist with distinct long forms.** `--mod` is the
  global module-loader flag; `--module` is the artifact dimension filter. No
  rename.

## Open Questions

1. How cross-artifact reference ordering interacts with the filter system.
