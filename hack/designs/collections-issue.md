# Collections

## Context

Builds on [Workspace artifacts (#14178)](https://github.com/dagger/dagger/pull/14178), using its [design at `8b2da1b1`](https://github.com/dagger/dagger/blob/8b2da1b1cd4b8b18d940df990a5982a71e320e69/hack/designs/collections-issue.md).

Artifacts defines DAG addresses, discovery, selection, evaluation, and the `Check` projection. It also replaces the group APIs. This issue adds dynamic keyed collections to that design.

## Problem

A module can discover dynamic sets of objects at runtime: tests by name, packages by path, or services by label. But a list of objects does not expose those keys to Dagger.

Users need to list the keys, select an item, and run checks or generators on a subset. Module authors also need to run one operation for the whole subset.

## Solution

A **collection** is a dynamic set of objects with unique keys. Its type defines the keys and item lookup. Each collection value contains its own set of keys.

A **dimension** is a named axis for selecting artifacts by key. The engine derives dimensions from fields that expose collections in the workspace schema. One collection type can supply several dimensions.

Authors declare collections. The engine supplies a standard collection API and projects collection fields into a flat list of dimensions.

### 1. Declare a collection

Mark an object type with `+collection` in Go, `@collection` in Dang and Python, or `@collection()` in TypeScript.

```go
// +collection
type GoTests struct {
 Keys []string
}

func (tests *GoTests) Get(name string) *GoTest {
 return &GoTest{Name: name}
}
```

A collection has one exposed keys field and one exposed item lookup function. The default names are `keys` and `get`. The markers `+keys` / `@keys` and `+get` / `@get` select other members. TypeScript uses `@keys()` and `@get()`. A marker overrides the default name. The collection marker is required even with the default names.

For example, this author schema uses paths as keys:

```graphql
type GoModules @collection {
  paths: [String!]! @keys
  module(path: String!): GoModule! @get
}
```

The keys field must store a non-null list of non-null scalar or enum values. Here, `!` means the value cannot be null. The lookup function must accept one non-null argument of the key type and return a non-null object. For string keys, use `[String!]!` for the field and `String!` for the argument. A raw list is not a collection.

Validate the member types when loading the module. Reject missing members or multiple markers for the same role. Reject null or duplicate keys when reading a collection. Report lookup failures when an item is used.

### 2. Expose a standard API

The engine exposes this schema in place of the author object. The field that returns the collection keeps its name.

```graphql
type GoModules {
  keys: [String!]!
  list: [GoModule!]!
  get(key: String!): GoModule!
  subset(keys: [String!]!): GoModules!
}
```

- `keys` returns the current keys in author order.
- `list` calls `get` for those keys, in that order.
- `get` fails if the key is outside the current subset.
- `subset` returns a new collection. It keeps parent order and fails on unknown or duplicate keys. An empty subset is valid.

Every other exposed function moves under `batch`. Other stored fields remain internal. For example, a `run` check on `GoTests` becomes:

```graphql
type GoTests {
  keys: [String!]!
  list: [GoTest!]!
  get(key: String!): GoTest!
  subset(keys: [String!]!): GoTests!
  batch: GoTests_Batch!
}

type GoTests_Batch {
  run: Check! @check
}
```

The author function returns `Void`. The Artifacts projection exposes it as `Check!`.

The engine calls batch functions on a copy of the author object. It replaces the keys field with the selected keys and updates the optional delta field. It preserves all other state. Authors use the keys to limit the work. Omit `batch` when there are no batch functions.

Collections remain object types. Add collection metadata to `TypeDef`; keep `kind: OBJECT` and `asObject`:

```graphql
extend type TypeDef {
  asCollection: CollectionTypeDef
}

type CollectionTypeDef {
  keyType: TypeDef!
  valueType: TypeDef!
  batchType: TypeDef
}
```

Generated clients see the standard collection API. Collection projection does not change item types.

#### Collection delta

A collection type may define a field named `delta` of type `CollectionDelta`. The marker `@delta`, or `+delta` in Go, selects a field with another name. TypeScript uses `@delta()`. The marker overrides the default name. Allow one delta field and require its type to be `CollectionDelta`.

The engine fills this field before passing the collection to module code, including batch functions. Authors can leave it unset when constructing the collection. It always contains the changes from the original collection.

```graphql
type CollectionDelta {
  addedKeys: [String!]!
  removedKeys: [String!]!
}

type GoTests @collection {
  keys: [String!]!
  get(name: String!): GoTest!
  delta: CollectionDelta
}
```

With another field name:

```graphql
type GoModules @collection {
  paths: [String!]! @keys
  module(path: String!): GoModule! @get
  selection: CollectionDelta @delta
}
```

The base is the collection first returned by the module. Further selections retain that base. The delta lists the differences between the current and base keys:

- `addedKeys` contains current keys absent from the base, in current order.
- `removedKeys` contains base keys absent from the current collection, in base order.
- Both lists are empty on the original collection.
- A `subset` call that retains every current key preserves the delta.

For example, with original keys `["TestA", "TestB", "TestC"]`:

| Selection | `addedKeys` | `removedKeys` |
| -- | -- | -- |
| Original collection | `[]` | `[]` |
| Select `TestA` and `TestC` | `[]` | `["TestB"]` |
| Then select `TestC` | `[]` | `["TestA", "TestB"]` |

All collections use the same `CollectionDelta` type. Delta keys use the same string form as artifact keys. Conversion must preserve key identity and allow conversion back using the declared key type. For example, integer keys `[1, 2]` become `["1", "2"]`.

The engine retains the base and current keys across module calls and loading by ID. It computes the delta without evaluating items. A change to an item's contents does not affect the delta. The delta belongs to the collection value. Its dimension name does not affect it.

The first implementation only selects subsets, so `addedKeys` is always empty. Future operations that add or restore keys must compare the result with the same base.

When both lists are empty, a batch function can use an operation for the full original collection. For example, its original collection can contain all tests in a directory. It can then run `go test` without a test filter.

### 3. Project collections into dimensions

A collection field creates a dimension. Its item keys become the keys for that dimension. Reusing the same schema field reuses its dimension. Different fields create different dimensions, even if they return the same collection value.

For example:

```graphql
type Golang { modules: GoModules! }
type App { dependencies: GoModules! }
type GoModule { tests: GoTests! }
type GoTest { container: Container! }
```

This schema has three dimensions:

| Collection field | Collection type | Short name | Qualified name |
| -- | -- | -- | -- |
| `Golang.modules` | `GoModules` | `go-module` | `golang-modules` |
| `App.dependencies` | `GoModules` | `go-module` | `app-dependencies` |
| `GoModule.tests` | `GoTests` | `go-test` | `go-module-tests` |

All collection values returned by `GoModule.tests` share one dimension. A list of test keys combines their keys. Selecting a parent module narrows that list.

#### Dimension names

Each dimension has a stable identifier: the exact GraphQL parent type and field name, separated by a dot. Include any module namespace in the type name. For example, `Golang.modules` identifies one dimension. Adding another collection field does not change it. Renaming that type or field does.

The short name comes from the author's item type, in CLI case. For example, `GoModule` gives `go-module`. The qualified name combines the parent type and field in CLI case, with a hyphen: `Golang.modules` gives `golang-modules`. Omit the engine's module namespace from both names. Do not choose a primary dimension when names conflict.

Resolve short and qualified names against the dimensions on the selected schema paths. Apply all path filters first. Each input address supplies the path scope for its own query. Separate key filters use the paths of the whole selection. Key filters and runtime key values do not affect name resolution. Resolve names when reading or running the selection, so filter order does not matter.

For example, the path `golang/modules/tests/container` crosses `Golang.modules` and `GoModule.tests`. It accepts `go-module` and `go-test`. Adding `App.dependencies` does not change that path or those names.

With no path filter, `go-module` is ambiguous in this schema. Use `golang-modules` or `app-dependencies`. Qualified names are also accepted when the short name is unique. If a name matches several dimensions, reject it and report their exact identifiers. This rule also applies when one dimension's short name matches another's qualified name. Accept exact identifiers in every place that accepts a dimension name. Identifiers are case-sensitive.

Artifact metadata uses stable identifiers. A complete artifact address uses the short name if it is unique on that path. Otherwise, use the qualified name. If that name also conflicts, use the exact identifier. Stored filters over multiple paths use exact identifiers. Adding unrelated fields cannot change the meaning of a complete artifact address.

Build the dimension list from the module schema before adding the standard collection operations. Operations such as `subset` do not create new dimensions. Empty collections still have dimensions in the schema.

#### Artifact API

Fill the dimension fields defined by Artifacts:

```graphql
extend type Artifact {
  dimensionKeys: [ArtifactDimensionKey!]!
}

type ArtifactDimensionKey implements Node {
  dimension: String!
  id: ID!
  key: String!
}

extend type Artifacts {
  filterDimensions(dimensions: [String!]!): Artifacts!
  filterDimensionKeys(dimension: String!, keys: [String!]!): Artifacts!
  dimensions: [String!]!
  dimensionKeys(dimension: String!): [String!]!
}
```

The container for one test has this address:

```json
{
  "path": ["golang", "modules", "tests", "container"],
  "dimensionKeys": [
    {"dimension": "Golang.modules", "key": "sdk/go"},
    {"dimension": "GoModule.tests", "key": "TestConnect"}
  ]
}
```

The Artifacts `uri()` formatter includes the dimension keys:

```text
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

Collection selection inserts `get(key: ...)` calls. `get` and the keys are not path segments. The engine enumerates item objects and their children. It also retains the collection object at its own path, without a key for that collection. Thus `golang/modules` is a `GoModules` artifact; adding a `Golang.modules` key at the same path selects a `GoModule` artifact. `batch` is an explicit path on the collection object, not a child of each item.

Artifact identity is the path plus all dimension keys. Distinct keys remain distinct even if `get` returns the same object. Filters never rewrite addresses or turn a collection artifact into a subset.

Alternatives in one filter use OR. Chained filters use AND. Unknown dimension names or keys match nothing. An ambiguous name is an error. A key filter applies to all collection values represented by its dimension. Skip keys that do not match, but retain the original collection for value lookup. A test absent from one module must not fail selection in another.

`dimensions()` reports identifiers represented by selected artifacts. `dimensionKeys()` reports their keys. Both results are sorted and contain no duplicates. Collection `keys` and `list` retain author order.

#### Discovery and evaluation

Listing can construct collections and read their keys. To discover nested keys, it can call a parent's `get` and the field that returns a child collection. These parent objects can also be artifacts. Listing must not call leaf value functions, such as `GoTest.container`. It must not run checks, generators, or batch functions.

For static object discovery, traverse module-defined fields that can be called without user input. Traverse engine-defined fields only when they carry `@check`, `@generate`, `@up`, or `@agent`. The rule applies to the field definition. A module field returning a Container is an artifact, but its unmarked `rootfs` field stops discovery. A Changeset exposes its marked `stale` check. Stop when an object type repeats on the current path.

Store the discovery scope and filters in `Artifacts`. Expand collections when a result is requested. Apply path, type, and parent-key filters before expanding child collections. Keep the existing traversal limits for cycles, nullable values, raw lists, and required arguments.

Reading metadata from an existing `Artifact` does not evaluate its value. `value()` follows the complete address in the source workspace, including after a module call or loading by ID.

#### Key text

Artifact and delta keys use the same string form. Strings, string scalars, and enums use their value. Numbers and booleans use their JSON form. Conversion back uses the declared key type and must preserve key identity. Keep the typed key for `get` and `subset`. Do not infer its type from the text.

### 4. Select items by dimension

Use dimension keys in the query part of a DAG address:

```text
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

Each pair accepts an exact dimension identifier, a short name, or a qualified name. Names must be unambiguous on the selected paths. Repeat a dimension to select alternative keys:

```text
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect&go-test=TestQuery
```

Use the key text defined above. `/` needs no escape in a key. Percent-encode `&`, `=`, `#`, `+`, `%`, and spaces. A bare dimension name, such as `?go-module`, selects any key. With `=`, an empty value selects the empty string key.

When an exact path ends at a collection field, omit its dimension to select the collection object. Supply its dimension to select items:

| Address | Selection |
| -- | -- |
| `dag://golang/modules` | The `GoModules` collection |
| `dag://golang/modules?go-module=sdk/go` | One `GoModule` item |
| `dag://golang/modules?go-module` | All items in that collection |

Parent dimensions still select the containing objects. A complete address has one key for each item selection along the path. Missing parent keys or alternative keys can select several artifacts. `Workspace.resolve` uses the Artifacts rule: require exactly one match.

For each dimension, `Artifact.uri()` tries its short name, then its qualified name, then its exact identifier. It uses the first name that is unambiguous on the path. Order pairs from parent to child. `Artifacts.uri` uses exact identifiers for selectors over several paths.

Dimension filters must survive an address round trip: `artifacts.filterUri(a.uri)` selects the same set as `a`.

### 5. Use the existing CLI and resolver

```console
$ dagger list go-test golang --go-module=sdk/go
TestConnect
TestQuery

$ dagger list -a golang/modules/tests/container --go-module=sdk/go --go-test=TestConnect
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

Use the Artifacts flags `--<dimension>=<key>` and `--dimension-key=DIMENSION=KEY`. Both accept exact identifiers, short names, or qualified names, unambiguous on the selected paths. The generic form also works when a name conflicts with a command flag. Repeat flags for alternatives; do not split values on commas. Use schema metadata to register flags, including for empty collections.

A flag has the same meaning as one query pair, and the two combine. Quote an address that contains `&`:

```console
$ dagger list -a 'dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect'
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

`Workspace.resolve` accepts the same complete address:

```text
ws.resolve("dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect").container()
```

`resolve` requires exactly one match. Missing keys or repeated dimensions can produce several matches; they use the same selection rules as `artifacts`. Unknown or unused selectors produce no match. Reject malformed selectors and paths that require two item selections for the same dimension. Workspace errors never trigger external resolution.

Default `uri()` output must resolve to the same value.

`dagger call`, the shell, and generated clients use the standard collection API directly:

```console
dagger call golang modules get --key=sdk/go tests subset --keys=TestConnect --keys=TestQuery batch run sync
```

### 6. Select checks and generators

`dagger check` and `dagger generate` use the shared Artifacts selection API. Add dimension filters to their path and directive filters. The dimension names, key text, and filter rules are the same as for `dagger artifacts`.

Resolve dimension names within the selected paths. Then merge keys for the same dimension with OR, and combine different dimensions with AND. An omitted dimension selects all keys. An empty key list matches nothing.

For execution, intersect the filters with each collection's keys, then call `subset` with the matching keys. Passing the raw filter to `subset` would fail when a requested test exists in another Go module only.

For each collection value, a batch check replaces a check with the same name on its item type. Run it once on the selected subset. Other item checks run once per selected item. Apply the same rule to generators. Do not run a batch for an empty selection, and do not combine subsets from different parent items.

Apply batch replacement in the shared Artifacts evaluator, before reading selected checks or evaluating generators. The evaluator needs the full selection to form each subset. It must not invoke the item functions that the batch replaces. Direct calls to an item function keep their normal behavior.

The base design can read each static `Check.pass` directly. Collection selections with a batch must first pass through batch replacement. This is an extension to Artifacts evaluation.

```console
$ dagger check golang/modules/tests/run --go-module=sdk/go --go-test=TestConnect --go-test=TestQuery
# One batch run for these two tests in sdk/go.

$ dagger check golang/modules/tests/lint --go-module=sdk/go --go-test=TestConnect --go-test=TestQuery
# Two item checks if GoTests has no batch lint check.

$ dagger check 'dag://golang/modules/tests/run?go-module=sdk/go&go-test=TestConnect&go-test=TestQuery'
# The same selection as the first command.
```

Path filters select the effective check or generator name, before batch replacement. `check -l` and `generate -l` print one address per line, with the selected keys, and show whether execution uses a batch. A batch function with no matching item function runs once on the subset under its own name.

### First implementation

Ship collection declarations, the standard API, delta, dimensions, nested discovery, keyed resolution, and batch selection together. Support Go, Dang, Python, and TypeScript authoring. Generate clients from the public collection schema. Expose the new collection fields and types in the v1 API.

Extend the discovery and evaluation supplied by #14178. Use its address parser, check projection, error handling, and command integration.

Verify:

- Collection validation, typed keys, author order, empty subsets, and rejection of unknown or duplicate subset keys.
- Delta naming and overrides, unchanged selections, chained subsets, and key text conversion.
- Nested and empty collections, one collection type or value exposed through several dimensions, and one field reused across parent items.
- Short, qualified, and exact names select the same dimension. Name conflicts and filter order do not change the selected dimension.
- Adding unrelated fields does not change complete addresses. Equal objects at different keys retain distinct addresses.
- Discovery does not evaluate leaf values or excluded parent items.
- Collection and item addresses at the same path, flag and query equivalence, and dimension filters preserved by `uri` and `filterUri`.
- Artifacts, subsets, base keys, and delta survive module calls and loading by ID. Evaluation uses the source workspace, including its edits.
- Batch replacement runs once per selected collection value, receives the selected keys and delta, and skips empty selections.
- Item functions replaced by a batch do not run. Other item functions still run once per selected item.
