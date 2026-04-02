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
- [Implementation](#implementation)
- [Initial Scope](#initial-scope)
- [Implementation Notes](#implementation-notes)
- [Open Questions](#open-questions)

## Problem

1. **Selection is fragmented** - Today, targeting is split across schema-path
   notation, command-specific behavior, and whatever structure happens to exist
   in a module.
2. **Ordinary object structure still needs disambiguation** - A module can
   expose multiple targetable object artifacts even before collections exist.
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
- `Artifacts.actions` and `Artifact.actions` describe direct non-traversal
  function names
- verb scopes such as `filterCheck()` narrow which artifacts and actions are in
  play, but they do not introduce new artifact dimensions on their own
- verb methods such as `Artifacts.check` compile from the selected artifact
  set; they do not derive their target set from `Artifacts.actions`

The client model is table-shaped:

- `Artifacts.dimensions` is the ordered header row
- `Artifact.coordinates` is the ordered value row for one item
- `Artifact.coordinate(name)` is a convenience lookup into that row

`dimensions()` is metadata only. To enumerate values in one dimension, clients
enumerate `items()` in the current scope and project that dimension's
coordinate value.

A dimension must be listed from the same `Artifacts` scope that surfaced it.
Root discovery uses `workspace.artifacts`; verb-local discovery uses the
corresponding verb-projected `dagger list` scope such as
`workspace.artifacts.filterCheck`.

If a future CLI wants action-name filtering such as `--check=<name>`, that
should layer on top of artifact selection and action discovery. It is not part
of the artifact selector algebra.

The engine should synthesize enough public selector dimensions to make current
artifact objects positively selectable without a separate path grammar.

### Example: Non-Collection Disambiguation

Suppose a module exposes:

```graphql
type Go {
  platforms: Platforms!
}

type Platforms {
  linux: Platform!
  darwin: Platform!
}

type Platform {
  run: Void! @check
}
```

The public artifact dimension is:

```text
platform
```

with values:

```text
linux
darwin
```

CLI discovery:

```bash
$ dagger check --help
  --module=<name>       Filter by module
  --platform=<name>     Filter by platform

$ dagger list platform --check --module=go
linux
darwin

$ dagger check --module=go --platform=linux
```

API shape:

```text
scope = workspace.artifacts
  .filterCheck

scope.dimensions
# => [module, platform]

scope
  .filterBy("module", ["go"])
  .filterBy("platform")
  .items
# => rows whose "platform" coordinate is linux or darwin

scope
  .filterBy("module", ["go"])
  .filterBy("platform", ["linux"])
  .items[0].coordinate("platform")
# => linux
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
- prune away every subtree that cannot reach at least one handler for the
  current verb (`check`, `generate`, etc.)

The projected tree is the input universe for selector synthesis. Not every
projected object becomes an artifact:

- the workspace-reparented module root is always an artifact
- a non-root object that directly exposes at least one direct action in the
  current scope is an artifact candidate
- a grouping object remains structural glue unless the selector algebra needs a
  public dimension at that point
- once a branching point is promoted to a public dimension, the selected child
  subtree roots become artifacts in that scope even if they have no direct
  actions of their own

A public dimension corresponds to one promoted branching point in the object
graph. The grouping object at that branching point is structural glue; the
selected child objects are the new artifact boundary.

For example, in the `Go -> Platforms -> Platform` example above:

- `Go` is an artifact
- `Platforms` is structural glue
- `Platform(linux)` and `Platform(darwin)` are artifacts

For a check scope, the retained artifact rows might look like:

```text
module=hello
module=hello, stage=test
module=hello, stage=release
module=go, platform=linux
module=go, platform=darwin
```

### Candidate Dimensions

Each reachable artifact object contributes candidate selector pairs:

1. `module=<module-name>`
2. for each promoted branching point on its path,
   `<dimension-name>=<selected-child-name>`

Using the tree above:

```text
hello root artifact             -> module=hello
hello test artifact             -> module=hello, stage=test
hello release artifact          -> module=hello, stage=release
go linux platform artifact      -> module=go, platform=linux
go darwin platform artifact     -> module=go, platform=darwin
```

Rules:

- zero-arg functions returning objects participate the same way as fields
- if one branching point selects among child artifacts that all share the same
  target artifact type `T`, the preferred public dimension name is the
  CLI-cased type name of `T`; type names should already be singular by
  convention (`Platform` → `platform`)
- otherwise, fall back to one qualified singleton dimension per selected child
  field/function name; its only non-null coordinate value is that same name
- their keys are always `String`
- their values are the selected child segment, also in CLI case
- scalar fields do not create dimensions
- members requiring arguments do not create dimensions
- actions such as `lint` or `unit` do not create artifact dimensions

### Synthesis Algorithm

Public dimensions are synthesized per `Artifacts` scope.

1. Start with the explicit root dimension: `module`.
2. Enumerate the projected artifact candidates in the current scope:
   - module roots
   - projected non-root objects with direct actions in this scope
   - any child subtree roots added by earlier promotion steps
3. Group those artifact candidates by the conjunction of currently public
   coordinate cells.
4. For each ambiguous group, find the shallowest branching point that
   separates its members.
5. Promote that branching point:
   - if its selected child artifacts are homogeneous, add one public
     dimension named from the shared child artifact type, with child names as
     values
   - otherwise, add one qualified singleton dimension per selected child
     field/function name
6. Repeat until every projected artifact candidate has a unique coordinate row
   in the current scope.
7. Keep promoted dimensions public for the whole scope, even if some artifact
   rows have `null` in that column.

In pseudocode:

```text
public := [module]
artifacts := moduleRoots + directActionObjects
repeat
  groups := groupByCoordinateRow(artifacts, public)
  if every group has size 1:
    break
  for each ambiguous group:
    bp := shallowestBranchingPoint(group)
    public += dimensionsFor(bp)
    artifacts += selectedChildRoots(bp)
until fixed point
```

`dimensionsFor(bp)` is the naming rule above: one shared typed dimension for a
homogeneous branch, otherwise qualified singleton dimensions. A one-value
dimension may still remain public if that value is the only positive selector
for some artifacts.

Root discovery uses the unscoped object tree and exposes only artifact
dimensions valid in that root scope. `dagger list --help` should enumerate
artifact selection axes only. Verb-specific help such as `dagger check --help`
shows the artifact dimensions valid for that verb-projected scope.

Two examples:

```text
module=go, platform=linux
module=go, platform=darwin
```

Both artifact rows collide on `module=go`, but they diverge under one
shared branching point, so the public dimensions are:

```text
module, platform
```

and selection is:

```bash
$ dagger check --module=go --platform=linux
```

```text
module=hello, stage=test
module=hello, stage=release
```

Both artifact rows collide on `module=hello`, and there is no shared
selector field between them. They diverge by taking different sibling fields,
but both children share the same target type `Stage`, so the public dimension
is:

```text
module, stage
```

Formal selector pairs are:

```text
hello test artifact    -> module=hello, stage=test
hello release artifact -> module=hello, stage=release
```

### Naming and Qualification

Dimension names are deterministic:

- use the CLI-cased target artifact type name when one branching point selects
  among homogeneous child artifact types; type names should already be singular
  by convention (`Platform` → `platform`)
- otherwise, use the qualified child field/function name itself as a singleton
  dimension
- use `module` for the workspace root dimension

If two synthesized dimensions would have the same public name in one scope,
qualify them with ancestor prefixes, nearest first:

```text
frontend:test
backend:test
```

may become:

```text
frontend-stage, backend-stage
```

Prepend successive ancestor names in CLI case until the name is unique in the
current scope. If exhausting ancestors still collides, append a deterministic
numeric suffix.

The same rule applies if a synthesized field dimension would collide with an
existing public dimension such as `module`.

Singleton fallback dimensions are mechanically correct, but not ideal UX. If a
module shape produces awkward dimensions, authors should rename leaves, insert
a grouping object, or later use a collection.

### Coordinates

`Artifact.coordinates` is the full coordinate row for the synthesized public
dimensions in the current scope. It is not a standalone address. Clients should
read it the same way they read one row in a table.

Using the earlier example:

```text
hello test artifact    -> module=hello, stage=test
hello release artifact -> module=hello, stage=release
```

In a check scope with `dimensions = [module, platform]`:

```text
go root artifact             -> ["go", null]
go linux platform artifact   -> ["go", "linux"]
go darwin platform artifact  -> ["go", "darwin"]
```

Coordinates follow a strict table contract:

- `scope.dimensions` is ordered and stable for a given scope
- `artifact.coordinates` has the same length and order
- `artifact.coordinates[i]` corresponds to `scope.dimensions[i]`
- `null` means that dimension is not applicable for this artifact in this
  scope
- after `filterBy("X")`, every returned artifact must have a non-null
  coordinate for `X`

### Edge Cases

- If artifact objects are already unique in scope, only `module` may be
  public.
- If one branch needs an extra field dimension, that dimension is public for
  the whole scope, not only for the ambiguous artifacts.
- A branching point with only one selected child in scope may still be
  promoted if it is the only positive way to separate a parent artifact from
  that child subtree.
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
dimension.

```
$ dagger check --help
  --module=<name>       Filter by module
  --platform=<name>     Filter by platform (when synthesized)
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

Root and verb-local discovery both use `dagger list`. Verb-local discovery
projects `dagger list` through one verb first, for example `dagger list
<dimension> --check`. Value listing is derived from `items()`, not from
`dimensions()`:

1. call `filterBy("<dimension>").items()`
2. read `coordinate("<dimension>")` from each returned artifact
3. deduplicate and sort the distinct non-null values for display

This keeps `dimensions()` static while leaving value enumeration to the scoped
artifact set.

The important rule is scope matching: if the dimension came from
`workspace.artifacts.filterCheck.dimensions`, value listing must query
`workspace.artifacts.filterCheck.filterBy("<dimension>").items()`, not the root
scope. `dagger list <dimension> --check` is the CLI surface for that scope.

Implementation sketch:

```go
rows := scope.FilterBy("platform", nil).Items()
values := Distinct(NonNil(Map(rows, func(a Artifact) string {
	return a.Coordinate("platform")
})))
```

### CLI Mappings

```
dagger list                → workspace.artifacts.dimensions
dagger list --help         → workspace.artifacts.dimensions
dagger list --check        → workspace.artifacts
                               .filterCheck
                               .dimensions
dagger list module         → workspace.artifacts
                               .filterBy("module")
                               .items
                               # then print distinct coordinate("module")
dagger list platform       → workspace.artifacts
                               .filterBy("platform")
                               .items
                               # then print distinct coordinate("platform")
dagger list platform --check
  --module=go              → workspace.artifacts
                               .filterCheck
                               .filterBy("module", ["go"])
                               .filterBy("platform")
                               .items
                               # then print sorted distinct coordinate("platform")
dagger check --module=go \
  --platform=linux         → workspace.artifacts
                               .filterCheck
                               .filterBy("module", ["go"])
                               .filterBy("platform", ["linux"])
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
  """
  Narrow by a dimension, optionally to specific values. With no values,
  selects artifacts whose coordinate row has a non-null cell for that
  dimension.
  """
  filterBy(dimension: String!, values: [String!]): Artifacts!

  """
  Narrow to artifacts whose subtree contains at least one check handler.
  Does not add `check` as an artifact dimension.
  """
  filterCheck: Artifacts!

  """
  Narrow to artifacts whose subtree contains at least one generate handler.
  Does not add `generate` as an artifact dimension.
  """
  filterGenerate: Artifacts!

  """
  Narrow to artifacts whose subtree contains at least one ship handler.
  Does not add `ship` as an artifact dimension.
  """
  filterShip: Artifacts!

  """
  Narrow to artifacts whose subtree contains at least one up handler.
  Does not add `up` as an artifact dimension.
  """
  filterUp: Artifacts!

  """Ordered filterable dimensions for the current scope. Static header row."""
  dimensions: [ArtifactDimension!]!

  """Artifacts matching the current filters. Dynamic row set."""
  items: [Artifact!]!

  """
  Union of direct non-traversal function names across all in-scope artifacts.
  Zero-arg object-returning members participate in selector traversal instead.
  """
  actions: [String!]!

  """
  Create an action targeting the in-scope artifacts that directly expose this
  function name. Errors if no in-scope artifact exposes it.
  """
  action(name: String!): Action!

  """
  Compile a check execution plan for the selected artifact set. Verb-specific
  expansion and ordering rules live in `plans.md`; overlapping ancestor/child
  selections are deduplicated at the reachable-handler set before actions are
  constructed.
  """
  check: Plan!

  """Compile a generate execution plan for the selected artifact set."""
  generate: Plan!

  """Compile a ship execution plan for the selected artifact set."""
  ship: Plan!

  """Compile an up execution plan for the selected artifact set."""
  up: Plan!
}

"""A filterable axis of the artifact graph."""
type ArtifactDimension {
  """Filter name as used in CLI flags and table headers. Example: "platform"."""
  name: String!

  """
  Type of this dimension's keys. Determines parsing, validation, and help
  rendering. It does not enumerate the current in-scope values.
  """
  keyType: TypeDef!
}

"""One artifact in the workspace."""
type Artifact {
  """
  Ordered coordinate row for this artifact. Same length and order as
  `scope.dimensions`.

  `coordinates[i]` corresponds to `scope.dimensions[i]`. A null cell means the
  dimension does not apply to this artifact in the current scope.

  Coordinates describe artifact selection only. Action names remain separate.
  Each cell is the public string form that the CLI would round-trip through
  `--<dimension>=<value>`.
  """
  coordinates: [String]!

  """Convenience lookup for one coordinate by dimension name."""
  coordinate(name: String!): String

  """
  The Artifacts scope that produced this artifact. Coordinates are unique
  within this scope. Use to navigate back to siblings, inspect which
  dimensions and filters are active, or further narrow the view.
  """
  scope: Artifacts!

  """Fields on the underlying object, for inspection."""
  fields: [FieldValue!]!

  """Available direct non-traversal function names on the underlying object."""
  actions: [String!]!

  """Create an action targeting this single artifact."""
  action(name: String!): Action!
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

- **Static synthesis (no user-object evaluation):** root dimensions,
  verb-scoped artifact dimensions, action names, filter flag generation
- **Dynamic enumeration:** items, item counts, coordinate values in the current
  scope, collection-derived selector value discovery, plan compilation, plan
  execution

Non-collection selector synthesis is static. It should require only workspace
module discovery plus schema/object introspection; it should not require
evaluating user objects.

For ordinary zero-arg object-returning members, selector synthesis uses the
declared member name and return type only. It does not execute the function
body to discover rows or dimensions.

In this document, a "direct action" means a callable non-object function on
the current object. Zero-arg object-returning members are traversal edges, not
actions.

`items()` is the one enumeration surface. An implementation may satisfy it
statically for ordinary object-structure scopes and dynamically for
collection-backed scopes, but the API contract is the same.

Root discovery and verb scopes may expose different dimensions. That is
expected: `workspace.artifacts` shows the full root selector space, while
`filterCheck` / `filterGenerate` prune that space to artifact objects relevant
to one verb.

Artifact selection and verb compilation are separate layers. `Artifacts.items`
and `Artifacts.actions` describe the selected artifact set. `Artifacts.check`,
`generate`, `ship`, and `up` compile plans from that selected set according to
the verb-specific rules in [plans.md](./plans.md).

For subtree-based verbs, overlap is resolved at the reachable-handler set, not
at the coordinate-row level. Selecting both an ancestor artifact and one of its
descendants should not duplicate the same direct handler in the compiled plan.

### Filter Algebra

- Every chained `filterBy` / `filterCheck` / etc. is **AND**
- Multiple values within one `filterBy` call is **OR** (list of values)
- `filterBy("X")` (no values) → select all items where dimension `X` is
  present in the current scope
- for a singleton dimension, `filterBy("X")` and `filterBy("X", ["X"])` are
  equivalent
- `filterBy("X", ["a"]).filterBy("X", ["b"])` → intersection (AND) — empty if
  no overlap
- `filterCheck` / `filterShip` / etc. → narrow to artifact objects relevant to
  that verb

## Implementation

This design is intended to land as one primary implementation unit:

- **PR:** `artifacts plumbing: selector scopes and rows`
- **API:** `Workspace.artifacts`, `Artifacts`, `Artifact`, `ArtifactDimension`,
  `FieldValue`
- **Contract:** static `dimensions`, dynamic `items`, row-shaped
  `coordinates`, root and verb-projected selector scopes

Included in this unit:

- selector synthesis and coordinate rows
- `filterBy`, `filterCheck`, `filterGenerate`, `filterShip`, `filterUp`
- `Artifact.coordinate(name)` and field inspection
- enough CLI plumbing to drive selector scopes internally

Deferred to [plans.md](./plans.md):

- `Action` and `Plan`
- `Artifacts.check` / `generate` / `ship` / `up`
- the public `dagger list` / typed-filter UX
- removal of the old check / generate execution path

### Pull Request Description

```text
This PR implements the Artifacts design unit. It adds `Workspace.artifacts`,
`Artifacts`, `Artifact`, `ArtifactDimension`, and `FieldValue`, together with
selector synthesis, scope-relative coordinate rows, and verb-projected selector
scopes. It lands the selector model itself, but defers the public `dagger list`
UX and verb execution to Execution Plans.
```

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
- clients derive `dagger list <dimension>` by projecting one coordinate column
  out of `filterBy("<dimension>").items()` on the same scope that exposed the
  dimension

Concretely:

```bash
$ dagger list platform --check --module=go
linux
darwin

$ dagger check --module=go --platform=linux
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
- implement `Artifact.coordinate(name)` as named lookup into the row defined by
  `scope.dimensions` and `coordinates`
- implement `dagger list <dimension>` by projecting one coordinate column out
  of `filterBy("<dimension>").items()` in the same scope that exposed the
  dimension
- expose verb-local value discovery as verb-projected `dagger list`
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
