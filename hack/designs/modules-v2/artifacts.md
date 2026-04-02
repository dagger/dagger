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
`Artifacts` type with chainable filters, dimension discovery, and
scope-relative coordinate rows.

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
- users target selector dimensions with repeatable `--<dimension>=<value>`
  flags
- existing schema-path notation is compat-only; it is not the intended
  steady-state UX

Artifacts has two related scope kinds:

- **Root discovery scope** — `workspace.artifacts`, `dagger list`, and
  `dagger list --help`. This scope is not verb-scoped.
- **Verb scopes** — `workspace.artifacts.filterCheck`,
  `workspace.artifacts.filterGenerate`, and so on. These scopes project the
  current structural scope through one or more accumulated verb predicates and
  synthesize the selector space needed for the retained artifact objects.

Selector dimensions choose **artifacts** only:

- `Artifacts.dimensions` and `Artifact.coordinates` describe object selection
- verb scopes such as `filterCheck()` narrow which artifact rows are in play,
  but they do not introduce new artifact dimensions on their own

The client model is table-shaped:

- `Artifacts.dimensions` is the ordered header row
- `Artifact.coordinates` is the ordered value row for one item
- `Artifact.coordinate(name)` is a convenience lookup into that row

`dimensions()` is metadata only. To enumerate values in one dimension, clients
enumerate `items()` in the current scope and project that dimension's
coordinate value.

The CLI contract described in this document belongs to the Artifacts model.
The Artifacts implementation unit may keep that surface hidden initially, but
it must already produce the scopes, dimensions, and rows that the CLI will
eventually expose.

A dimension must be listed from the same `Artifacts` scope that surfaced it.
Root discovery uses `workspace.artifacts`; verb-local discovery uses the
corresponding verb-projected `dagger list` scope such as
`workspace.artifacts.filterCheck`.

The engine should synthesize enough public selector dimensions to make current
artifact objects positively selectable without a separate path grammar.

For one verb `V`, a projection retains the object nodes whose subtree contains
at least one direct `V` member, plus the ancestor path to those nodes.
Descendants therefore compose with **OR** inside a single verb projection.

Chaining verb projections accumulates predicates over the same underlying
structural tree. A node remains in scope only if its subtree satisfies every
active verb predicate. Chained verb projections therefore compose with **AND**,
are order-independent, and are idempotent:

```text
filterCheck().filterGenerate() == filterGenerate().filterCheck()
filterCheck().filterCheck() == filterCheck()
```

Verb projection is structural only. It does not follow cross-artifact
references and it does not compile or execute anything.

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
  os: String!
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
$ dagger list --help
  --module=<name>       Filter by module
  --platform=<name>     Filter by platform

$ dagger list platform --module=go
linux
darwin
```

API shape:

```text
scope = workspace.artifacts

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

This section defines how selector dimensions are synthesized from the object
tree already present in the current `Artifacts` scope.

For root discovery (`workspace.artifacts.dimensions`, `dagger list`,
`dagger list --help`), that tree is the exposed module tree across the current
workspace modules.

For a verb scope, the scoped tree is derived from that same structural tree by
applying the accumulated verb predicates:

```text
hasVerb(node, V) =
  hasDirectVerbMember(node, V)
  OR any(hasVerb(child, V) for child in objectChildren(node))

retain(node, activeVerbs) =
  all(hasVerb(node, V) for V in activeVerbs)
```

Only zero-arg object-valued members participate in `objectChildren`. Verb
projection never follows cross-artifact references.

This means:

- one verb projection uses **OR** over descendants
- chained verb projections use **AND** across verbs
- composition is evaluated over the same underlying tree, not by re-pruning an
  already-pruned intermediate result

For example:

```text
module
├── test   # subtree contains a check
└── sdk    # subtree contains a generate
```

Then:

```text
workspace.artifacts.filterCheck.items                  => [module, test]
workspace.artifacts.filterGenerate.items               => [module, sdk]
workspace.artifacts.filterCheck.filterGenerate.items   => [module]
```

Not every scoped object becomes a public artifact row:

- the workspace-reparented module root is always an artifact
- a retained non-root leaf object is an artifact candidate
- grouping objects remain structural glue unless the selector algebra needs a
  public dimension at that point
- once a branching point is promoted to a public dimension, the selected child
  subtree roots become artifacts in that scope

Here, a retained leaf object means an object node in the current scope with no
retained object-valued children beneath it.

A public dimension corresponds to one promoted branching point in the object
graph. The grouping object at that branching point is structural glue; the
selected child objects are the new artifact boundary.

For example, in the `Go -> Platforms -> Platform` example above:

- `Go` is an artifact
- `Platforms` is structural glue
- `Platform(linux)` and `Platform(darwin)` are artifacts

In one scope, the retained artifact rows might look like:

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
- non-object members do not create artifact dimensions

### Synthesis Algorithm

Public dimensions are synthesized per `Artifacts` scope.

1. Start with the explicit root dimension: `module`.
2. Enumerate the projected artifact candidates in the current scope:
   - module roots
   - retained non-root leaf objects
   - any child subtree roots added by earlier promotion steps
3. Group those artifact candidates by the conjunction of currently public
   coordinate cells.
4. For each ambiguous group, find the shallowest branching point that
   separates its members, or the shallowest retained child edge needed to
   separate a parent artifact from its only ambiguous child subtree.
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
artifacts := moduleRoots + retainedLeafObjects
repeat
  groups := groupByCoordinateRow(artifacts, public)
  if every group has size 1:
    break
  for each ambiguous group:
    bp := shallowestPromotablePoint(group)
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
$ dagger list platform --module=go
linux
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

`scope.dimensions` is ordered deterministically:

- `module` is always first
- remaining dimensions appear in first-promotion order during a stable preorder
  walk of the scoped object tree
- if one promotion step emits multiple dimensions, order them by their final
  public name after qualification
- if qualification still collides, assign numeric suffixes in ascending order
  after that same final-name sort

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

In a scope with `dimensions = [module, platform]`:

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
- If a module's structure forces awkward field dimensions, the engine should
  still expose them mechanically. Module authors who want better selector UX
  should rename leaves, insert a named object boundary, or later use a
  collection.
- Collections extend this exact mechanism by adding their own candidate
  dimensions; see [collections.md](./collections.md).

## CLI

Dimension filters use `--<dimension>=<value>`, repeatable, with no
comma-separated values. Flags are named directly from the public selector
dimension.

```
$ dagger list --help
  --module=<name>       Filter by module
  --platform=<name>     Filter by platform (when synthesized)
```

The CLI generates flags from the current Artifacts scope's `dimensions` (for
example `workspace.artifacts.filterCheck.dimensions` for `dagger check`) and
parses user input into `filterBy` chains over that scope.

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
  dimension. Errors if the dimension is not present in the current scope.
  Preserves the current scope's dimension order and narrows only the row set.
  Values are parsed and validated according to that dimension's `keyType`.
  An empty values list is invalid; omit `values` for presence filtering.
  """
  filterBy(dimension: String!, values: [String!]): Artifacts!

  """
  Project this scope through the `check` verb.
  Does not add `check` as an artifact dimension.
  Returns a new scope with dimensions re-synthesized for that projection.
  """
  filterCheck: Artifacts!

  """
  Project this scope through the `generate` verb.
  Does not add `generate` as an artifact dimension.
  Returns a new scope with dimensions re-synthesized for that projection.
  """
  filterGenerate: Artifacts!

  """
  Project this scope through the `ship` verb.
  Does not add `ship` as an artifact dimension.
  Returns a new scope with dimensions re-synthesized for that projection.
  """
  filterShip: Artifacts!

  """
  Project this scope through the `up` verb.
  Does not add `up` as an artifact dimension.
  Returns a new scope with dimensions re-synthesized for that projection.
  """
  filterUp: Artifacts!

  """Ordered filterable dimensions for the current scope. Static header row."""
  dimensions: [ArtifactDimension!]!

  """Artifacts matching the current filters. Dynamic row set."""
  items: [Artifact!]!
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
  dimension does not apply to this artifact in the current scope. Each cell is
  the public string form that the CLI would round-trip through
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

  """
  Already-materialized non-object fields on the underlying object, for
  inspection. Does not invoke functions and does not include object-valued
  traversal members.
  """
  fields: [FieldValue!]!
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

## Notes

- **Static synthesis (no user-object evaluation):** root dimensions,
  verb-scoped artifact dimensions, filter flag generation
- **Dynamic enumeration:** items, item counts, coordinate values in the current
  scope, collection-derived selector value discovery

Non-collection selector synthesis is static. It should require only workspace
module discovery plus schema/object introspection; it should not require
evaluating user objects.

For ordinary zero-arg object-returning members, selector synthesis uses the
declared member name and return type only. It does not execute the function
body to discover rows or dimensions.

`Artifact.fields` is instance inspection only over already-materialized
non-object fields. It does not call functions and does not surface
object-valued traversal members.

`items()` is the one enumeration surface. An implementation may satisfy it
statically for ordinary object-structure scopes and dynamically for
collection-backed scopes, but the API contract is the same.

Root discovery and verb scopes may expose different dimensions. That is
expected: `workspace.artifacts` shows the full root selector space, while
`filterCheck`, `filterGenerate`, and other verb projections may prune that
space before selector synthesis runs.

### Filter Algebra

- Every chained `filterBy` / `filterCheck` / etc. is **AND**
- Multiple values within one `filterBy` call is **OR** (list of values)
- `filterBy("X")` (no values) → select all items where dimension `X` is
  present in the current scope
- `filterBy("X", ...)` is only valid if `X` is present in the current scope's
  `dimensions`
- `filterBy` preserves the current scope's header row; it narrows only `items`
- `filterCheck` / `filterShip` / etc. create a new projected scope and may
  resynthesize `dimensions`
- one verb projection is **OR** over descendants in the structural tree
- chained verb projections are **AND** across verbs over the same underlying
  structural tree
- verb projection order does not matter, and repeating the same verb
  projection is a no-op
- for a singleton dimension, `filterBy("X")` and `filterBy("X", ["X"])` are
  equivalent
- `filterBy("X", ["a"]).filterBy("X", ["b"])` → intersection (AND) — empty if
  no overlap
- verb projections do not follow cross-artifact references

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
- enough internal plumbing to drive selector scopes and projected discovery

Deferred to [plans.md](./plans.md):

- the first public rollout of the CLI contract described above
- verb execution over selected artifact scopes
- removal of the old check / generate execution path

### Pull Request Description

```text
This PR implements the Artifacts design unit. It adds `Workspace.artifacts`,
`Artifacts`, `Artifact`, `ArtifactDimension`, and `FieldValue`, together with
selector synthesis, scope-relative coordinate rows, and verb-projected selector
scopes. It lands the selector model itself, but defers public discovery and
verb execution to Execution Plans.
```

## Locked Decisions

- **`--mod` and `--module` coexist with distinct long forms.** `--mod` is the
  global module-loader flag; `--module` is the artifact dimension filter. No
  rename.

## Open Questions

None currently.
