# Artifacts

## Table of Contents

- [What Problem Does This Solve?](#what-problem-does-this-solve)
- [Non-Goals](#non-goals)
- [What Is an Artifact?](#what-is-an-artifact)
- [What Is a Verb?](#what-is-a-verb)
- [Core Model](#core-model)
- [Artifact Eligibility](#artifact-eligibility)
- [Dimensions And Coordinates](#dimensions-and-coordinates)
- [Verb Scopes](#verb-scopes)
- [CLI](#cli)
- [Schema](#schema)
- [Filter Algebra](#filter-algebra)
- [Acceptance Criteria](#acceptance-criteria)
- [Open Questions](#open-questions)
- [Alternatives Considered](#alternatives-considered)
- [Locked Decisions](#locked-decisions)

## What Problem Does This Solve?

Today the platform mostly sees workspaces, modules, and functions.

Modules expose object graphs, but those objects do not have a stable public
selection model. You can call functions through them, but the objects
themselves are mostly invisible. The structural-verb commands — the
lifecycle commands like `dagger check` and (later) `dagger generate`,
`dagger ship`, `dagger up` — have no shared way to ask "what things in this
workspace can I run this verb on?". Each one would otherwise grow its own
selection syntax and its own discovery rules.

Artifacts fixes that by giving the verb commands a small set of real objects
they can select, narrow, and act on. The verb commands all share the same
selection vocabulary; the workspace exposes one filterable view of artifacts
and the verb commands consume it.

In this design:

- top-level module objects are artifacts
- collection items are artifacts
- ordinary nested objects are not artifacts

Nested non-collection structure still matters. It just shows up as action paths
inside an artifact, not as separate artifacts.

## Non-Goals

This document defines a selection model for verb commands. It deliberately
does not cover:

- **`dagger call` and `dagger shell`.** These remain as-is with their
  existing path syntaxes. Whether they ever migrate onto the artifact model
  is an open question, not a commitment.
- **Action discovery, action paths, and execution plans.** These are defined
  in [plans.md](./plans.md). This document only defines enough verb-scope
  behavior to decide which artifacts stay in scope for a verb.
- **The `@collection` directive contract.** Artifacts assumes collections
  exist and produce eligible item rows. The contract for what a collection
  is, what `keys` and `get` must guarantee, and how items are enumerated
  lives in [collections.md](./collections.md). See
  [Open Questions](#open-questions).
- **Coordinate-to-argument-value lowering.** When a verb command runs an
  action that takes a dimensioned value as an argument (e.g.
  `dagger check --go-module=./my-app`), there must be a step that turns the
  coordinate string `./my-app` into the typed argument the action expects.
  That hand-off is not specified here. See [Open Questions](#open-questions).
- **Provenance and path-based change predicates.** These extend Artifacts in
  [provenance.md](./provenance.md) and are out of scope for this document.

## What Is an Artifact?

An **artifact** is a stably named, CLI-selectable thing in the workspace
that has its own lifecycle.

"Stably named" means: it has a coordinate the user can spell at the command
line, and that coordinate does not change across runs unless the workspace
itself changes. "CLI-selectable" means: every artifact is a row the verb
commands can narrow to. "Has its own lifecycle" means: it's the kind of
thing you might want to lint, generate code for, ship, or bring up — as
opposed to internal structure of one of those things.

Two concrete kinds of artifacts exist in this design:

1. **Top-level module objects.** The roots a module exposes. Usually one
   per module, sometimes more. These are the units the workspace owns
   directly.
2. **Collection items.** The keyed members of a `@collection`-marked type.
   These are the units the workspace owns *transitively* via a collection.

Everything else in the object graph — non-collection nested objects,
configuration helpers, internal structure — is **structural glue**. Glue
matters for action reachability and path naming, but it is not selectable
in its own right.

The choice of "what counts as an artifact" is a model decision with real
trade-offs. See [Alternatives Considered](#alternatives-considered) for the
alternatives that were not adopted, and [Open Questions](#open-questions)
for cases where the current rule may be incomplete.

## What Is a Verb?

A **verb** is a standardized lifecycle commitment that the CLI builds
generic UX around. The four verbs in this design are `CHECK`, `GENERATE`,
`SHIP`, and `UP`. The set is closed.

Verbs are not categories of functions. They are *promises about behavior*
that a function opts into via annotation. The promise is what lets the
structural-verb commands (`dagger check`, `dagger generate`, etc.) be
generic: a single CLI command knows how to render results, batch
invocations, surface failures, and integrate with tooling, *because* every
function it might invoke has committed to the same behavioral contract.

### The four verbs and their contracts

| Verb       | Behavior                                              | Side effects                              |
| ---------- | ----------------------------------------------------- | ----------------------------------------- |
| `CHECK`    | Validates the artifact. Fails on issues.              | None. Read-only.                          |
| `GENERATE` | Produces files in the workspace.                     | Writes to local filesystem.               |
| `SHIP`     | Publishes the artifact to a remote (registry, repo). | Writes to a remote. One-shot.             |
| `UP`       | Brings up a long-running interactive service.        | Runs a process; forwards signals; ports.  |

These four are roughly orthogonal across two axes: validate vs. mutate, and
local vs. remote vs. live. They cover the standard lifecycle of "things in a
workspace" without overlap.

### Verbs vs. functions

Functions are arbitrary user code, called via `dagger call <fn>`. They have
no contract beyond "this is invokable."

Verbs are a fixed set of lifecycle commitments. A function becomes part of
a verb when it is annotated:

```dang
type Go {
  pub lint: Void! @check
}
```

Here `lint` is two things at once: a regular function (callable via
`dagger call lint`) **and** a member of the CHECK lifecycle (visible to
`dagger check`). The annotation is the function's promise that it
satisfies the verb's contract — read-only in this case.

### Why a closed set

The closed set is load-bearing, not arbitrary. If module authors could
invent their own verbs, the CLI could not have generic behavior for any of
them — each new verb would need its own command, its own UX, its own
integration story. The whole payoff of standardization vanishes.

Common things authors might think of as "their own verb" lower naturally
into the existing four:

| Author intent              | Verb         |
| -------------------------- | ------------ |
| `@test`, `@audit`, `@scan` | `@check`     |
| `@lint`, `@format` (rule)  | `@check`     |
| `@codegen`, `@format`      | `@generate`  |
| `@deploy` (one-shot)       | `@ship`      |
| `@deploy` (long-running)   | `@up`        |
| `@release`, `@publish`     | `@ship`      |

Adding a new verb is a language-level change to Dagger's lifecycle
vocabulary, on the order of adding a new HTTP method. It is rare,
deliberate, and carries the weight of a new top-level CLI command — because
that is what it is.

## Core Model

`Workspace.artifacts` is a filterable, introspection-driven view over the
workspace's artifacts.

Artifacts owns:

- artifact eligibility
- artifact scopes
- artifact dimensions
- artifact coordinate rows
- the closed `Verb` vocabulary shared by plan compilation

Artifacts does **not** own action discovery, exact action selection, or
execution plans. Those are defined in [plans.md](./plans.md). This document
defines the artifacts that later plan compilation can select, and the verb
contracts that plan compilation later consumes.

The client model is table-shaped:

- `Artifacts.dimensions` is the ordered header row
- `Artifact.coordinates` is one ordered value row
- `Artifact.coordinate(name)` is a convenience lookup into that row

Without collections, the artifact set is simple:

- one row per top-level module object

With collections, the artifact set grows:

- top-level module object rows still exist
- each collection item adds its own rows

## Artifact Eligibility

An object occurrence is an eligible artifact if and only if it is:

1. a **top-level module object**, or
2. a **collection item**

There are no other artifacts.

### Top-Level Module Objects

A top-level module object is an object exposed directly by a module at the
workspace root.

Today that is usually one object per module. This wording intentionally leaves
room for future modules that expose several top-level objects.

Examples:

```dang
module go-sdk {
  pub go: Go! {
    Go()
  }
}

module release {
  pub release: Release! {
    Release()
  }
}
```

Here the eligible artifacts are:

```console
go
release
```

If a future module exposes more than one top-level object:

```dang
module release {
  pub sdkRelease: SdkRelease! {
    SdkRelease()
  }

  pub cliRelease: CliRelease! {
    CliRelease()
  }
}
```

then both are eligible artifacts, and they both belong to module
`release`.

### Collection Items

A collection item is always an eligible artifact.

Example:

```dang
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
```

If the current subset contains `TestFoo` and `TestBar`, then those items are
artifacts:

```console
go-test=TestFoo
go-test=TestBar
```

### Structural Glue

All other objects are structural glue.

They may still matter for:

- action reachability
- action naming
- collection traversal

But they do not become artifacts and they do not create non-collection
dimensions.

Example:

```dang
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

Here:

- `Go` is an artifact if it is a top-level module object
- `Tests` is structural glue
- `tests:run-bun` and `tests:run-nodejs` are action paths on `Go`

`Tests` is not an artifact.

## Dimensions And Coordinates

Artifacts always exposes one built-in dimension:

- `type`

Collections later add more dimensions such as `go-test` or `go-module`.

There is no synthesized non-collection dimension algebra in this design. A
nested non-collection object does not become a new dimension just because it
exists in the object graph.

### `type`

`type` is the generic built-in artifact classifier.

- Every artifact has a non-null `type`.
- The value is derived from the artifact's underlying GraphQL object type
  name, kebab-cased: `Go` → `go`, `SdkRelease` → `sdk-release`,
  `GoTest` → `go-test`.
- The derivation is purely syntactic. It does not consult the module name,
  the field name the object was exposed under, or any user configuration.

Examples:

```console
go
release
go-test
go-module
```

Cross-module collisions (two modules both exposing a top-level object of
the same GraphQL type name) are currently unspecified and tracked in
[Open Questions](#open-questions).

This is why these are legal:

```console
$ dagger list types
go
release
go-test
```

```console
$ dagger check --type=go lint
$ dagger check --type=go-test run
```

### Collection Dimensions

Collections add dimensions on top of the built-in `type` axis.

Example:

```console
type=go-test
go-module=./my-app
go-test=TestFoo
```

Top-level module object rows have `null` for collection dimensions that do not
apply to them.

### Coordinate Table Contract

Coordinates are scope-relative rows in a table:

- `scope.dimensions` is ordered and stable for a given scope
- `artifact.coordinates` has the same length and order
- `artifact.coordinates[i]` corresponds to `scope.dimensions[i]`
- `null` means that dimension does not apply to this row in this scope
- after `filterDimension("X")`, every returned row must have a non-null cell
  for `X`

Example root scope:

```console
dimensions = [type, go-test]
```

Rows:

```console
go top-level object      -> ["go", null]
js top-level object      -> ["js", null]
go test TestFoo item     -> ["go-test", "TestFoo"]
go test TestBar item     -> ["go-test", "TestBar"]
```

### Dimension Key Types

Dimension keys are always carried on the wire as strings.
`Artifact.coordinates` is `[String]!`. CLI flags pass strings.
`filterCoordinates` takes strings.

`ArtifactDimension.keyType` is metadata, not transport. It exists so the
engine and CLI can:

- parse a string value into a richer typed form for downstream use
- validate that a user-supplied value is well-formed
- render help text and flag formats appropriately

This implies a hard constraint that authors must respect:

- **Dimension keys must be stringifiable.** A `keyType` must have a
  canonical, unambiguous string representation. Strings, integers, booleans,
  and enums qualify. Custom scalars qualify if and only if they document a
  total round-trip with strings.
- **Object types do not qualify as dimension keys.** A dimension keyed on
  `Directory`, `Container`, or any other dagql object type is not coherent:
  these types carry identity beyond a string, and two values that print the
  same may not be equal.
- **Workspace load must validate keyType.** Declaring a dimension with a
  non-stringifiable keyType is a workspace-level error.

When a `keyType` represents something that is "string-on-the-wire, typed
elsewhere" — for example, a workspace-relative path that the engine later
lowers into a `Directory` argument for an action — the **lowering step** is
not part of this document. See [Open Questions](#open-questions).

## Verbs In Artifacts

Artifacts defines the closed `Verb` enum, but it does not expose a public
verb filter. Public verb selection starts at `Artifacts.plan(...)` in
[plans.md](./plans.md).

Artifacts still owns one structural notion that plan compilation depends on:

- whether an artifact exposes at least one reachable handler for a given verb

### Reachability Is Type-Level

Reachability is a walk over **types**, not over instances. Nothing is
materialized; no resolvers are invoked; no arguments are fabricated. The
walk is a static traversal of the GraphQL type graph rooted at the
artifact's underlying *type*, and it produces the same answer no matter
how many copies of that type exist or what state any particular instance
might hold.

This is a deliberate design constraint, not an accident. The rationale:

- **No invoking user code.** Verb exposure is used by plan compilation,
  introspection, and listing. It must be cheap and free of side effects.
  Walking over types is free; walking over instances would mean calling
  resolvers.
- **No fabricating arguments.** A walk over instances would have to choose
  argument values for any function it traversed. There is no general way
  to do that — there are no defaults for arbitrary user types — and
  guessing would silently produce wrong reachability answers.
- **No false positives.** "Reachable" must mean "the engine can produce a
  callable handler from this without further input." Anything that
  requires more input is not reachable; the doc would be lying if it said
  otherwise.

### The Walk

For an artifact whose underlying type is `T`:

1. Start at type `T`.
2. Walk recursively through:
   - declared object-valued fields
   - zero-arg object-valued functions (treating their declared return type
     as the next node in the walk)
3. At each visited type, **direct verb-annotated functions** count as
   reachable handlers for that artifact.
4. **Stop walking** at:
   - the next artifact boundary (a top-level type or a collection item type
     other than the one we started from)
   - members that require arguments
   - non-object fields
   - cross-artifact references

This means:

- a top-level module object can roll up handlers from nested structural glue
- a collection item can roll up handlers from nested structural glue inside
  that item
- a parent artifact does not absorb handlers from child collection-item
  artifacts

### Limitation: Handlers Behind Required Arguments

Because the walk stops at members that require arguments, any handler that
is only reachable through a required-argument traversal is **invisible to
verb reachability**.

For example:

```dang
type App {
  pub env(name: String!): Service! {
    ...
  }
}

type Service {
  pub healthCheck: Void! @check
}
```

`App.env(name)` requires an argument, so the walk stops there. `App` will
not be in CHECK scope, even though `Service.healthCheck` is annotated. This
is the intended behavior — there is no general way to enumerate the values
of `name` without running user code — but it is a real limitation that
authors should know about.

The supported way to expose parameterized variants as discoverable
artifacts is **collections**. If `Envs` is marked `@collection` with `keys`
and `get(name)`, each environment becomes its own artifact row with its own
coordinate, and the per-row handlers are reachable normally. Collections
exist precisely to turn "things keyed by an argument" into eligible
artifacts.

The handler-arity question (whether a verb-annotated handler may itself
take required arguments) is currently unspecified and tracked in
[Open Questions](#open-questions).

### Using Reachability

Define:

```text
hasVerb(artifact, V) =
  the artifact can reach at least one handler for V within its own boundary
```

`Artifacts.plan(verb: V, ...)` in [plans.md](./plans.md) only considers
artifacts for which `hasVerb(artifact, V)` is true. This document does not
define any additional public filtering surface on top of that structural rule.

### Example

```dang
type Go {
  pub lint: Void! @check

  pub tests: Tests! {
    Tests()
  }
}

type Tests {
  pub runBun: Void! @check
  pub generateFixtures: Directory! @generate
}
```

For the top-level `Go` artifact:

- `lint` is a reachable check handler
- `tests:run-bun` is a reachable check handler
- `tests:generate-fixtures` is a reachable generate handler

So:

```console
workspace.artifacts.plan(verb: CHECK)    may include the Go artifact
workspace.artifacts.plan(verb: GENERATE) may include the Go artifact
```

## CLI

Artifacts exposes one built-in listing subcommand:

- `dagger list types`

That is a UI alias over the built-in `type` dimension.

For all other dimensions, the CLI uses the dimension name directly:

- `dagger list go-test`
- `dagger list go-module`

Extension docs may define additional CLI aliases that lower to the same
underlying dimension filters. Those aliases are command syntax, not new
dimensions. For example, [collections.md](./collections.md) defines
`--<collection-type>` as sugar for presence filtering on the corresponding
collection item dimension.

### Discovery

```console
$ dagger list --help
Usage:
  dagger list <dimension> [flags]

Available dimensions:
  types     List available artifact types
  go-test   List values for the artifact dimension "go-test"
```

### Value Listing

Value listing is derived from `items()`, not from `dimensions()`:

1. apply the active filters to the current scope
2. select rows where the target dimension is non-null
3. read that coordinate from each row
4. print the distinct non-null values in stable order

Examples:

```console
$ dagger list types
go
js
go-test
```

```console
$ dagger list go-test
TestFoo
TestBar
```

### Filter Flags

Valued dimension flags still use repeatable valued flags and lower to
`filterCoordinates(...)`:

```console
--type=<value>
--go-test=<value>
--go-module=<value>
```

No comma-separated values.

Presence filtering is part of the API:

```text
filterDimension("go-test")
```

Coordinate filtering is a separate operation:

```text
filterCoordinates("go-test", ["TestFoo", "TestBar"])
```

CLI parsing should normalize command-specific sugar into this API before
evaluation. After normalization, Artifacts only sees real dimensions such as
`type`, `go-test`, and `go-module`.

The generic CLI surface only guarantees valued flags. Extension docs may add
command-level sugar for presence filtering. Collections does this with
`--<collection-type>`.

Examples:

```console
$ dagger check --type=go lint
$ dagger check --type=go-test run
$ dagger check --go-test=TestFoo run
$ dagger check --go-module=./my-app --go-test=TestFoo run
```

If a more specific dimension already determines the artifact kind, `--type` is
redundant and may be omitted.

These are equivalent:

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

Coordinate filters use repeatable valued flags and compose in the obvious way:

- values within one dimension are **OR**
- different dimensions are **AND**

Examples:

```console
$ dagger check --type=go --type=js lint
```

means:

- type is `go`
- or type is `js`

while:

```console
$ dagger check --type=go-test --go-test=TestFoo run
```

means:

- type is `go-test`
- and go-test is `TestFoo`

Presence filtering means:

- keep rows where the given coordinate is non-null

Coordinate filtering means:

- keep rows whose coordinate value matches one of the given values

Verb selection is not part of the Artifacts filter algebra. It happens later,
inside `Artifacts.plan(verb, include, exclude)` in [plans.md](./plans.md).

These combine in the obvious way:

- `filterDimension("go-test").filterCoordinates("go-test", ["TestFoo"])`
  is equivalent to `filterCoordinates("go-test", ["TestFoo"])`
- `filterCoordinates("type", ["go", "js"])` means `go OR js`

For CLI usage, users normally spell the same intent with a valued filter:

```console
$ dagger check --type=go-test run
```

Filters only narrow rows. They do not re-order dimensions.

Verb filters preserve the built-in `type` dimension and keep any collection
dimensions that still apply to at least one retained row.

## Acceptance Criteria

These examples intentionally split into two layers:

1. **UI output tests** — the CLI is given a fixed resolved `Artifacts` scope;
   every byte of input and output matters.
2. **Artifact and dimension detection tests** — a schema is given; only the
   resulting rows and dimensions matter. CLI formatting is noise.

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

### Artifact And Dimension Detection Tests

#### One Top-Level Object Per Module

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

#### Several Top-Level Objects In One Module

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

This test proves that several top-level objects in one module are
distinguishable without a separate `module` dimension.

#### Structural Glue Does Not Create Rows

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

There is no extra `tests` row and no extra non-collection dimension.

#### Collection Items Add Rows And Dimensions

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

Expected:

```console
dimensions: { type, go-test }
rows:
  { type=go,      go-test=null }
  { type=go-test, go-test=TestFoo }
  { type=go-test, go-test=TestBar }
```

#### Verb Projection Uses Reachable Handlers Inside The Artifact Boundary

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

This test proves that nested structural glue contributes verb reachability
without becoming rows or dimensions.

## Open Questions

These items are deliberately unresolved in the current spec. Each one needs
team input before it can move into [Locked Decisions](#locked-decisions).

### Q1. Cross-module `type` collisions

**Problem.** The `type` dimension value is derived from the underlying
GraphQL type name, kebab-cased. If two different modules each expose a
top-level object of GraphQL type `Release`, both produce artifact rows with
`type=release`. Today's spec does not say what happens.

Options:

- **Workspace-unique by construction.** Validate at workspace load time;
  error on collision. Forces module authors to use distinct type names.
- **Allow collisions; add a `module` dimension.** Users disambiguate with
  `--module=...`. Adds a new built-in dimension.
- **Namespace on collision.** Auto-prefix with the module name only when a
  collision occurs (`mymod/release`). Ugly but minimal.

**Recommendation:** workspace-unique by construction is the simplest and
catches the issue early. Worth confirming this is acceptable to module
authors.

### Q2. Handler arity

**Problem.** The walk stops at members that require arguments — but is a
verb-annotated handler with required arguments still counted as a reachable
handler? Every example in this doc uses nullary handlers. The rule for
parameterized handlers is unspecified.

Options:

- **A. Handlers must be nullary.** Symmetric with the traversal rule.
  Restrictive: parameterized checks cannot be expressed as standard verbs.
- **B. Handlers may take any arguments; downstream layers handle
  invocation.** "Reachable" becomes "advertised, may not be directly
  invokable from a bare verb command." Power, at the cost of a leakier
  abstraction.
- **C. Handlers may take arguments only if every required argument has a
  default.** GraphQL-native middle ground. Common convention.

**Recommendation:** C, plus an explicit pointer to the CLI invocation
contract for any non-default arguments. Reading A is too restrictive for
real-world checks (linters with strictness, tests with seeds). Reading B
hides the question.

### Q3. Scope-relative `Artifact` identity

**Problem.** With the current contract, an `Artifact` is implicitly a
`(scope, coordinates)` projection — the same underlying object, viewed
through two different scopes, is two different `Artifact` values, with
different coordinate-row shapes if the dimension sets differ. There is no
stable cross-scope handle.

Implications worth deciding explicitly:

- **Caching.** Every filter call mints fresh dagql IDs for every retained
  row. UIs that narrow scope on every keystroke produce a cache footprint
  proportional to (number of rows) × (number of filter steps) per
  interaction.
- **Equality.** Tooling cannot ask "is this the same artifact as that one
  across scopes?" by identity. It must compare content.
- **Bookmarking.** No way to refer to "this specific artifact" persistently
  outside its current scope.

Options:

- **Accept the projection model and document it.** State explicitly that
  `Artifact` is a `(scope, coordinates)` projection, not an entity. Tooling
  that needs cross-scope identity must derive it from coordinates plus
  workspace state.
- **Introduce a stable underlying handle.** Add a separate concept (e.g.
  `Artifact.handle: ArtifactHandle!`) that is stable across scopes and
  identifies the underlying object regardless of how it was projected.

**Recommendation:** accept the projection model for now and document it
clearly. Reconsider if a concrete tooling use case needs cross-scope
identity.

### Q4. Coordinate-to-argument-value lowering

**Problem.** Filtering and listing are well-defined on string coordinates.
Invoking an action that takes a dimensioned value as a typed argument
(e.g. `dagger check --go-module=./my-app lint` where `lint(module: Directory!)`)
requires turning the coordinate string `./my-app` into the typed value the
action expects. No such hand-off is specified.

This is missing infrastructure, not just missing prose. It probably belongs
in [plans.md](./plans.md), but at minimum this document should call out the
hand-off so the gap is visible.

**What needs deciding:**

- Where the lowering specification lives.
- Whether `keyType` carries enough information to perform the lowering, or
  whether dimensions need a separate "lowering" hook.
- How normalization works (`./my-app` vs `my-app/` vs absolute path) and
  whether two spellings can be coordinate-equal.

### Q5. `@collection` contract

**Problem.** This document leans on `@collection` as load-bearing
infrastructure but does not define it. The intended semantics — laid out in
[collections.md](./collections.md) — must guarantee at least:

- a stable shape (e.g. `keys: [K!]` and `get(key: K!): T!`)
- key uniqueness within a collection occurrence
- bounded enumeration cost for `keys`
- a fixed item type `T` per collection occurrence (so type-level
  reachability is sound)
- no holes (every `k in keys` resolves)

**What needs deciding:** that collections.md formally exposes these
guarantees, and that this document references the contract by name rather
than reinventing it.

### Q6. Heterogeneous named substructures

**Problem.** The current eligibility set, `{top-level module objects} ∪
{collection items}`, does not cover heterogeneous named substructures:

```dang
type Release {
  pub sdk: SdkRelease!     # named, heterogeneous, not a collection
  pub cli: CliRelease!
  pub docs: DocsRelease!
}

type App {
  pub frontend: Frontend!  # named, heterogeneous, different types
  pub backend: Backend!
}
```

These are not collections (not homogeneous, not keyed, no `get(name)`) and
they are not top-level module objects. Under the current rule they are
structural glue, which means there is no way to spell "check just the
frontend" or "ship just the SDK release" without restructuring the module.

Workarounds, all bad:

- **Split into separate top-level modules.** Loses the conceptual
  grouping. There is no longer a `Release` you can ship as a unit.
- **Force into a collection.** Requires a homogeneous item type;
  `Frontend | Backend` does not fit.
- **Treat as glue and check the parent.** Loses the ability to scope to a
  single child.

**Options:**

- **Accept the limitation; document it.** Authors who hit this case must
  refactor.
- **Add a `@artifact` directive.** A third eligibility rule: an
  object-valued field marked `@artifact` is an eligible artifact in its
  own right. The three eligibility rules collapse into "the author marked
  this as a thing" with three syntactic forms: top-level (implicit),
  `@collection` (keyed-homogeneous), `@artifact` (heterogeneous-named).
  See [Alternatives Considered](#alternatives-considered).

**Recommendation:** worth a real design conversation. The current
restriction may be acceptable for Dagger's existing module ecosystem but
becomes more painful as richer module shapes emerge.

### Q7. Display data on artifacts

**Problem.** Earlier drafts of this schema included an `Artifact.fields`
field returning "already-materialized non-object fields on the underlying
object, for inspection." It has been removed because no concrete use case
was tied to it and the "already-materialized" semantics did not have a
clean definition in dagql (every dagql field is backed by a resolver).

**What needs deciding:** whether there is a real use case for displaying
non-coordinate object data on artifacts (e.g. `dagger list --wide`, a TUI
inspector, structured output), and if so, what the cost model is for
reading those values. If the answer is yes, the field returns; if no, it
stays out.

## Alternatives Considered

### `@artifact` directive as a third eligibility rule

**Idea.** Replace the two-rule eligibility set with three rules: top-level
module object (implicit), `@collection` item (keyed-homogeneous), or any
object-valued field marked `@artifact` (heterogeneous-named). All three
collapse to "the author marked this as a thing."

**For.** Covers heterogeneous named substructures (`Release.sdk`,
`App.frontend`) which the current rule does not. Treats artifact-ness as
intent-derived rather than graph-shape-derived. Composes orthogonally with
collections.

**Against.** Adds an annotation for module authors to learn. Risks artifact
proliferation in workspaces with deep object graphs. The current ecosystem
mostly does not need it because Dagger modules tend to expose one
top-level object plus collections.

**Status.** Not adopted. Tracked in [Open Questions](#open-questions) Q6
because the absence may become painful as the module ecosystem grows.

### User-defined verbs

**Idea.** Allow module authors to declare new verbs (e.g. `@audit`,
`@benchmark`) that the CLI exposes as new commands.

**For.** Lets every workspace have exactly the lifecycle vocabulary it
wants.

**Against.** Defeats the point of standardization. Each new verb would
need its own CLI command, its own UX, its own integration story; the CLI
can no longer be generic; tooling that wants to build on top of "every
Dagger module supports check" loses that ground. If verbs are negotiable,
they are no longer commitments.

**Status.** Rejected. Verbs are deliberately a closed, language-level set.
See [What Is a Verb?](#what-is-a-verb).

### Dimensions as data, not schema

**Idea.** Allow the dimension set to shrink in response to filtering,
hiding columns whose retained rows are all null.

**For.** Cleaner table rendering by default. Less noise in narrowed scopes.

**Against.** Breaks composability. Two filter chains whose user intent
commutes can produce different outcomes if intermediate filters drop a
dimension that a later filter needs. This is a real footgun for both users
and tooling.

**Status.** Rejected. Dimensions stay schema-stable; empty-column hiding
belongs in the CLI presentation layer.

### Non-stringifiable dimension keys

**Idea.** Allow dimensions keyed on dagql object types like `Directory` or
`Container`, with the wire format being some opaque ID.

**For.** Lets dimensions carry rich typed values directly.

**Against.** Object types carry identity beyond a string; two values that
print the same may not be equal. Round-tripping through CLI flags is not
possible. Equality is undefined. Coordinate uniqueness becomes ambiguous.

**Status.** Rejected. Dimension keys are stringifiable. See
[Dimension Key Types](#dimension-key-types).

### Replacing `dagger call` with verb commands

**Idea.** Migrate `dagger call` and `dagger shell` to use the artifact
selection model, replacing their existing path syntaxes.

**For.** A single CLI selection vocabulary across all commands.

**Against.** `dagger call` and the verb commands serve genuinely different
audiences. `dagger call` is for arbitrary function invocation with
arbitrary arguments — power users who want a thin wrapper around the
schema. The verb commands are for standardized lifecycle operations — CI,
UIs, and users who want commands that Just Work without per-function
configuration. Forcing them to share vocabulary risks making both worse.

**Status.** Not adopted. The verb commands consume the artifact model;
`dagger call` and `dagger shell` remain as-is. See [Non-Goals](#non-goals).

## Locked Decisions

- Eligible artifacts are only:
  - top-level module objects
  - collection items
- Ordinary nested objects are structural glue. They are not artifacts and
  do not create non-collection dimensions.
- `type` is the generic built-in artifact dimension.
- There is no synthesized non-collection dimension algebra.
- Artifacts does not expose a public verb filter. Verb selection starts at
  `Artifacts.plan(...)` in [plans.md](./plans.md).
- Verb exposure is structural and side-effect free. Determining whether an
  artifact exposes a verb never executes user code.
- Reachability is type-level and side-effect free. The walk never invokes
  user code or fabricates argument values.
- The verb set (`CHECK`, `GENERATE`, `SHIP`, `UP`) is closed. New verbs are
  language-level changes.
- Dimension keys are stringifiable. Object types do not qualify as dimension
  keys.
