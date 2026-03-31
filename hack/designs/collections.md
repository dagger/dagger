# Collections

## Status: Draft

## Table of Contents

- [Summary](#summary)
- [Problem](#problem)
- [Proposal](#proposal)
- [Module Definition](#module-definition)
- [Client Representation](#client-representation)
- [Schema Representation](#schema-representation)
- [Tooling and Traversal](#tooling-and-traversal)
- [Scope](#scope)

## Summary

Collections are a standalone primitive for representing keyed dynamic sets in a
module's published object graph.

This document intentionally stays narrow:
- collections define keyed collections of objects
- this proposal defines the collection primitive only
- it does not change object semantics beyond collection traversal, selection,
  and batching

This document includes `subset` as part of the collection primitive so that
collections can represent exact key-selected subsets of themselves.

## Problem

Modules can already discover dynamic sets of related objects, but today they
have no standard way to publish them as keyed collections.

That leaves a gap:

1. Dagger has lists and objects, but no map-like abstraction in the published
   schema.
2. Dynamic sets are usually keyed in practice, but the key structure is not
   first-class.
3. Existing traversal and selection mechanisms need keyed targeting without
   forcing users to spell explicit collection accessors like `get(...)`.

Collections fill that gap.

## Proposal

A collection is a keyed dynamic set.

It is defined on ordinary module objects using collection semantics, not a new
type kind.

The current direction is:
- module-side collection contract: `+keys` and `+get`
- public DAGQL projection: collection-valued fields/functions project as
  synthetic collection objects
- higher-level targeting syntax provides keyed traversal over that projection

## Module Definition

Collections are defined by semantic annotations on ordinary module fields and
functions.

Leading shape:

```graphql
"""Collection of Go modules keyed by workspace path."""
type GoModules {
  """All keys currently present in the collection."""
  keys: [WorkspacePath!]! @keys

  """Resolve one module by workspace path."""
  get(
    """Workspace path to resolve."""
    path: WorkspacePath!
  ): GoModule! @get
}
```

Rules:
- `+keys` enumerates the collection keyspace
- `+get` resolves one item by key
- `get` is required for every collection in v1
- every key returned by `+keys` must be accepted by `get`; otherwise the
  collection is invalid
- keys should be unique within a collection

Collections describe how a dynamic set is addressed and traversed. They do not
by themselves add mutation, execution, or other higher-level behavior.

If a module wants extra behavior on the underlying collection object, that is
module-specific behavior, not part of the core collection algebra. If surfaced
publicly, type-specific efficient collection-level behavior belongs under
namespaces such as `batch`, not alongside the core `keys`, `list`, `get`, and
`subset` operations.

## Client Representation

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
  modules: _GoModuleCollection!
}

"""Synthetic public projection of a Go module collection."""
type _GoModuleCollection {
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
  ): _GoModuleCollection!

  """Type-specific efficient operations over the current subset."""
  batch: _GoModuleCollectionBatch!
}

"""Type-specific batch operations over the current subset."""
type _GoModuleCollectionBatch {
  """Illustrative only: efficiently evaluate checks over the current subset."""
  checks: CheckGroup!
}
```

Current rules:
- projection keeps the original field/function name
- public collection types are synthetic, engine-defined objects rather than
  raw module-defined collection objects
- projected collections expose typed `keys`, `list`, and `get`
- projected collections also expose `subset`, returning the same synthetic
  collection type so subsets can be represented across engine boundaries
- projected collections reserve `batch` as a type-specific namespace for
  efficient execution over the current subset
- `get` errors on an unknown key
- projected item types are otherwise unchanged; collection-relative identity
  stays on the collection object rather than being injected onto the item type
- projection applies to both fields and functions returning collections
- projected list order preserves the order of `+keys`
- `subset` is exact key-based subset selection, not a general predicate
  language
- `subset` preserves the parent key order
- `subset` errors on unknown keys
- `subset` errors on duplicate keys

The engine materializes `list` by iterating keys and calling backing
collection `get(...)`.
`get` is the canonical exact-key access path.
`subset(keys: [...])` is the operation for representing an exact
key-selected subset of a collection while preserving collection shape.

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

`batch` is not part of the core collection algebra. It is a reserved
extension point for type-specific collection-level operations that can execute
more efficiently over the current subset than naively invoking the equivalent
item-level operation one item at a time.

For example, a collection of test definitions may implement batch `checks`
that runs one `go test` process over many selected tests rather than one
process per test.

Important boundaries:
- `batch` operates on the current subset, so `c.subset(keys: ks).batch`
  sees only `ks`
- `batch` is about efficient execution over a collection subset
- `batch` is type-specific; different collection types may expose different
  methods under it
- the meaning of methods under `batch` is specific to each collection type and
  outside the core collection algebra

## Schema Representation

This section answers how Collections are represented in the schema and
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

Leading shape:

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

Intended meaning:
- `KeyType` is the type accepted by `get(key)` and `subset(keys: [...])`
- `ValueType` is the type returned by `get()` and enumerated by `list`
- `BatchType` is the type returned by `batch`

This document does not introduce `TypeDefKindCollection`.

### Reserved Names

Leading `_` is reserved for Dagger-injected fields and arguments.

The core synthetic collection object in this document uses normal names
(`keys`, `list`, `get`, `subset`, `batch`) because that object is fully
engine-owned and does not expose raw module-defined collection methods.

The reservation still matters as a general rule for future Dagger-injected
members and other projection escape hatches. Module authors should not define
public fields or arguments with leading `_`.

### Map Semantics

Collections should be understood as map-shaped at the semantic layer:
- `+keys` defines a typed keyspace
- `+get` resolves a value from a key

In part 1, that value is constrained to be an object, and the public DAGQL
surface projects the collection as a synthetic collection object rather than a
first-class map kind.

Collections are map-shaped semantically, even though this proposal does not
introduce a general map kind.

That broader map design is intentionally out of scope here. This document does
not define:
- a public DAGQL map kind
- traversal semantics for arbitrary map values
- codegen or introspection rules for general maps

## Tooling and Traversal

This document focuses on how Collections affect selection and traversal
mechanisms that already exist.

Collections affect the following current surfaces.

This document specifies collection-aware traversal semantics, but does not
require a particular shared implementation strategy for those surfaces.
Whether keyed traversal is centralized in one lowering layer or implemented in
surface-specific code is a tactical part 1 detail.

### `dagger call`

`dagger call` already walks a function pipeline over the current object.

Collections add a keyed refinement step to that traversal:
- selecting a collection-valued member should not force users to spell raw
  `get(...)`
- keyed refinement should lower to `get(...)` on the synthetic collection
  object and then continue traversal on the item type
- raw DAGQL still degrades gracefully because clients can call `list` and
  `get` directly

### `dagger shell`

`dagger shell` already pipes state through commands such as `foo | bar`.

Collections add keyed refinement to that existing pipeline model:
- a collection-valued hop should remain traversable inside shell pipelines
- keyed refinement should lower to `get(...)` before continuing on item
  methods/fields
- shell completion and help should understand collection-valued steps in the
  current pipeline

### Checks

Collections affect checks through generated filters and through the collection's
effective check set.

#### Filter Model

Check filters shape the effective check tree before listing or execution.

Two classes of filters are generated automatically from the traversed schema:

- Every object type touched by check traversal gets a boolean filter of the form
  `--<type>=true|false`.
- Every collection type touched by check traversal gets a valued filter of the
  form `--<collection-type>=<key>[,<key>...]`.

Filter names are derived mechanically from type names using Dagger's existing
CLI casing rules.

Examples:

```bash
$ dagger check --go=true --nodejs=true --sdk=true --nodejs-sdk=false
$ dagger check --go-tests=TestFoo,TestBar --go-modules=./myapp/app2
```

These filters are scope-relative constraints, not unique selectors.

- An object filter applies to every occurrence of that type within the selected
  scope.
- A collection filter narrows every matching collection occurrence to the given
  key subset.
- A filter may match zero, one, or many occurrences.
- Matching many occurrences is valid.
- To narrow further, combine filters.

Function-path selectors remain valid, but filters do not depend on them. A
selector narrows the scope first. Filters then shape the tree within that
scope.

Type renames are CLI-breaking for these generated filters.

#### Listing Filter Values

Every collection filter has a corresponding list flag:

- `--list-<collection-type>`

This lists the flat set of values available for that collection filter within
the current scope.

Examples:

```bash
$ dagger check --list-go-tests
$ dagger check --go-modules=./myapp/app2 --list-go-tests
$ dagger check --go-tests=TestFoo --list-go-tests --list-go-modules
```

Listing rules:

- `--list-<collection-type>` lists raw filter values, not schema paths.
- It applies all other active filters first.
- It ignores its own active value filter while listing.
- Multiple `--list-*` flags are allowed.
- Each listed dimension is printed independently.
- Parent/child relationships between different filter dimensions are
  intentionally flattened.
- Output is unique values in stable order.

`dagger check -l` remains a structural check listing. It is allowed with
selectors and boolean object filters. It is not allowed with valued collection
filters.

For example:

```bash
$ dagger check -l --go=true --sdk=false

$ dagger check -l --go-tests=TestFoo
Error: can't use -l with --go-tests; use --list-go-tests instead
```

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

```bash
$ dagger check -l
go:tests:lint
go:tests:run-test

$ dagger check --go-tests=TestFoo,TestBar go:tests:run-test
# runs once using go.tests.batch.runTest over the filtered subset

$ dagger check --go-tests=TestFoo,TestBar go:tests:lint
# runs once per filtered item using the item type's lint check
```

### `+generate` / generators

The current generators path should be treated the same way as current
`dagger check`.

Collections do not materially retrofit generator discovery or execution in part
1.

This should also be understood as a near no-op:
- no major collection-aware redesign of current generator walking is specified
  here
- no major effort should be spent teaching current modtree walking to become
  collection-native for generators
- collection-native generator targeting, if needed, belongs with later
  higher-layer work rather than this core Collections design

### Client Libraries

Generated client libraries should reflect the projected public DAGQL surface:
- collection-valued members appear as synthetic collection objects
- projected collections preserve the distinction between the core collection
  algebra and the type-specific `batch` namespace
- generated clients should not collapse the core collection algebra into a
  type-specific `batch` surface

### Codegen Tooling

Collections affect both codegen directions:
- module authoring codegen must understand module-side `+keys` and `+get`
- client codegen must expose the projected synthetic collection object surface,
  including the separation between core collection operations and `batch`
  methods
- leading `_` reservation must be preserved so generated APIs do not collide
  with module-defined members

### JSON Export Of Object State

Collections affect the JSON shape of projected values:
- public collection projection exports as a synthetic collection object, not
  the raw module-defined collection object
- enumerated values appear through `list`

### Other Current Surfaces

Other existing discoverability surfaces should follow the same projection model:
- `dagger functions`
- shell completion/help
- module/type inspection
- schema introspection consumed by tooling

## Scope

### Rooting Scope

Part 1 fits the current module shape on `main`.

That means:
- collections live under the existing single rooted module object
- rooted collections and broader root-model cleanup are out of scope here

### Non-Goals

This document does not define:
- artifacts
- provenance
- verb planning
- dedicated split/sharding APIs beyond key-based `subset`
- the contents of any particular collection type's `batch` namespace
- schema cleanup for multiple rooted constructors

### Deferred Work

The engine may use private collection splitting internally in part 1.

This document sketches public `subset` because distributed splitting may need
to represent collection subsets across engine boundaries.

Richer public split/shard APIs stay out of scope until the planned schema
cleanup for constructors/rooting lands.
