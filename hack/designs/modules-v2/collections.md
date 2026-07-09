# Collections

Depends on: [Execution Plans](./plans.md)

A collection is a keyed set of objects. Collections extend both prior layers:
they add keyed selector dimensions to [Artifacts](./artifacts.md) and
collection-aware batching to [Plans](./plans.md). They do not replace either.

## Table of Contents

- [Problem](#problem)
- [Proposal](#proposal)
- [Use Cases](#use-cases)
- [Authoring](#authoring)
- [Projected API](#projected-api)
- [Extending Artifacts](#extending-artifacts)
- [Extending Plans](#extending-plans)
- [Checks and Generators](#checks-and-generators)
- [Type System](#type-system)
- [Implementation Notes](#implementation-notes)
- [General Maps](#general-maps)

## Problem

Modules can discover dynamic sets of related objects — tests by name, packages
by path, services by label — but cannot present them to clients without losing
`dagger check`, keyed selection, filtering, or batching. Keys are not
first-class in the schema, so every module invents ad hoc accessors and tooling
cannot offer these features generically.

## Proposal

A collection is declared on an ordinary object type with `@collection` /
`+collection` and projected by the engine into a synthetic public type with a
small standard surface: the algebra `keys`, `list`, `get`, `subset`, plus a
type-specific `batch` namespace.

Deliberately narrow:

- Collections are keyed sets of **objects**. A broader map abstraction may
  follow but is out of scope (see [General Maps](#general-maps)).
- They layer semantics onto existing objects; they are not a new type kind.
- They add keyed traversal, selection, and batching — nothing else (no mutation
  or execution semantics of their own).

## Use Cases

- **Test selection.** A suite publishes its tests as a collection keyed by name;
  users select tests by key instead of ad hoc flags. This is the motivation for
  collection-aware filtering in `check`/`generate`.
- **Test splitting.** `subset(keys)` turns one collection into an exact
  key-selected subset while preserving collection shape, letting tooling divide
  tests into buckets. `batch` gives the subset a place to execute efficiently.
- **One module, many projects.** A repository often holds many apps, packages,
  or services, discovered at runtime and keyed by names or paths. Collections
  let one module publish that dynamic set and let clients select by key.

## Authoring

Collections are defined by annotating the object type itself. Each collection
type has one effective `keys` field and one effective `get` function:

- If a field named `keys` exists, it is the effective `keys` field.
- If a function named `get` exists, it is the effective `get` function.
- `@keys` / `@get` override those default names only.

The canonical `GoTests` collection (see
[artifacts.md § Canonical Example](./artifacts.md#canonical-example)), expressed
in each SDK:

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

Non-standard names use overrides:

```graphql
type GoModules @collection {
  paths: [WorkspacePath!]! @keys
  module(path: WorkspacePath!): GoModule! @get
}
```

Requirements:

- `@collection` is required on the type; a collection has exactly one effective
  `keys` field (returning `[KeyType!]!`) and one effective `get` function
  (taking exactly one non-null `KeyType`, returning a non-null object).
- Key types are scalar or enum only (builtin scalars, custom scalars, enums).
  Object, input-object, interface, and list types are not valid keys.
- Keys should be unique within a collection.
- Any other exposed function on the type is re-homed under `batch` (see
  [Projected API](#projected-api)); non-function fields are not projected except
  the effective `keys` field.

Validity is enforced in two stages: at **load** the engine validates structure
(annotation present, exactly one `keys`/`get`, valid signatures); at **runtime**
it validates behavior (if `keys` advertises a key `get` cannot resolve,
operations fail at the point of use).

## Projected API

The raw module-defined collection type stays hidden. A field or function
returning a collection projects to a synthetic public type — keeping the
original field name — with engine-defined members:

```graphql
"""Synthetic public projection of a Go module collection."""
type GoModules {
  """Keys in the current subset, in stable collection order."""
  keys: [WorkspacePath!]!

  """Items in the current subset, in the same order as `keys`."""
  list: [GoModule!]!

  """Resolve one item in the current subset by key."""
  get(key: WorkspacePath!): GoModule!

  """Restrict the collection to an exact subset of keys."""
  subset(keys: [WorkspacePath!]!): GoModules!

  """Type-specific efficient operations over the current subset."""
  batch: GoModules_Batch!
}
```

### Algebra

- `keys` — the current subset's keyspace
- `list` — materializes the current subset's items (engine iterates `keys`,
  calls backing `get`)
- `get(key)` — one item from the current subset; errors on unknown key
- `subset(keys)` — exact key selection (not a predicate language); preserves
  parent key order; errors on unknown or duplicate keys

Laws:

- `c.subset(keys: c.keys)` ≡ `c`
- `c.subset(keys: ks).keys` returns `ks` in parent order
- `c.subset(keys: ks).list` returns the items for `ks` in parent order
- `c.subset(keys: ks).get(k)` errors unless `k` is in `ks`

Item types are unchanged: collection-relative identity stays on the collection,
not the item.

### Batch namespace

`batch` is not part of the core algebra. It is a type-specific namespace for
operations that execute more efficiently over the whole subset than one item at
a time. The engine identifies the effective `keys` and `get` and re-homes every
other exposed function under `batch`. For example, a collection of tests may
expose `runTests`; the engine projects it as `c.batch.runTests`, running one
`go test` process over many selected tests. `batch` operates on the current
subset, so `c.subset(keys: ks).batch` sees only `ks`.

## Extending Artifacts

A collection occurrence contributes to the [Artifacts](./artifacts.md) model:

- a new selector dimension named by the collection's **item type** (`go-test`)
- selector values from the collection's current keys
- extra coordinates on rows when that dimension is needed to distinguish them
  (see [artifacts.md § Dimensions and coordinates](./artifacts.md#dimensions-and-coordinates))

The base dimensions stay valid; collections add selector space rather than a
parallel targeting model. For the canonical example, the base scope
`filterCoordinates("type", ["go-test"])` can be narrowed further by
`filterCoordinates("go-test", ["TestFoo"])`.

## Extending Plans

Plan compilation still starts from an Artifacts scope. Collections change it in
two places:

1. collection selector dimensions lower to `subset(keys: ...)` on matching
   collections
2. collection `batch` behavior may replace one-item-at-a-time expansion

For the canonical example:

```text
workspace.artifacts
  .filterCoordinates("type", ["go-test"])
  .filterCoordinates("go-test", ["TestFoo", "TestBar"])
  .plan(verb: CHECK)
```

Here `go.tests` projects to the synthetic `GoTests` collection type, whose
algebra (`subset`, `batch`) the compiled plan drives directly. Without batch
behavior the plan has one action per item; with it, one action over the subset,
equivalent to:

```text
go.tests.subset(keys: ["TestFoo", "TestBar"]).batch.run
```

## Checks and Generators

These rules are specific to the check/generate feature, not the core algebra.
Generators follow the same traversal and filtering as checks throughout.

### Filter model

Collections add keyed dimensions and batch behavior on top of the built-in
`type` dimension. Two flag forms, both lowering to the Artifacts API:

- `--<item-type>=<key>` — the canonical keyed filter →
  `filterCoordinates("<item-type>", [...])`
- `--<collection-type>` — convenience presence alias →
  `filterDimension("<item-type>")`

For item type `GoTest` in collection `GoTests`:

```text
--go-test=TestFoo              => filterCoordinates("go-test", ["TestFoo"])
--go-tests                     => filterDimension("go-test")
--go-tests --go-test=TestFoo   => filterCoordinates("go-test", ["TestFoo"])
```

The presence alias is positive-only: `--go-tests` is legal; `--go-tests=true`,
`--go-tests=false`, and `--no-go-tests` are not. This keeps the selector model
positive-only without one-off boolean negation.

These aliases are CLI sugar. After parsing, the engine sees only real item
dimensions. Names are derived mechanically from Dagger's CLI casing rules:
keyed filters use the item type name, presence aliases use the collection type
name. Each value gets its own flag instance; comma-separated values are
forbidden. Repeated `--<item-type>` values are OR within the dimension;
repeating `--<collection-type>` has no additional effect. Type renames are
CLI-breaking for both forms.

```console
$ dagger check --go-test=TestFoo --go-test=TestBar --go-module=./myapp/app2
$ dagger check --go-tests --go-module=./myapp/app2
```

These filters are scope-relative constraints, not unique selectors: one filter
narrows every matching collection occurrence to the given key subset and may
match zero, one, or many occurrences. Combine filters to narrow further.

### Listing and discovery

`dagger list` is the exploration surface, using the same filter flags as the
verb commands, and can be projected through a verb to match its scope:

```console
$ dagger list                      # available dimensions with key types
$ dagger list types                # artifact types
$ dagger list go-module --check    # go modules in check scope
$ dagger list go-test --check \
    --go-module=./myapp/app2       # go tests filtered by module
```

`dagger list --help` lists real dimensions (`go-test`), not convenience aliases
(`go-tests`), though command help may show accepted aliases in its flag list.
Listing applies active filters from other dimensions first, flattens
parent/child relationships between dimensions, and prints unique values in
stable order. `dagger check -l` / `generate -l` use the table-capable listing
from [plans.md](./plans.md#cli-listing); collection dimensions become columns
when needed to distinguish rows.

### Batch shadowing

A collection has one effective check set drawn from item checks (on the item
type) and batch checks (on the `batch` type):

- If an item check and a batch check share a name, the batch check shadows the
  item check.
- Otherwise the item check remains.

Execution follows: a shadowing batch check runs once over the current subset; an
unshadowed item check runs once per item. Extend the canonical `GoTest` item
type with a `lint` check alongside its `run`, and let `GoTests.batch` define
`run` but not `lint`:

```console
$ dagger check -l
GO TEST   ACTION
TestFoo   lint
TestFoo   run
TestBar   lint
TestBar   run

$ dagger check --go-test=TestFoo --go-test=TestBar run
# runs once via go.tests.batch.run over the filtered subset

$ dagger check --go-test=TestFoo --go-test=TestBar lint
# runs once per filtered item via the item type's lint
```

## Type System

Collections are metadata layered on an object, not a peer kind:

- projected collections keep `TypeDef.Kind = OBJECT`
- they still populate `TypeDef.AsObject`
- they additionally populate `TypeDef.AsCollection`

Collection-unaware clients keep treating a projection as an ordinary object;
collection-aware surfaces read `AsCollection` for keyed-hop behavior. This
document does not introduce a `TypeDefKindCollection`.

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

Leading `_` stays reserved for Dagger-injected members. The synthetic collection
object uses normal names (`keys`, `list`, `get`, `subset`, `batch`) because it
is fully engine-owned and exposes no raw module methods; module authors should
not define public members with a leading `_`.

## Implementation Notes

The existing `collections` branch is a useful behavior reference but predates the
`Artifacts → Plans → Collections` ordering. The final implementation targets the
Artifacts/Plans stack; it does **not** revive the old
`CheckGroup` / `GeneratorGroup` / `ModTree` integration path.

- **Engine** — validate `+collection` / `+keys` / `+get`; synthesize public
  collection objects; expose `AsCollection` alongside `AsObject`; implement
  `keys`, `list`, `get`, `subset`, `batch`.
- **Module runtimes** — accept `+collection` / `+keys` / `+get` in schema
  registration by extending existing pragma/decorator/directive plumbing; the
  raw collection object stays a backing shape, not the public shape.
- **Generated clients** — reflect the projected surface; preserve the split
  between the core algebra and the type-specific `batch` namespace (do not
  collapse the algebra into `batch`).
- **`dagger call` / `dagger shell`** — no keyed-refinement sugar. Traversal uses
  the projected API explicitly (`keys`, `list`, `get`, `subset`, `batch`).
  Collection-aware filtering belongs to the verb commands and `dagger list`.
  Shell completion/help should understand collection-valued steps.
- **`dagger functions`, completion, inspection, introspection** — follow the same
  projection model.

## General Maps

Collections are map-shaped at the semantic layer — `keys` defines a typed
keyspace, `get` resolves a value — but this proposal restricts values to
objects. It does not introduce a public DagQL map kind, traversal/codegen for
arbitrary map values, or typedef/introspection rules for a general map. A broader
map design may follow; collections are intended not to close that door.

## Open Questions

None currently.
