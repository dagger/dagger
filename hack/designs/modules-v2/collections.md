# Collections

## Status: Designed; prior prototype exists (`collections` branch, predates current ordering)

Depends on: [Execution Plans](./plans.md)

## Table of Contents

- [Problem](#problem)
- [Proposal](#proposal)
- [Use Cases](#use-cases)
- [Interfaces](#interfaces)
- [How Collections Expand Artifacts](#how-collections-expand-artifacts)
- [How Collections Expand Plans](#how-collections-expand-plans)
- [Checks and Generators](#checks-and-generators)
- [Implementation](#implementation)
- [General Maps](#general-maps)

## Problem

Modules can discover dynamic sets of related objects (tests by name, packages
by path, services by label). But they can't present them to clients without
losing features like `dagger check`, `dagger call` keyed selection, filtering,
or batching. Keys aren't first-class in the schema, so every module invents
ad hoc accessors, and tooling can't offer these features generically.

## Proposal

A collection is a keyed set of objects, defined on ordinary module types
using `@collection` / `+collection` and projected by the engine into a
synthetic public type with a small standard algebra: `keys`, `list`, `get`,
`subset`, and `batch`.

This proposal is intentionally narrow:

- Collections are keyed sets of objects. A broader map abstraction may follow,
  but is out of scope.
- They layer semantics onto existing objects, not a new type kind.
- They are declared on object types with `@collection` / `+collection`, with
  `keys` and `get` by convention or `@keys` / `@get` by override.
- They add keyed traversal, selection, and batching — nothing else.

## Use Cases

### Test Selection

A test suite can publish its tests as a collection keyed by test name. Users
and tools select tests by key instead of relying on ad hoc command flags or
list position.

This is the motivation for collection-aware filtering in `dagger check` and
`dagger generate`: a user should be able to say "run these tests" by naming
them directly.

### Test Splitting

Collections make test splitting precise because a subset of tests can be
represented explicitly as another collection.

`subset(keys)` is the key operation here. It turns one collection into an exact
key-selected subset while preserving collection shape. That lets tooling
divide tests into buckets without losing the collection abstraction.

`batch` complements this by giving a collection a place to implement efficient
execution over a selected subset.

### One Module, Many Projects

Collections let one installed module publish a dynamic set of related projects.

In practice, a single repository often contains many apps, packages, modules,
sites, or services. Those sets are usually discovered at runtime and are keyed
by names or paths that matter to users.

Collections give modules a standard way to publish those dynamic sets and let
clients select one project by key or operate on subsets.

## Interfaces

### Module Definition

Collections are defined on ordinary object types by annotating the type itself
as a collection.

Each collection type has:
- one effective `keys` field
- one effective `get` function

The type annotation is required. Member annotations are optional.

- If a collection type exposes a field named `keys`, that field is the
  effective `keys` field by default.
- If a collection type exposes a function named `get`, that function is the
  effective `get` function by default.
- `@keys` and `@get` are only needed to override those default names.

Canonical schema shape:

```graphql
"""Collection of Go modules keyed by workspace path."""
type GoModules @collection {
  """All keys currently present in the collection."""
  keys: [WorkspacePath!]!

  """Resolve one module by workspace path."""
  get(
    """Workspace path to resolve."""
    path: WorkspacePath!
  ): GoModule!
}
```

Non-standard names:

```graphql
"""Collection of Go modules keyed by workspace path."""
type GoModules @collection {
  """All keys currently present in the collection."""
  paths: [WorkspacePath!]! @keys

  """Resolve one module by workspace path."""
  module(
    """Workspace path to resolve."""
    path: WorkspacePath!
  ): GoModule! @get
}
```

Rules:
- `@collection` / `+collection` is required on the type itself
- the effective `keys` field enumerates the collection keyspace
- the effective `get` function resolves one item by key
- `keys` must be an exposed field
- `get` must be an exposed function
- if `@keys` is absent, the exposed field named `keys` is used
- if `@get` is absent, the exposed function named `get` is used
- if `@keys` or `@get` is present, it overrides the default name-based
  convention
- only scalar and enum input types are valid as collection keys; this includes
  builtin scalars, custom scalars, and enums; object, input-object, interface,
  and list types are not valid collection key types
- the effective `keys` field returns `[KeyType!]!`
- the effective `get` function accepts exactly one non-null `KeyType` argument
  and returns a non-null object
- a collection must have exactly one effective `keys` field and exactly one
  effective `get` function
- keys should be unique within a collection

Collection validity is enforced in two stages. At module load time, the engine
validates structure: whether the type is marked as a collection, whether there
is exactly one effective `keys` field and one effective `get` function, and
whether their signatures are valid. At runtime, the engine validates behavior
when the collection is used: if `keys` advertises a key that `get` cannot
resolve, collection operations fail at the point of use.

Collections describe how a dynamic set is addressed and traversed. They do not
by themselves add mutation, execution, or other higher-level behavior.

Any exposed function on the collection type beyond the effective `keys` field and
`get` is automatically re-homed under the synthetic `batch` namespace (see
[Batch Namespace](#batch-namespace)). Module authors define batch operations
as ordinary functions on the collection type; the engine handles projection.

Illustrative authoring examples:

```dang
type GoTests @collection {
  pub keys: [String!]

  pub get(name: String!): GoTest! {
    GoTest(name: name)
  }
}
```

```go
// +collection
type GoTests struct {
	Keys []string
}

func (tests *GoTests) Get(name string) *GoTest {
	return &GoTest{Name: name}
}
```

```typescript
import { collection, func, object } from "@dagger.io/dagger";

@object()
@collection()
class GoTests {
  keys: string[] = [];

  @func()
  get(name: string): GoTest {
    return new GoTest(name);
  }
}
```

```python
from dagger import collection, function, object_type


@object_type
@collection
class GoTests:
    keys: list[str]

    @function
    def get(self, name: str) -> "GoTest":
        return GoTest(name=name)
```

### DagQL Schema

The raw module-defined collection object stays hidden from clients.

Instead, a field or function returning a collection projects to a synthetic
collection object with engine-defined standardized members. The projected field
keeps the original module name.

Example internal shape:

```graphql
"""Root object exposing a collection of Go modules."""
type Go {
  """Collection of Go modules."""
  modules: GoModules!
}
```

Example public projection:

```graphql
"""Root object exposing the projected public collection."""
type Go {
  """Projected collection of Go modules."""
  modules: GoModules!
}

"""Synthetic public projection of a Go module collection."""
type GoModules {
  """Keys in the current subset, in stable collection order."""
  keys: [WorkspacePath!]!

  """Items in the current subset, in the same order as `keys`."""
  list: [GoModule!]!

  """Resolve one item in the current subset by key."""
  get(
    """Key to resolve."""
    key: WorkspacePath!
  ): GoModule!

  """Restrict the collection to an exact subset of keys."""
  subset(
    """Keys to retain from the current subset."""
    keys: [WorkspacePath!]!
  ): GoModules!

  """Type-specific efficient operations over the current subset."""
  batch: GoModules_Batch!
}

"""Type-specific batch operations over the current subset."""
type GoModules_Batch {
  """Illustrative only: efficiently evaluate checks over the current subset."""
  checks: CheckGroup!
}
```

Rules:
- projection keeps the original field/function name
- projection applies to both fields and functions returning collections
- public collection types are synthetic and engine-defined
- item types are unchanged; collection-relative identity stays on the
  collection, not the item
- list order preserves the order of the effective `keys` field
- `get` errors on an unknown key
- `subset` is exact key selection, not a predicate language; it preserves
  parent key order and errors on unknown or duplicate keys

The engine materializes `list` by iterating keys and calling the backing
`get`. `subset` narrows the keyspace while preserving collection shape.

### Collection Algebra

The synthetic collection object defines a small algebra:
- `keys` describes the current subset's keyspace
- `list` materializes the current subset's items
- `get(key)` materializes one item from the current subset
- `subset(keys)` narrows the current subset while preserving collection shape

Expected laws:
- `c.subset(keys: c.keys)` is equivalent to `c`
- `c.subset(keys: ks).keys` returns `ks` in parent order
- `c.subset(keys: ks).list` returns the items for `ks` in parent order
- `c.subset(keys: ks).get(k)` errors unless `k` is in `ks`

This keeps subset transport, exact-key access, and enumeration coherent across
both single-engine and cross-engine execution.

### Batch Namespace

`batch` is not part of the core collection algebra. It is a type-specific
namespace for collection-level operations that can execute more efficiently
over the current subset than invoking the equivalent item-level operation one
item at a time.

The synthetic `batch` type is derived from the backing collection type. The
engine identifies the effective `keys` field and effective `get` function;
every other exposed function on the collection type is re-homed under `batch`.
Non-function fields are not projected publicly, except for the effective `keys`
field.

For example, a collection of test definitions may expose a `runTests` function
alongside `keys` and `get`. The engine projects `runTests` under `batch`,
so clients call `c.batch.runTests` to run one `go test` process over many
selected tests rather than one process per test.

Important boundaries:
- `batch` operates on the current subset, so `c.subset(keys: ks).batch`
  sees only `ks`
- `batch` is type-specific; different collection types may expose different
  methods under it, and their meaning is outside the core collection algebra

## How Collections Expand Artifacts

Collections extend the selector model defined in [artifacts.md](./artifacts.md).
They do not replace it.

A collection occurrence contributes:
- a new public selector dimension named by the collection's item type
- selector values from the collection's current keys
- extra artifact coordinates when that dimension is needed for uniqueness in
  the current scope

Example:

```console
$ dagger check --help
  --type=<name>

$ dagger check --help
  --type=<name>
  --go-test=<name>
```

Base Artifacts scope:

```text
workspace.artifacts
  .filterVerb(CHECK)
  .filterDimension("type", ["go-test"])
```

Expanded by a collection:

```text
workspace.artifacts
  .filterVerb(CHECK)
  .filterDimension("type", ["go-test"])
  .filterDimension("go-test", ["TestFoo"])
```

The base dimensions remain valid. Collections add new selector space rather
than introducing a parallel targeting model.

## How Collections Expand Plans

Plan compilation still starts from an Artifacts scope. Collections change plan
compilation in two places:

1. collection selector dimensions lower to `subset(keys: ...)` on matching
   collections
2. collection `batch` behavior may replace one-item-at-a-time expansion

Example:

```text
workspace.artifacts
  .filterVerb(CHECK)
  .filterDimension("type", ["go-test"])
  .filterDimension("go-test", ["TestFoo", "TestBar"])
  .check
```

Without collection-aware batch behavior, the plan may contain one action per
selected item.

With collection-aware batch behavior, the plan may instead contain one action
over the filtered subset, equivalent to:

```text
go.tests
  .subset(keys: ["TestFoo", "TestBar"])
  .batch
  .runTest
```

### Typedefs

This section answers how collections are represented in the schema and
introspection model, as distinct from the public client-facing API they
project to.

Collections should be represented in the type system as metadata layered on an
object, not as a peer kind.

That means:
- projected collections still have `TypeDef.Kind = OBJECT`
- projected collections still populate `TypeDef.AsObject`
- projected collections additionally populate `TypeDef.AsCollection`

This keeps collection-unaware clients simple: they can continue treating a
collection projection as an ordinary object. Collection-aware traversal
surfaces can look for `AsCollection` when they need keyed-hop behavior.

```go
// CollectionTypeDef describes collection semantics layered on top of an object type.
type CollectionTypeDef struct {
  // KeyType is the type accepted by get(key) and subset(keys: ...).
  KeyType *TypeDef

  // ValueType is the type returned by get() and enumerated by list.
  ValueType *TypeDef

  // BatchType is the type returned by batch.
  BatchType *TypeDef
}
```

This document does not introduce `TypeDefKindCollection`.

### Reserved Names

Leading `_` is reserved for Dagger-injected fields and arguments.

The core synthetic collection object in this document uses normal names
(`keys`, `list`, `get`, `subset`, `batch`) because that object is fully
engine-owned and does not expose raw module-defined collection methods.

The reservation still matters as a general rule for future Dagger-injected
members and other projection escape hatches. Module authors should not define
public fields or arguments with leading `_`.

## Checks and Generators

This section describes how collections interact with the existing check and
generator feature. These rules are specific to that feature and are not part of
the core collection interfaces defined above.

### Checks

Collections affect checks through generated filters and through the collection's
effective check set.

The base selector model lives in [artifacts.md](./artifacts.md). Collections do
not replace it. They add keyed dimensions and batch behavior on top of
the pre-existing built-in `type` dimension.

#### Filter Model

Check filters shape the effective check tree before listing or execution.

Collection-provided selector dimensions still use
`--<dimension>=<value>`, repeatable. Those dimension flags are named by
**item type** (singular), not collection type. Dedicated provenance filters
such as `--path` remain separate; collections do not change them.

Each flag points to `dagger list` for discovery:

```console
$ dagger check --help
  --type=<name>         Filter by artifact type (see: dagger list types)
  --go-module=<name>    Filter by go module (see: dagger list go-module --check)
  --go-test=<name>      Filter by go test (see: dagger list go-test --check)
```

Filter names are derived mechanically from item type names using Dagger's
existing CLI casing rules.

Each value gets its own flag instance. Comma-separated values in a single
flag occurrence are forbidden (or treated as a literal key containing a
comma).

Examples:

```console
$ dagger check --type=go --type=nodejs
$ dagger check --go-test=TestFoo --go-test=TestBar --go-module=./myapp/app2
```

The built-in `type` axis remains available alongside these collection
dimensions:

```console
$ dagger check --type=go-test --go-test=TestFoo run
$ dagger check --go-test=TestFoo run
```

The second form is legal because filtering by `--go-test=...` already implies
that the selected rows are `go-test` artifacts.

These dimension filters are scope-relative constraints, not unique selectors.

- A filter narrows every matching collection occurrence to the given
  key subset.
- A filter may match zero, one, or many occurrences.
- To narrow further, combine filters.

If function-path selectors remain temporarily for CLI compatibility, filters do
not depend on them. They should be treated as a thin transitional alias over
the typed selector model, not as a separate long-term targeting system.

Type renames are CLI-breaking for these generated filters.

#### Listing and Discovery

`dagger list` is the exploration surface for discovering filter values. It can
also be projected through a verb when needed to match a verb-local selector
scope:

```console
$ dagger list                      # available dimensions with key types
$ dagger list types                # artifact types
$ dagger list go-module --check    # go modules in check scope
$ dagger list go-test --check      # all go tests in check scope
$ dagger list go-test --check \
    --go-module=./myapp/app2       # go tests filtered by module
```

`dagger list` uses the same filter flags as `check`/`generate`/`ship`/`up`.

Listing rules:

- `dagger list <dimension>` lists items in that dimension.
- Active filters from other dimensions are applied first.
- Parent/child relationships between different filter dimensions are
  intentionally flattened.
- Output is unique values in stable order.

`dagger check -l` and `dagger generate -l` use the table-capable action
listing defined in [plans.md](./plans.md). Collection dimensions simply become
more columns when they are needed to distinguish the listed rows.

#### Batch Shadowing

Collections affect checks in two places:

- item checks, defined on the collection's item type
- batch checks, defined on the collection's `batch` type

A collection contributes one effective check set.

- If an item check and a batch check have the same name, the batch check
  shadows the item check.
- If no batch check exists for a name, the item check remains in the effective
  check set.

Execution follows the same rule.

- A shadowing batch check runs once for the current collection subset.
- An unshadowed item check runs once per item in the current collection
  subset.

For example, suppose `go.tests` has item checks `runTest` and `lint`, and
`go.tests.batch` defines `runTest` but not `lint`.

```console
$ dagger check -l
GO TEST   ACTION
TestFoo   lint
TestFoo   run-test
TestBar   lint
TestBar   run-test

$ dagger check --go-test=TestFoo --go-test=TestBar run-test
# runs once using go.tests.batch.runTest over the filtered subset

$ dagger check --go-test=TestFoo --go-test=TestBar lint
# runs once per filtered item using the item type's lint check
```

### Generators

Generators follow the same traversal rules as checks. The collection-aware
filtering and targeting described above applies to both.

## Implementation

This design is intended to land as one primary implementation unit:

- **PR:** `collections: project and integrate collections`
- **API:** `TypeDef.AsCollection`, projected `<Collection>` types,
  projected `<Collection>_Batch` types, collection-driven `Artifacts`
  dimensions
- **UI:** collection traversal in `dagger call` / `dagger shell` /
  `dagger functions`, plus collection-aware `dagger list`, `dagger check`,
  and `dagger generate`

Included in this unit:

- collection metadata and validation
- public schema projection for collection and batch types
- module authoring/runtime and generated-client support
- collection selector dimensions in the Artifacts model
- collection-aware filtering and batch behavior in `check` / `generate`

Important:

- the existing `collections` branch is a useful prototype and behavior
  reference, but it predates the current `Artifacts -> Execution Plans ->
  Collections` ordering
- final implementation should target the Artifacts/Plans stack, not revive the
  old `CheckGroup` / `GeneratorGroup` / `ModTree` integration path

Collections affect several existing implementation areas.

### Engine

The engine is responsible for validating collection definitions and projecting
them into the public schema:
- validate module-side `+collection`, `+keys`, and `+get`
- synthesize public collection objects
- expose `AsCollection` alongside `AsObject`
- implement `keys`, `list`, `get`, `subset`, and `batch` on the synthetic
  collection object

### Module Runtimes

Module runtimes must support authoring collections on ordinary objects:
- runtime-side schema registration must accept `+collection`, `+keys`, and
  `+get`
- existing pragma/decorator/directive plumbing should extend to collection
  semantics
- the raw module-defined collection object remains a backing shape rather than
  the public client shape

### Generated Clients

Generated client libraries should reflect the projected public DAGQL surface:
- collection-valued members appear as synthetic collection objects
- projected collections preserve the distinction between the core collection
  algebra and the type-specific `batch` namespace
- generated clients should not collapse the core collection algebra into a
  type-specific `batch` surface

### `dagger call`

`dagger call` does not add keyed-refinement sugar for collections. Collection
traversal follows the projected API directly: users call `keys`, `list`,
`get`, `subset`, and `batch` explicitly.

Collection-aware filtering — where generated CLI flags lower to `subset` on
matching collections — belongs to verb commands (`dagger check`, `dagger
generate`, `dagger ship`, `dagger up`) and `dagger list`, not to `dagger call`.

### `dagger shell`

`dagger shell` follows the same rule as `dagger call`: collection traversal
uses the projected API explicitly. Shell completion and help should understand
collection-valued steps in the current pipeline, but no collection-aware
filtering is added.

### `dagger functions`

Other existing discoverability surfaces should follow the same projection model:
- `dagger functions`
- shell completion/help
- module/type inspection
- schema introspection consumed by tooling

## General Maps

Collections are map-shaped at the semantic layer — `keys` defines a typed
keyspace, `get` resolves a value from a key — but this proposal restricts
values to objects. It does not introduce:
- a public DagQL map kind
- traversal or codegen rules for arbitrary map values
- typedef or introspection rules for a general map abstraction

A broader map design may follow; collections are intended not to close that
door.

## Implementation Status

### Planned

- [x] Add collection metadata and validation to module typedefs
- [x] Implement schema projection for public collection and batch types
- [x] Support explicit collection traversal in `dagger call`, `dagger shell`, and discoverability surfaces
- [x] Add collection-aware filtering and batch shadowing to `dagger check` and `dagger generate`
- [x] Add module authoring support across supported SDKs and runtimes
- [x] Add integration, CLI, and codegen coverage

### Accomplished

- [x] Locked the design decision that `dagger call` and `dagger shell` use explicit collection traversal only
- [x] Locked the design decision that collection-aware filtering sugar belongs to verb commands and `dagger list`, not `dagger call`
- [x] Locked the design decision that any exposed collection function beyond the effective `keys` field and `get` is re-homed under `batch`
- [x] Locked the design decision that collection `keys` are always authored as a field
- [x] Locked the design decision that the public projected collection type keeps the author-defined collection type name
- [x] Locked the design decision that the synthetic batch type is named `<CollectionType>_Batch`
- [x] Locked the design decision that collection keys may be builtin scalars, custom scalars, or enums, but not object-like or list types
- [x] Locked the design decision that effective `get` takes exactly one non-null key argument and returns a non-null object
- [x] Locked the design decision that load time checks structure and runtime checks behavior
- [x] Locked the design decision that collection filters use repeated flags only; comma-separated values are forbidden
- [x] Locked the design decision that filter flags are named by item type (singular), not collection type
- [x] Locked the design decision that artifact kind selection uses the built-in `--type=<name>` filter
- [x] Locked the design decision that `dagger list` is the discovery surface for filter values
- [x] Engine implementation has started with collection typedef metadata and validation
- [x] Engine implementation now projects synthetic public collection and batch schema types
- [x] CLI type inspection now recognizes projected collections and `dagger call` treats collection leaves as explicit traversal points
- [x] Check and generator traversal now apply collection-aware filters, batch shadowing, and raw filter-value listing
- [x] Go, TypeScript, Python, and Java module authoring paths now register collection backing objects and explicit keys/get overrides
- [x] Integration coverage now exercises explicit collection traversal in `dagger call` and generated Go and TypeScript clients over the projected `keys` / `list` / `get` / `subset` / `batch` surface

## Open Questions

None currently.
