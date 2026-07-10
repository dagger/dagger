# Artifacts

Artifacts is the base selection model for Dagger's lifecycle verb commands
(`dagger check`, `generate`, `ship`, `up`). It defines what those commands can
select, narrow, and act on. [Execution Plans](./plans.md) and
[Collections](./collections.md) build on it.

## Table of Contents

- [Problem](#problem)
- [Non-Goals](#non-goals)
- [Canonical Example](#canonical-example)
- [Model](#model)
- [Verbs](#verbs)
- [CLI](#cli)
- [Schema](#schema)
- [Filter Algebra](#filter-algebra)
- [Acceptance Criteria](#acceptance-criteria)
- [Open Questions](#open-questions)
- [Decisions](#decisions)

## Problem

The platform sees workspaces, modules, and functions, but the objects a module
exposes have no stable selection model. You can call functions through them, but
the objects themselves are invisible, and the verb commands have no shared way
to ask "what in this workspace can I run this verb on?" Left unsolved, each verb
command grows its own selection syntax and discovery rules.

Artifacts gives the verb commands one filterable view of selectable things, with
one selection vocabulary they all share.

## Non-Goals

This document defines selection only. It does not cover:

- **`dagger call` and `dagger shell`.** They keep their existing path syntaxes.
  Whether they ever adopt this model is deliberately left open (see
  [Decisions](#decisions)), not a commitment.
- **Actions, action paths, and execution plans.** Defined in
  [plans.md](./plans.md). Artifacts defines only enough verb-scope behavior to
  decide which artifacts stay in scope for a verb.
- **The `@collection` contract.** Artifacts assumes collections exist and yield
  selectable items; [collections.md](./collections.md) defines what a collection
  guarantees.
- **Coordinate-to-argument lowering.** Turning a coordinate string like
  `./my-app` into the typed argument an action expects is unspecified here. See
  [Q4](#open-questions).
- **Provenance and change predicates.** These extend Artifacts in
  [provenance.md](./provenance.md).

## Canonical Example

This module is referenced throughout all three documents.

```dang
module go {
  pub go: Go! {
    Go()
  }
}

type Go {
  pub lint: Void! @check

  pub tests: GoTests! {
    GoTests()
  }
}

type GoTests @collection {
  pub keys: [String!]

  pub get(name: String!): GoTest! {
    GoTest(name: name)
  }
}

type GoTest {
  pub name: String!
  pub run: Void! @check
}
```

Assume the collection currently holds keys `TestFoo` and `TestBar`. This yields
three artifacts: the top-level `go` object, and one item per test.

## Model

`Workspace.artifacts` is a filterable, introspection-driven view over the
workspace. It owns artifact eligibility, dimensions, coordinate rows, and the
closed `Verb` vocabulary that plan compilation consumes. It does not own actions
or plans.

### Artifacts and glue

An **artifact** is a stably named, CLI-selectable thing with its own lifecycle:
it has a coordinate the user can spell, that coordinate is stable across runs,
and it is the kind of thing you lint, generate for, ship, or bring up.

Exactly two things are artifacts:

1. **Top-level module objects** — the roots a module exposes (usually one,
   sometimes more). In the canonical example, `go`.
2. **Collection items** — the keyed members of a `@collection` type. In the
   canonical example, `TestFoo` and `TestBar`.

Everything else is **structural glue**: nested non-collection objects,
configuration helpers, internal structure. Glue is not selectable, but it still
matters for verb reachability and action-path naming (`tests:run`). In the
canonical example, nothing between `Go` and `GoTest` is glue; add an
intermediate `type Tests { ... }` and it would be.

The eligibility rule is graph-shape-derived. Cases it does not cover
(heterogeneous named substructures like `Release.{sdk,cli,docs}`) are
[Q6](#open-questions).

### Dimensions and coordinates

The client model is a table:

- `Artifacts.dimensions` is the ordered header row.
- Each artifact carries one coordinate row of the same length and order.
- `null` in a cell means that dimension does not apply to that artifact.
- After `filterDimension("X")`, every row has a non-null cell for `X`.

For the canonical example the root scope is:

```console
dimensions = [type, go-test]

go   top-level object   -> ["go",      null]
TestFoo item            -> ["go-test", "TestFoo"]
TestBar item            -> ["go-test", "TestBar"]
```

Dimensions are schema-stable: filtering narrows rows, never the dimension set.
Top-level rows carry `null` for collection dimensions that do not apply.

### The `type` dimension

`type` is the one built-in dimension. Every artifact has a non-null `type`,
derived purely syntactically from the artifact's GraphQL object type name,
kebab-cased: `Go` → `go`, `SdkRelease` → `sdk-release`, `GoTest` → `go-test`. It
does not consult the module name, the field name, or user configuration.

Collections add further dimensions such as `go-test` or `go-module`. There is no
synthesized non-collection dimension algebra: a nested object does not become a
dimension merely by existing.

Cross-module `type` collisions are [Q1](#open-questions).

### Selector paths and the `type:field` bridge

A selector string is `[<type>]:<field>[:<field>…]`: the leading segment is a
`type` coordinate, each remaining segment a step along the **artifact-relative**
field/action path, and a `[<key>]` suffix pins a collection member (lowering to
the same key filter as `--<item-type>=<key>`, not a bespoke `get`). The leading
`<type>` may be dropped once scope already fixes one artifact — `dagger check
--type=go tests:run` ≡ `check go:tests:run`. Because a top-level object's `type`
kebab-matches its constructor field (`Go`/`go`), every legacy `<module>:<fn>`
string re-resolves **unchanged** under this reading: the token that once
navigated a root field now selects the artifact of that `type`. Collection items
are ordinary artifacts, so `type:field[:field]` reaches them identically;
`[<key>]` only refines *which* member.

One grammar backs every consumer of the selection vocabulary — positional
`check`/`generate`/`up` selectors, `FunctionPattern` ([plans.md](./plans.md)),
and `from` references in workspace config — so a path learned in one place
transfers verbatim to the others. Disambiguation is engine-owned: a leading
segment naming an in-scope `type` is a type filter; otherwise it is the first
step of a relative path.

### Dimension key types

Dimension keys travel as strings. `Artifact.coordinates` is `[String]!`, CLI
flags pass strings, and `filterCoordinates` takes strings.
`ArtifactDimension.keyType` is metadata — not transport — so the engine can
parse, validate, and render values.

This constrains authors:

- Keys must be stringifiable with a canonical round-trip. Strings, integers,
  booleans, and enums qualify; custom scalars qualify only if they document a
  total string round-trip.
- Object types (`Directory`, `Container`, any dagql object) do not qualify: they
  carry identity beyond a string, and two values that print the same may not be
  equal.
- Declaring a non-stringifiable `keyType` is a workspace-load error.

When a key is "string-on-the-wire, typed elsewhere" (a path later lowered to a
`Directory` argument), the lowering step is out of scope. See
[Q4](#open-questions).

## Verbs

A **verb** is a standardized lifecycle commitment the CLI builds generic UX
around. The set is closed: `CHECK`, `GENERATE`, `SHIP`, `UP`.

| Verb       | Behavior                                              | Side effects                              |
| ---------- | ----------------------------------------------------- | ----------------------------------------- |
| `CHECK`    | Validates the artifact. Fails on issues.              | None. Read-only.                          |
| `GENERATE` | Produces files in the workspace.                      | Writes to local filesystem.               |
| `SHIP`     | Publishes the artifact to a remote.                   | Writes to a remote. One-shot.             |
| `UP`       | Brings up a long-running interactive service.         | Runs a process; forwards signals; ports.  |

The four are orthogonal across two axes — validate vs. mutate, and
local/remote/live — and cover the lifecycle without overlap. Per-verb execution
is specified separately over the plan substrate: `check` and `generate` in
[plans.md](./plans.md), `ship` in [ship.md](./ship.md); `up` uses the same
substrate.

A function joins a verb by annotation. In the canonical example `lint` is both a
regular function (`dagger call lint`) and a member of CHECK (`dagger check`):

```dang
pub lint: Void! @check
```

The set is closed because that is what makes the CLI generic: if authors could
invent verbs, each would need its own command and UX, and the payoff of
standardization would vanish. Author-imagined verbs lower into the four —
`@test`/`@audit`/`@scan`/`@lint` → `@check`, `@codegen`/`@format` →
`@generate`, `@release`/`@publish` → `@ship`, long-running `@deploy` → `@up`.
Adding a verb is a language-level change, on the order of a new HTTP method.

### Reachability

Artifacts exposes no public verb filter. It owns one structural fact that plan
compilation depends on:

```text
hasVerb(artifact, V) = the artifact can reach at least one handler for V
                       within its own boundary
```

Reachability is a static walk over **types**, not instances: no resolvers run,
no arguments are fabricated, and the answer is independent of instance state.
This keeps verb exposure cheap and side-effect free, and avoids guessing
argument values there is no general way to supply.

For an artifact of type `T`, starting at `T`:

1. Walk recursively through object-valued fields and zero-arg object-valued
   functions (using their declared return type as the next node).
2. At each type, direct verb-annotated functions are reachable handlers.
3. Stop at: the next artifact boundary, members that require arguments,
   non-object fields, and cross-artifact references.

So a top-level object rolls up handlers from its nested glue, a collection item
rolls up handlers from glue inside that item, and a parent artifact does not
absorb handlers from its child collection items.

`Artifacts.plan(verb: V, ...)` considers only artifacts where
`hasVerb(artifact, V)` holds.

### Limitation: handlers behind required arguments

Because the walk stops at members requiring arguments, a handler reachable only
through a required-argument traversal is invisible to reachability:

```dang
type App {
  pub env(name: String!): Service! { ... }
}

type Service {
  pub healthCheck: Void! @check
}
```

`App.env(name)` requires an argument, so `App` is not in CHECK scope even though
`Service.healthCheck` is annotated. There is no general way to enumerate `name`
without running user code. **Collections are the supported escape hatch**: mark
`Envs` `@collection`, and each environment becomes an artifact row whose handlers
are reachable normally.

Whether a verb-annotated handler may itself take required arguments is
[Q2](#open-questions).

## CLI

One built-in listing subcommand exists:

- `dagger list types` — a UI alias over the built-in `type` dimension.

Every other dimension uses its name directly: `dagger list go-test`,
`dagger list go-module`. Extension docs may add CLI aliases that lower to the
same dimension filters; those are command syntax, not new dimensions.
[collections.md](./collections.md) defines `--<collection-type>` as presence
sugar this way.

### Discovery and value listing

```console
$ dagger list --help
Usage:
  dagger list <dimension> [flags]

Available dimensions:
  types     List available artifact types
  go-test   List values for the artifact dimension "go-test"
```

Value listing is derived from `items()`: apply active filters, keep rows where
the target dimension is non-null, and print the distinct values in stable order.

```console
$ dagger list types
go
js
go-test

$ dagger list go-test
TestFoo
TestBar
```

### Filter flags

Valued dimension flags are repeatable and lower to `filterCoordinates(...)`; no
comma-separated values.

```console
--type=<value>
--go-test=<value>
--go-module=<value>
```

Presence filtering (`filterDimension`) and coordinate filtering
(`filterCoordinates`) are separate API operations. The CLI normalizes any
command-specific sugar into these before evaluation, so Artifacts only ever sees
real dimensions.

If a more specific dimension already fixes the artifact kind, `--type` is
redundant. These are equivalent:

```console
$ dagger check --type=go-test --go-test=TestFoo run
$ dagger check --go-test=TestFoo run
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
  Keep rows whose coordinate row has a non-null cell for the given dimension.
  Errors if the dimension is not present in the current scope.
  Preserves the current scope's dimension order and narrows only the row set.
  """
  filterDimension(dimension: String!): Artifacts!

  """
  Keep rows whose coordinate for the given dimension matches one of `values`.
  Errors if the dimension is not present in the current scope.
  Preserves the current scope's dimension order and narrows only the row set.
  Values are parsed and validated according to that dimension's `keyType`.
  `values` must be non-empty.
  """
  filterCoordinates(dimension: String!, values: [String!]!): Artifacts!

  """
  Ordered filterable dimensions for the current scope.
  Always includes `type`. May include collection dimensions such as `go-test`
  or `go-module`.
  """
  dimensions: [ArtifactDimension!]!

  """Artifacts matching the current filters."""
  items: [Artifact!]!
}

"""A filterable axis of the artifact graph."""
type ArtifactDimension {
  """
  Filter name as used in CLI flags and table headers.
  Examples: `type`, `go-test`.
  """
  name: String!

  """
  Type of this dimension's keys. Determines parsing, validation, and help
  rendering. It does not enumerate the current in-scope values.
  """
  keyType: TypeDef!
}

enum Verb {
  CHECK
  GENERATE
  SHIP
  UP
}

"""One artifact in the workspace."""
type Artifact {
  """
  Ordered coordinate row for this artifact.
  Same length and order as `scope.dimensions`.
  """
  coordinates: [String]!

  """Convenience lookup for one coordinate by dimension name."""
  coordinate(name: String!): String

  """
  The Artifacts scope that produced this row. Coordinates are unique only
  within this scope.
  """
  scope: Artifacts!
}
```

## Filter Algebra

Coordinate filters compose the obvious way:

- values within one dimension are **OR**
- different dimensions are **AND**

```console
$ dagger check --type=go --type=js lint      # type is go OR js
$ dagger check --type=go-test --go-test=TestFoo run   # type=go-test AND go-test=TestFoo
```

Presence filtering keeps rows where a coordinate is non-null; coordinate
filtering keeps rows whose value matches one of the given values. So
`filterDimension("go-test").filterCoordinates("go-test", ["TestFoo"])` equals
`filterCoordinates("go-test", ["TestFoo"])`.

Filters only narrow rows; they never re-order or drop dimensions. Verb selection
is not part of this algebra — it happens in `Artifacts.plan(...)` in
[plans.md](./plans.md).

## Acceptance Criteria

These split into two layers: **UI output tests**, where the CLI is given a fixed
resolved scope and every byte of output matters; and **detection tests**, where
a schema is given and only the resulting rows and dimensions matter.

### UI Output Tests

Fixture:

- root dimensions: `type`
- statically enumerable `type` values in scope: `go`, `js`

Expected:

```console
$ dagger list --help
Usage:
  dagger list <dimension> [flags]

Available dimensions:
  types     List available artifact types
```

Expected:

```console
$ dagger check --help
Flags:
  --type=go|js   Filter by artifact type
```

Fixture:

- root dimensions: `type`, `go-module`, `go-test`
- statically enumerable `type` values in scope: `go`, `js`, `go-module`,
  `go-test`
- statically enumerable `go-module` values in scope: `./my-app`, `./lib`
- `go-test` values are dynamic

Expected:

```console
$ dagger list --help
Usage:
  dagger list <dimension> [flags]

Available dimensions:
  types     List available artifact types
  go-module List values for the artifact dimension "go-module"
  go-test   List values for the artifact dimension "go-test"
```

Expected:

```console
$ dagger list go-test --help
Usage:
  dagger list go-test [flags]

Flags:
  --go-module=./my-app|./lib   Narrow to selected go modules
```

Expected:

```console
$ dagger list go-test --go-module=./my-app
TestFoo
TestBar
```

### Detection Tests

#### One top-level object per module

```dang
module go {
  pub go: Go! {
    Go()
  }
}

module js {
  pub js: Js! {
    Js()
  }
}

type Go {
  pub lint: Void! @check
}

type Js {
  pub lint: Void! @check
}
```

Expected:

```console
dimensions: { type }
rows:
  { type=go }
  { type=js }
```

#### Several top-level objects in one module

```dang
module release {
  pub sdkRelease: SdkRelease! {
    SdkRelease()
  }

  pub cliRelease: CliRelease! {
    CliRelease()
  }
}

type SdkRelease {
  pub publish: Void! @generate
}

type CliRelease {
  pub publish: Void! @generate
}
```

Expected:

```console
dimensions: { type }
rows:
  { type=sdk-release }
  { type=cli-release }
```

Proves several top-level objects in one module are distinguishable without a
`module` dimension.

#### Structural glue does not create rows

```dang
module go {
  pub go: Go! {
    Go()
  }
}

type Go {
  pub tests: Tests! {
    Tests()
  }
}

type Tests {
  pub runBun: Void! @check
  pub runNodejs: Void! @check
}
```

Expected:

```console
dimensions: { type }
rows:
  { type=go }
```

No extra `tests` row and no extra dimension.

#### Collection items add rows and dimensions

```dang
module go {
  pub go: Go! {
    Go()
  }
}

type Go {
  pub tests: GoTests! {
    GoTests()
  }
}

type GoTests @collection {
  pub keys: [String!]

  pub get(name: String!): GoTest! {
    GoTest(name: name)
  }
}

type GoTest {
  pub name: String!
  pub run: Void! @check
}
```

Assume `keys` resolves to `TestFoo` and `TestBar`.

Expected:

```console
dimensions: { type, go-test }
rows:
  { type=go,      go-test=null }
  { type=go-test, go-test=TestFoo }
  { type=go-test, go-test=TestBar }
```

#### Verb projection uses reachable handlers inside the artifact boundary

```dang
module go {
  pub go: Go! {
    Go()
  }
}

type Go {
  pub lint: Void! @check

  pub tests: Tests! {
    Tests()
  }
}

type Tests {
  pub run: Void! @check
  pub generateFixtures: Directory! @generate
}
```

Expected:

```console
root rows:
  { type=go }

check rows:
  { type=go }

generate rows:
  { type=go }
```

Proves nested glue contributes verb reachability without becoming rows or
dimensions.

## Open Questions

### Q1. Cross-module `type` collisions

Two modules each exposing a top-level object of GraphQL type `Release` both
produce `type=release`. Options: validate workspace-unique at load and error on
collision; allow collisions and add a `module` dimension; or namespace on
collision (`mymod/release`). **Recommendation:** workspace-unique by
construction — simplest, catches it early. (The POC implements this by
module-namespacing type names.)

### Q2. Handler arity

The walk stops at members requiring arguments — but is a verb-annotated handler
that itself takes required arguments a reachable handler? Options: (A) handlers
must be nullary; (B) any arguments allowed, downstream layers handle invocation;
(C) arguments allowed only if every required one has a default.
**Recommendation:** C, plus a pointer to the CLI invocation contract for
non-default arguments. A is too restrictive for real checks (linter strictness,
test seeds); B hides the question.

### Q4. Coordinate-to-argument lowering

Filtering and listing are defined on string coordinates, but invoking an action
that takes a dimensioned value as a typed argument (`--go-module=./my-app` where
`lint(module: Directory!)`) requires turning `./my-app` into that typed value.
This is missing infrastructure, and probably belongs in
[plans.md](./plans.md). To decide: where the spec lives; whether `keyType`
carries enough to lower or a separate hook is needed; how path normalization
works and whether two spellings can be coordinate-equal.

### Q6. Heterogeneous named substructures

The eligibility set does not cover named, heterogeneous, non-collection
substructures:

```dang
type Release {
  pub sdk: SdkRelease!
  pub cli: CliRelease!
  pub docs: DocsRelease!
}
```

These are not collections (not homogeneous, not keyed) and not top-level
objects, so today they are glue — there is no way to "ship just the SDK release"
without restructuring. Every workaround is bad (split modules loses the grouping;
force-fit a collection needs a homogeneous item type; treat as glue loses the
child scope). Option: a `@artifact` directive as a third eligibility rule, so
the rules collapse into "the author marked this as a thing" with three forms:
top-level (implicit), `@collection` (keyed-homogeneous), `@artifact`
(heterogeneous-named). **Recommendation:** worth a real design conversation;
the current restriction may be fine for today's ecosystem but gets painful as
module shapes grow.

### Q3 / Q5 / Q7 (minor)

- **Q3 — `Artifact` identity.** An `Artifact` is a `(scope, coordinates)`
  projection, not a stable cross-scope entity, with consequences for caching,
  equality, and bookmarking. Recommendation: accept and document the projection
  model; revisit if a concrete tooling need for cross-scope identity appears.
- **Q5 — `@collection` contract.** Artifacts leans on `@collection` for a stable
  `keys`/`get` shape, unique bounded keys, a fixed item type, and no holes.
  [collections.md](./collections.md) must expose these guarantees by name.
- **Q7 — display data.** Whether artifacts should carry non-coordinate object
  data for inspection (`dagger list --wide`, TUI). Out until a concrete use case
  and a cost model exist.

## Decisions

- Eligible artifacts are exactly top-level module objects and collection items;
  all other nested objects are structural glue.
- `type` is the built-in dimension; there is no synthesized non-collection
  dimension algebra. Dimensions are schema-stable — filtering never drops a
  dimension. (Rejected: shrinking dimensions on filter — breaks composability.)
- Dimension keys are stringifiable; object types do not qualify. (Rejected:
  object-typed keys with opaque IDs — undefined equality, no round-trip.)
- The verb set is closed. New verbs are language-level changes. (Rejected:
  user-defined verbs — defeats generic CLI UX.)
- Verb exposure and reachability are type-level and side-effect free: they never
  run user code or fabricate arguments.
- Artifacts exposes no public verb filter; verb selection starts at
  `Artifacts.plan(...)` in [plans.md](./plans.md).
- `dagger call` and `dagger shell` keep their existing syntaxes; the verb
  commands consume the artifact model. (Rejected: migrating `call` onto
  artifacts — different audiences.)
