# Artifacts

## Status: Designed

Depends on: Workspace plumbing (done)

## Table of Contents

- [What Problem Does This Solve?](#what-problem-does-this-solve)
- [Core Model](#core-model)
- [Artifact Eligibility](#artifact-eligibility)
- [Dimensions And Coordinates](#dimensions-and-coordinates)
- [Verb Scopes](#verb-scopes)
- [CLI](#cli)
- [Schema](#schema)
- [Filter Algebra](#filter-algebra)
- [Acceptance Criteria](#acceptance-criteria)
- [Locked Decisions](#locked-decisions)

## What Problem Does This Solve?

Today the platform mostly sees workspaces, modules, and functions.

Modules expose object graphs, but those objects do not have a stable public
selection model. You can call functions through them, but the objects
themselves are mostly invisible. Every command then grows its own way of
talking about nested structure:

- `foo:bar` in `dagger check`
- `foo bar` in `dagger call`
- `foo | bar` in `dagger shell`
- `foo().bar()` in dang

Artifacts fixes that by giving the platform a small set of real object rows it
can select, inspect, and later act on.

In this design:

- top-level module objects are artifacts
- collection items are artifacts
- ordinary nested objects are not artifacts

Nested non-collection structure still matters. It just shows up as action paths
inside an artifact, not as separate artifact rows.

## Core Model

`Workspace.artifacts` is a filterable, introspection-driven view over the
workspace's artifact rows.

Artifacts owns:

- artifact eligibility
- artifact scopes
- artifact dimensions
- artifact coordinate rows
- structural verb projections over artifact rows

Artifacts does **not** own action discovery or execution plans. Those are
defined in [plans.md](./plans.md). This document only defines enough verb-scope
behavior to decide which artifact rows stay in scope for a verb.

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

There are no other artifact rows.

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

Here the eligible artifact rows are:

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

then both are eligible artifact rows, and they both belong to module
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
artifact rows:

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

But they do not become artifact rows and they do not create non-collection
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

- `Go` is an artifact row if it is a top-level module object
- `Tests` is structural glue
- `tests:run-bun` and `tests:run-nodejs` are action paths on `Go`

`Tests` is not an artifact row.

## Dimensions And Coordinates

Artifacts always exposes one built-in dimension:

- `type`

Collections later add more dimensions such as `go-test` or `go-module`.

There is no synthesized non-collection dimension algebra in this design. A
nested non-collection object does not become a new dimension just because it
exists in the object graph.

### `type`

`type` is the generic built-in artifact classifier.

- Every artifact row has a non-null `type`.
- The value is the CLI-cased artifact type name.

Examples:

```console
go
release
go-test
go-module
```

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

## Verb Scopes

Artifacts has two scope kinds:

- root discovery scope: `workspace.artifacts`
- verb-projected scopes: `workspace.artifacts.filterVerb(CHECK)`,
  `workspace.artifacts.filterVerb(GENERATE)`, and so on

`filterVerb` is structural. It does not compile or run anything.

It only answers:

- which artifact rows stay in scope for this verb?

### Reachable Verb Handlers

An artifact row is in verb scope `V` if its underlying object can reach at
least one handler for `V` within its own artifact boundary.

Reachability for one artifact works like this:

1. Start at that artifact's root object.
2. Walk recursively through:
   - object-valued fields
   - zero-arg object-valued functions
3. At each visited object, direct verb-annotated functions count as reachable
   handlers.
4. Stop walking at:
   - the next artifact boundary
   - members that require arguments
   - non-object fields
   - cross-artifact references

This means:

- a top-level module object can roll up handlers from nested structural glue
- a collection item can roll up handlers from nested structural glue inside
  that item
- a parent artifact does not absorb handlers from child collection-item
  artifacts

### Composition

Define:

```text
hasVerb(artifact, V) =
  the artifact can reach at least one handler for V within its own boundary
```

Then:

```text
filterVerb(V) keeps rows where hasVerb(row, V)
```

Verb filters compose by intersection:

```text
filterVerb(CHECK).filterVerb(GENERATE)
```

means:

- keep rows that can reach at least one check handler
- and can also reach at least one generate handler

Composition is:

- order-independent
- idempotent
- evaluated over the same underlying artifact set

So:

```text
filterVerb(CHECK).filterVerb(GENERATE) ==
filterVerb(GENERATE).filterVerb(CHECK)
```

and:

```text
filterVerb(CHECK).filterVerb(CHECK) == filterVerb(CHECK)
```

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
workspace.artifacts.filterVerb(CHECK)    keeps the Go row
workspace.artifacts.filterVerb(GENERATE) keeps the Go row
workspace.artifacts
  .filterVerb(CHECK)
  .filterVerb(GENERATE)                  also keeps the Go row
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

### Compatibility Input

For compatibility, the CLI may also accept:

```console
<type>:<action-path>
```

as shorthand for:

```console
--type=<type> <action-path>
```

Examples:

```console
$ dagger check go:lint
$ dagger check go-test:run
```

These are input aliases only. The CLI does not need to print this notation in
its primary output.

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
  Keep only artifact rows that can reach at least one handler for the given
  verb within their own artifact boundary.
  Does not add the verb as a dimension.
  """
  filterVerb(verb: Verb!): Artifacts!

  """
  Ordered filterable dimensions for the current scope.
  Always includes `type`. May include collection dimensions such as `go-test`
  or `go-module`.
  """
  dimensions: [ArtifactDimension!]!

  """Artifact rows matching the current filters."""
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

"""One artifact row in the workspace."""
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

  """
  String form of the value. Must match normal CLI rendering for the field's
  type.
  """
  display: String!

  """Structured JSON form, for SDKs and tools."""
  json: JSON!

  """Schema type of this field."""
  type: TypeDef!
}
```

## Filter Algebra

Coordinate filters use repeatable valued flags and compose in the obvious way:

- values within one dimension are **OR**
- different dimensions are **AND**
- verb filters are also **AND**

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

## Locked Decisions

- Eligible artifact rows are only:
  - top-level module objects
  - collection items
- Ordinary nested objects are structural glue. They are not artifact rows and
  do not create non-collection dimensions.
- `type` is the generic built-in artifact dimension.
- There is no synthesized non-collection dimension algebra.
- Verb scopes are structural row filters. They do not add new dimensions and
  they do not execute anything.
