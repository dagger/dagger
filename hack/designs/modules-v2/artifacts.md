# Artifacts

## Status: Designed

Depends on: Workspace plumbing (done)

## Table of Contents

- [What Problem Does This Solve?](#what-problem-does-this-solve)
- [What Artifacts Are](#what-artifacts-are)
- [Why They're Useful on Their Own](#why-theyre-useful-on-their-own)
- [What They Make Possible](#what-they-make-possible)
- [Detailed Design](#detailed-design)
- [Selector Model](#selector-model)
- [Selector Algebra](#selector-algebra)
- [CLI](#cli)
- [Acceptance Criteria](#acceptance-criteria)
- [Schema](#schema)
- [Filter Algebra](#filter-algebra)
- [Synthesis Constraints](#synthesis-constraints)
- [Implementation](#implementation)
- [Locked Decisions](#locked-decisions)
- [Open Questions](#open-questions)

## What Problem Does This Solve?

Your workspace has modules. Each module exposes objects. Those objects are
rich — they carry state, compose files, directories, containers, secrets. They
are a major part of how the Dagger platform works.

But objects are mostly invisible. Today, the platform sees three levels:
workspaces, modules, and functions. You can reach a function by its path
through the module, but the objects along that path have no identity. You pass
through them to get to the function. You can't point at them.

You can't say "the darwin build for our CLI" or "the base container for our Go
build environment" as things in their own right. You can only name the
functions you call on them. And you spell those function paths differently
depending on which command you use: `foo:bar` in `dagger check`, `foo bar` in
`dagger call`, `foo | bar` in `dagger shell`, `foo().bar()` in dang.

The objects are there. The platform doesn't see them.

## What Artifacts Are

Artifacts fill that gap. An artifact is an object that the platform can see —
discover, name, select, and act on.

The engine scans your modules' type graphs, finds the targetable objects, and
gives each one a set of coordinates. Think of it as a table. Each column is a
*dimension* — `module`, `platform`, `stage`, or whatever the object structure
implies. Each artifact is a row.

You discover artifacts with `dagger list`:

- `dagger list --help` shows the dimensions — what kinds of artifacts exist
- `dagger list platform` shows the actual artifacts in one dimension
- `dagger list platform --module=go` narrows further

## Why They're Useful on Their Own

The engine synthesizes dimensions from the type structure your modules already
have. A Go module with `linux` and `darwin` platform variants gets a `platform`
dimension automatically. A frontend module with `test` and `release` stages
gets a `stage` dimension. The module author doesn't build any targeting
machinery. It comes from the shape they already wrote.

## What They Make Possible

Everything else in the modules-v2 sequence builds on the selection model that
artifacts establish:

- **Execution Plans** turn a verb into an inspectable plan. Select artifacts
  with `--dimension=value` flags on any verb —
  `dagger check --module=go --platform=linux` — and the engine compiles that
  into a DAG of concrete actions. You can look at the plan before you run it.

- **Provenance** adds source-aware filtering. The engine tracks which workspace
  files each artifact depends on. `--path=./docs` or
  `--affected-by=HEAD~1` narrows the artifact set before any verb runs. Only
  check what the diff touched.

- **Collections** add dynamic dimensions. A test suite publishes its tests as a
  collection keyed by name. `--go-test=TestFoo` becomes another filter flag in
  the same model. Collections don't create a new targeting system — they plug
  into the one artifacts already defined.

- **Ship** uses the same selection and plan model for shipping. Pick artifacts,
  compile a ship plan, inspect it, run it.

One selection model. Everything else layers on top.

## Detailed Design

The sections below define the formal selector model, schema, and
implementation constraints.

`Workspace.artifacts` returns a filterable, introspection-driven view over
workspace objects. The CLI is a generic client — no per-workspace codegen.

Artifacts owns the general selector model: workspace/module dimensions such as
`module`, synthesized non-collection object-field selector dimensions, and
later, collection-provided dimensions.

## Selector Model

### Identity

Artifacts are identified by scope-relative coordinates. There is no separate
public address layer.

- **Artifacts live in selector space.** The noun quality comes from the
  `Artifact` type plus its scope-relative coordinates, not from a separate
  identity layer.
- **Blessed scalar types** (WorkspacePath, HTTPAddress, etc.) carry parsing and
  rendering semantics for filter values, not identity semantics.
- **Collections extend the selector model.** They are not the basis of it.

### Scope Kinds

Artifacts has two related scope kinds:

- **Root discovery scope** — `workspace.artifacts`, `dagger list`, and
  `dagger list --help`. This scope is not verb-scoped.
- **Verb scopes** — `workspace.artifacts.filterVerb(CHECK)`,
  `workspace.artifacts.filterVerb(GENERATE)`, and so on. These scopes project
  the current structural scope through one or more accumulated verb predicates
  and synthesize the selector space needed for the retained artifact objects.

Selector dimensions choose **artifacts** only:

- `Artifacts.dimensions` and `Artifact.coordinates` describe object selection
- verb scopes such as `filterVerb(CHECK)` narrow which artifact rows are in
  play, but they do not introduce new artifact dimensions on their own

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
`workspace.artifacts.filterVerb(CHECK)`.

The engine should synthesize enough public selector dimensions to make current
artifact objects positively selectable without a separate path grammar.

Verb projections compose: they are order-independent, idempotent, and
structural only (they do not compile or execute anything). See
[Selector Algebra](#selector-algebra) for the formal composition rules.

## Selector Algebra

This section defines how selector dimensions are synthesized from the object
tree already present in the current `Artifacts` scope, and how verb
projections compose over that tree.

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
- verb projections are order-independent and idempotent:

```text
filterVerb(CHECK).filterVerb(GENERATE) == filterVerb(GENERATE).filterVerb(CHECK)
filterVerb(CHECK).filterVerb(CHECK) == filterVerb(CHECK)
```

For example:

```text
module
├── test   # subtree contains a check
└── sdk    # subtree contains a generate
```

Then:

```text
workspace.artifacts.filterVerb(CHECK).items                    => [module, test]
workspace.artifacts.filterVerb(GENERATE).items                 => [module, sdk]
workspace.artifacts.filterVerb(CHECK).filterVerb(GENERATE).items   => [module]
```

### Artifacts vs. Structural Glue

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

- `Go` is an artifact (module root)
- `Platforms` is structural glue — the engine looks through it
- `Platform(linux)` and `Platform(darwin)` are artifacts

The engine synthesizes a `platform` dimension with values `linux` and `darwin`:

```text
dimensions: [module, platform]

module=go                          # the module root artifact
module=go, platform=linux          # the linux platform artifact
module=go, platform=darwin         # the darwin platform artifact
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
  .filterDimension("module", ["go"])
  .filterDimension("platform")
  .items
# => rows whose "platform" coordinate is linux or darwin

scope
  .filterDimension("module", ["go"])
  .filterDimension("platform", ["linux"])
  .items[0].coordinate("platform")
# => linux
```

Collections later add more dimensions to the same model; see
[collections.md](./collections.md).

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
  field/function name; its only non-null coordinate value is that same name.
  For example, if a branching point selects among `logs: Logs!` and
  `metrics: Metrics!` (heterogeneous types), the fallback produces two
  singleton dimensions: `logs` (with value `logs`) and `metrics` (with value
  `metrics`)
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
numeric suffix. See the [CI Collision / Qualification](#ci-collision--qualification)
acceptance test for a worked example.

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

In a different scope — the Go module's `dimensions = [module, platform]`:

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
- after `filterDimension("X")`, every returned artifact must have a non-null
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
example `workspace.artifacts.filterVerb(CHECK).dimensions` for `dagger check`)
and parses user input into `filterDimension` chains over that scope.

Collection-provided dimensions are an extension to this same model; see
[collections.md](./collections.md).

Root and verb-local discovery both use `dagger list`. Verb-local discovery
projects `dagger list` through one verb first, for example `dagger list
<dimension> --check`. Value listing is derived from `items()`, not from
`dimensions()`:

1. call `filterDimension("<dimension>").items()`
2. read `coordinate("<dimension>")` from each returned artifact
3. deduplicate and sort the distinct non-null values for display

This keeps `dimensions()` static while leaving value enumeration to the scoped
artifact set.

The important rule is scope matching: if the dimension came from
`workspace.artifacts.filterVerb(CHECK).dimensions`, value listing must query
`workspace.artifacts.filterVerb(CHECK).filterDimension("<dimension>").items()`,
not the root scope. `dagger list <dimension> --check` is the CLI surface for
that scope.

Implementation sketch:

```go
rows := scope.FilterDimension("platform", nil).Items()
values := Distinct(NonNil(Map(rows, func(a Artifact) string {
	return a.Coordinate("platform")
})))
```

### CLI Mappings

```
dagger list                → workspace.artifacts.dimensions
dagger list --help         → workspace.artifacts.dimensions
dagger list --check        → workspace.artifacts
                               .filterVerb(CHECK)
                               .dimensions
dagger list module         → workspace.artifacts
                               .filterDimension("module")
                               .items
                               # then print distinct coordinate("module")
dagger list platform       → workspace.artifacts
                               .filterDimension("platform")
                               .items
                               # then print distinct coordinate("platform")
dagger list platform --check
  --module=go              → workspace.artifacts
                               .filterVerb(CHECK)
                               .filterDimension("module", ["go"])
                               .filterDimension("platform")
                               .items
                               # then print sorted distinct coordinate("platform")
dagger check --help        → workspace.artifacts.filterVerb(CHECK).dimensions
```

## Acceptance Criteria

These examples intentionally split into two layers:

1. **UI output tests** — the CLI is given a fixed resolved `Artifacts` scope;
   every byte of input and output matters.
2. **Dimension detection tests** — a schema is given; only the detected
   dimensions matter. CLI formatting and help wording are noise.

These two test classes should not be coupled.

### UI Output Tests

The following are golden-output tests for CLI rendering. They assume the
selector engine has already resolved the current scope's `dimensions`,
`keyType`s, and any statically enumerable flag values.

Fixture:

- root dimensions: `module`, `sdk`, `release-lane`
- statically enumerable module values in scope: `sdks`, `release`
- statically enumerable `release-lane` values in scope: `stable`,
  `experimental`

Expected:

```console
$ dagger list --help
Usage:
  dagger list <dimension> [flags]

Available dimensions:
  module         List available modules
  release-lane   List values for the artifact dimension "release-lane"
  sdk            List values for the artifact dimension "sdk"
```

Expected:

```console
$ dagger list sdk --help
Usage:
  dagger list sdk [flags]

Flags:
  --module=sdks|release                  Narrow to selected modules
  --release-lane=stable|experimental     Narrow to selected release lanes
```

Fixture:

- check-projected dimensions: `module`, `engine-test`
- statically enumerable module values in scope: `test-split`, `ci`
- `engine-test` values are dynamic

Expected:

```console
$ dagger check --help
Flags:
  --module=test-split|ci   Filter by module
  --engine-test=<name>     Filter by engine-test
```

Expected:

```console
$ dagger list engine-test --module=test-split
base
provision
telemetry
call-and-shell
cli-engine
client-generator
```

### Dimension Detection Tests

The following examples are selector-synthesis tests. The expected result is the
set of dimensions detected for each scope.

#### SDK Family

Inspired by the real `sdks`, `go-sdk`, `java-sdk`, and `typescript-sdk`
toolchains.

```dang
type AllSdks {
  pub go: Sdk! {
    Sdk(name: "go", sourcePath: "sdk/go")
  }

  pub java: Sdk! {
    Sdk(name: "java", sourcePath: "sdk/java")
  }

  pub typescript: Sdk! {
    Sdk(name: "typescript", sourcePath: "sdk/typescript")
  }
}

type Sdk {
  pub name: String!
  pub sourcePath: String!
}
```

Expected detected dimensions:

```console
root: { module, sdk }
```

#### Release Lanes

Inspired by the repo's `release` flows plus SDK publishing.

```dang
type Release {
  pub stable: ReleaseLane! {
    ReleaseLane()
  }

  pub experimental: ReleaseLane! {
    ReleaseLane()
  }
}

type ReleaseLane {
  pub go: Sdk! {
    Sdk(name: "go")
  }

  pub java: Sdk! {
    Sdk(name: "java")
  }

  pub typescript: Sdk! {
    Sdk(name: "typescript")
  }
}

type Sdk {
  pub name: String!
}
```

Expected detected dimensions:

```console
root: { module, release-lane, sdk }
```

#### Engine Test Split Collection

Inspired directly by `toolchains/test-split`.

```dang
type TestSplit {
  pub engineTests: EngineTests! {
    EngineTests(keys: [
      "base",
      "provision",
      "telemetry",
      "call-and-shell",
      "cli-engine",
      "client-generator",
    ])
  }
}

type EngineTests @collection {
  pub keys: [String!]

  pub get(name: String!): EngineTest! {
    EngineTest(name: name)
  }
}

type EngineTest {
  pub name: String!
}
```

Expected detected dimensions:

```console
root: { module, engine-test }
check: { module, engine-test }
```

#### CI Collision / Qualification

Inspired by the real split between engine-focused and SDK-focused CI work.

```dang
type Ci {
  pub engine: EngineChecks! {
    EngineChecks()
  }

  pub sdks: SdkChecks! {
    SdkChecks()
  }
}

type EngineChecks {
  pub lint: CheckStage! {
    CheckStage(name: "lint")
  }

  pub test: CheckStage! {
    CheckStage(name: "test")
  }
}

type SdkChecks {
  pub lint: CheckStage! {
    CheckStage(name: "lint")
  }

  pub test: CheckStage! {
    CheckStage(name: "test")
  }
}

type CheckStage {
  pub name: String!
}
```

Expected detected dimensions:

```console
root: { module, engine, sdks, engine-check-stage, sdks-check-stage }
```

#### Verb Projection Composition

Inspired by a monorepo root that contains engine checks and SDK generation in
different subtrees.

```dang
type Ci {
  pub engine: EngineSuite! {
    EngineSuite()
  }

  pub sdks: SdkSuite! {
    SdkSuite()
  }
}

type EngineSuite {
  pub lint: Void @check {
    null
  }
}

type SdkSuite {
  pub generateClients: Directory! @generate {
    directory
  }
}
```

Expected detected dimensions:

```console
root: { module, engine, sdks }
check: { module, engine }
generate: { module, sdks }
check+generate: { module }
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
  filterDimension(dimension: String!, values: [String!]): Artifacts!

  """
  Project this scope through one verb.
  Does not add the verb as an artifact dimension.
  Returns a new scope with dimensions re-synthesized for that projection.
  """
  filterVerb(verb: Verb!): Artifacts!

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

"""A structural verb projection over artifacts."""
enum Verb {
  CHECK
  GENERATE
  SHIP
  """Reserved for a future design."""
  UP
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

## Filter Algebra

- Every chained `filterDimension` / `filterVerb(...)` is **AND**
- Multiple values within one `filterDimension` call is **OR** (list of values)
- `filterDimension("X")` (no values) → select all items where dimension `X` is
  present in the current scope (non-null coordinate)
- `filterDimension("X", ...)` is only valid if `X` is present in the current
  scope's `dimensions`
- `filterDimension("X", [])` (empty list) is invalid; omit `values` for
  presence filtering. The engine returns an error at call time.
- `filterDimension` preserves the current scope's header row; it narrows only
  `items`
- `filterDimension("X").filterDimension("X", ["a"])` is equivalent to
  `filterDimension("X", ["a"])` — the value-filter is strictly narrower than
  the presence-filter
- `filterDimension("X", ["a"]).filterDimension("X", ["b"])` → intersection
  (AND) — empty if no overlap
- for a singleton dimension, `filterDimension("X")` and
  `filterDimension("X", ["X"])` are equivalent
- `filterVerb(...)` creates a new projected scope and may
  resynthesize `dimensions`
- one verb projection is **OR** over descendants in the structural tree
- chained verb projections are **AND** across verbs over the same underlying
  structural tree
- verb projection order does not matter, and repeating the same verb
  projection is a no-op
- verb projections do not follow cross-artifact references

## Synthesis Constraints

- **Static synthesis (no user-object evaluation):** non-collection selector
  synthesis requires only workspace module discovery plus schema/object
  introspection. It does not require evaluating user objects.
- **Dynamic enumeration:** `items()`, item counts, and coordinate values in
  the current scope are dynamic. Collection-derived selector value discovery
  is also dynamic.
- For ordinary zero-arg object-returning members, selector synthesis uses the
  declared member name and return type only. It does not execute the function
  body to discover rows or dimensions.
- `Artifact.fields` is instance inspection only over already-materialized
  non-object fields. It does not call functions and does not surface
  object-valued traversal members.
- `items()` is the one enumeration surface. An implementation may satisfy it
  statically for ordinary object-structure scopes and dynamically for
  collection-backed scopes, but the API contract is the same.
- Root discovery and verb scopes may expose different dimensions. That is
  expected: `workspace.artifacts` shows the full root selector space, while
  `filterVerb(CHECK)`, `filterVerb(GENERATE)`, and other verb projections may
  prune that space before selector synthesis runs.

## Implementation

This design is intended to land as one primary implementation unit:

- **PR:** `artifacts plumbing: selector scopes and rows`
- **API:** `Workspace.artifacts`, `Artifacts`, `Artifact`, `ArtifactDimension`,
  `FieldValue`
- **Contract:** static `dimensions`, dynamic `items`, row-shaped
  `coordinates`, root and verb-projected selector scopes

Included in this unit:

- selector synthesis and coordinate rows
- `filterDimension`, `filterVerb`
- `Artifact.coordinate(name)` and field inspection
- enough internal plumbing to drive selector scopes and projected discovery

Deferred to [plans.md](./plans.md):

- the first public rollout of the CLI contract described above
- verb execution over selected artifact scopes
- removal of the old check / generate execution path

## Locked Decisions

- **`--mod` and `--module` coexist with distinct long forms.** `--mod` is the
  global module-loader flag; `--module` is the artifact dimension filter. No
  rename.

## Open Questions

None currently.
