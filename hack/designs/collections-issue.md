# Collections

## Context

Builds on [Workspace artifacts (#14178)](https://github.com/dagger/dagger/pull/14178).

Artifacts defines DAG addresses, discovery, selection, evaluation, and the `Check` projection. It also owns `Changeset.stale` and the move from group APIs to Artifacts. This issue adds dynamic keyed collections to that design.

## Problem

A module can discover dynamic sets of objects at runtime: tests by name, packages by path, or services by label. But a list of objects does not expose those keys to Dagger.

Users need to list the keys, select an item, and run checks or generators on a subset. Module authors also need to run one operation for the whole subset.

## Solution

A **collection** is a dynamic set of objects with unique keys. Its type defines the keys and item lookup. Each collection value contains its own set of keys.

A **dimension** is a column in the flattened workspace. Each collection type defines exactly one collection column. All occurrences of that collection type share the column. Its name uses the item type, with a collection type fallback for name conflicts.

Authors declare collections. The engine supplies a standard collection API and maps collection types to columns.

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

A collection has one exposed keys field and one exposed item lookup function. The default names are `keys` and `get`. The markers `+keys` / `@keys` and `+get` / `@get` select other members. TypeScript uses `@keys()` and `@get()`. Python uses `keys()` for a stored field and `@get` on a `@function`. A marker overrides the default name. The collection marker is required even with the default names.

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

A collection type may define a field named `delta` of type `CollectionDelta`. The marker `@delta`, or `+delta` in Go, selects a field with another name. TypeScript uses `@delta()`. Python uses the field helper `delta()`. The marker overrides the default name. Allow one delta field and require its type to be `CollectionDelta`.

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

Every collection has an internal `base` reference. It is not a schema field or an author field. When the engine first stores a collection result with no base, it sets the base to that result. The base is then fixed.

Normal state copies preserve the base, including across module calls and loading by ID. A new collection starts a new base. This rule applies even when the author declares no delta field. Clearing or copying the delta field does not change the base.

Any function can change keys. No function writes to the base. `subset` has no special role in delta tracking. The engine compares current keys with base keys when it fills the delta field:

- `addedKeys` contains current keys absent from the base, in current order.
- `removedKeys` contains base keys absent from the current collection, in base order.
- Both lists are empty on the original collection.
- A `subset` call that retains every current key preserves the delta.

For example, with original keys `["TestA", "TestB", "TestC"]`:

| Operation | `addedKeys` | `removedKeys` |
| -- | -- | -- |
| Original collection | `[]` | `[]` |
| Select `TestA` and `TestC` | `[]` | `["TestB"]` |
| Then select `TestC` | `[]` | `["TestA", "TestB"]` |
| Then module code sets keys to `TestB`, `TestC`, `TestD` | `["TestD"]` | `["TestA"]` |

All collections use the same `CollectionDelta` type. Delta keys use the same string form as artifact keys. Conversion must preserve key identity and allow conversion back using the declared key type. For example, integer keys `[1, 2]` become `["1", "2"]`.

The engine computes the delta without evaluating items. Key order and item contents do not affect key membership. The delta belongs to the collection value. Its dimension name does not affect it.

The delta shows the net difference, not a history of operations. Restoring a removed key cancels its removal. The injected delta is a snapshot for one module call. Local key changes become visible to the engine when the module returns the collection. The engine fills a new delta before the next module call.

When both lists are empty, a batch function can use an operation for the full original collection. For example, its original collection can contain all tests in a directory. It can then run `go test` without a test filter.

### 3. Project collections into dimensions

A collection type creates one column. The column identifier is its exact schema type name. The default column name is its item type name in kebab case. Its coordinate is the selected item key.

```graphql
type Go { modules: GoModules! }
type GoModule { tests: GoTests! }
type GoModules { get(key: String!): GoModule! }
type GoTests { get(key: String!): GoTest! }
```

| Collection type | Column identifier | Column name |
| -- | -- | -- |
| `GoModules` | `GoModules` | `go-module` |
| `GoTests` | `GoTests` | `go-test` |

A second field that returns `GoModules` uses the same `go-module` column. Field paths do not create or identify collection columns. A module coordinate is not needed to construct a collection identifier: schema type names already identify the types.

An item can itself be a collection. For `Rows.get -> Row.get -> Cell`, the columns are `Rows` (named `row`) and `Row` (named `cell`). No field is needed between those boundaries. The input lists both boundaries in parent-to-child order.

A route that repeats the same collection type is rejected: one scalar cell cannot hold two different keys for that type. Do not create a second column or silently replace a key.

Build the complete column set from the schema before selecting rows. Empty collections still contribute columns. Fields such as `get`, `list`, and `subset` do not create columns.

#### Static dimensions

| Identifier | Coordinate |
| -- | -- |
| `module` | Configured module name, when present |
| `name` | Full field path from the exposed module root, or `.` for that root |

Keep literal schema field names. Include ordinary collection field names. Omit the module prefix and synthetic `get` operations. For example, a test can have `name=modules.packages.tests.test`. Artifact types do not add columns. Legacy `type:<TypeName>` keys remain compatibility selectors.

#### Dimension names

Collection type identity and column names come only from schema type metadata. They do not depend on a field route, current selection, or observed keys. Selectors can use the exact collection type identifier. Existing legacy path identifiers stay in the compatibility layer.

Two different collection types can return the same item type. For example, `Packages` and `Dependencies` can both return `Package`. Their columns stay distinct because their identifiers are different collection types. Do not merge them.

Use item type names by default. For every name conflict, switch all affected columns to their collection type names. The example gives `packages` and `dependencies`. An unrelated `Tests -> Test` column stays `test`.

A fallback can conflict with another column. Apply the same rule to that column. Report an error if a conflict remains after every affected column uses its collection type name. Reserve `module` and `name`, even in a workspace with no module coordinate. Do not add field qualifiers or numeric suffixes.

Compute names from the complete workspace schema before filtering, including source filters. Empty collections contribute columns. Legacy type selectors and CLI flags do not change column names. A reserved CLI flag can use another flag name while the API column keeps its name.

For a collection column, `qualifiedName` is its exact collection type identifier. Both `name` and `qualifiedName` must select that column.

Exact column identifiers remain valid selectors. Reject a column name that equals another collection's identifier. This can occur with lower-case schema type names.

#### Artifact API

Fill the dimension fields defined by Artifacts:

```graphql
extend type Artifact {
  dimensionKeys(all: Boolean = false): [ArtifactDimensionKey!]!
}

type ArtifactDimensionKey implements Node {
  dimension: String!
  id: ID!
  key: String!
}

extend type Artifacts {
  dimensionDefinitions: [ArtifactDimension!]!
  filterDimensions(dimensions: [String!]!, exclude: Boolean = false): Artifacts!
  filterDimensionKeys(dimension: String!, keys: [String!]!): Artifacts!
  dimensions(all: Boolean = false): [String!]!
  dimensionKeys(dimension: String!): [String!]!
}

type ArtifactDimension implements Node {
  id: ID!
  identifier: String!
  name: String!
  qualifiedName: String!
  collectionType: String
  itemType: String!
  keyName: String!
  keyDescription: String!
}
```

`dimensionDefinitions` lists dimensions on the selected schema paths. It does not read collection values. Empty collections still appear here. Modern dimension and path definitions list only canonical columns. Legacy type selectors are not columns. The CLI keeps their compatibility metadata separate.

The key name and description come from the argument of the author's `@get` function. Flag help uses an uppercase argument placeholder and the item type name. An argument description appears on a second line. The discovery hint names the actual collection type: `--go-test NAME` points to `dagger list go-tests`. `dagger list` uses plural names for artifact type shortcuts, such as `containers`. Collection names use the declared collection type, such as `go-tests`. If a collection name is also an item type's plural, the command lists collection keys. Use `dagger list -a --type=go-test` to list those item artifacts.

`pathDefinitions(absolute: Boolean = false): [ArtifactPath!]!` lists schema paths with their addresses, descriptions, and dimension identifiers. It does not construct collections or resolve dimension-key filters. Empty collections still have paths. Each path appears once, sorted by address.

Command lists such as `check -l`, `up -l`, and `shell -l` use these paths by default. They show one hint when dimensions are present: `Use --all to list each key combination.` With `--all` or an explicit key filter, they enumerate runtime items. `list -a` always enumerates runtime items.

Requesting `dimensionKeys(all: true)` for a test container gives its full coordinates:

```json
{
  "path": ["go", "modules", "tests", "container"],
  "dimensionKeys": [
    {"dimension": "module", "key": "go"},
    {"dimension": "GoModules", "key": "sdk/go"},
    {"dimension": "GoTests", "key": "TestConnect"},
    {"dimension": "name", "key": "modules.tests.container"}
  ]
}
```

`uri()` separately chooses the smallest key set that identifies the artifact. If the full catalog is unavailable, a complete path form can retain both collection keys:

```text
dag+container://go/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

Legacy type-path query selectors remain accepted. They are not columns in the canonical projection.

Collection selection inserts `get(key: ...)` calls. `get` and the keys are not path segments. The engine enumerates item objects and their children. It also retains the collection object at its own path, without a key for that collection. Thus `go/modules` is a `GoModules` artifact; adding a `GoModules` key at the same path selects a `GoModule` artifact. For legacy modules, `batch` is an explicit path on the collection object, not a child of each item.

#### Artifact identity

Source artifact identity includes its type, complete source path, and collection lookup keys. Projected columns use collection types, not source paths.

Different fields can expose the same collection type with the same key. They share the collection column. Other dimensions must distinguish their artifacts; do not split the collection column or silently merge source artifacts.

For `before: Tests` and `after: Tests`, both collections can contain key `unit`. They share one `test` column. Their checks differ by `name=before.check` and `name=after.check`. A name relative to the nearest item would lose that distinction. The full module-root field path preserves it without another column.

An artifact can also differ from another by an absent coordinate. A check on a `Row` collection and a check on its `Cell` can share the path `rows/check`. The parent has no cell coordinate. Use an absence test to select it.

| Query | Selects |
| -- | -- |
| `?cell=left` | The cell key `left` |
| `?cell=` | An empty cell key |
| `?cell` | Any present cell key |
| `?!cell` | No cell coordinate |

The leading `!` is Dagger syntax. Decode the parameter name before reading it. Accept `?!cell=`, including its encoded form `?%21cell=`, with the same meaning. Reject a nonempty value for an absence test.

Repeated positive selectors for one dimension use OR. Absence tests use AND. Thus `?cell=left&cell=right` selects either key; `?cell=left&!cell` selects nothing. An unknown absent dimension keeps all known rows.

In the API, `filterDimensions(dimensions: ["Cell"], exclude: true)` keeps artifacts without that dimension. With several dimensions, all must be absent. An empty list excludes nothing. Resolve names against the full schema before applying the filter. Remove excluded collection levels before reading keys. An unavailable module remains a load failure when its schema cannot prove absence.

Absence tests are selectors. They do not add coordinates or table cells.

#### Projections

An address printed for people, such as a `dagger list` row, a check name, or an `Artifact.uri()` result, uses the smallest set of keys, in their shortest names, that selects that artifact in the addressed workspace. It must not depend on the source selection's visible columns. A schema or module change can alter this short form. A stored identifier, such as a Cloud check name or the address an engine sends a cloud engine, uses the full form: every key, and the full path in the type key.

Apply these rules when the link retains a type key or names a legacy batch.

- The type key is the shortest suffix of the path, on segment boundaries, that names one path among the loaded paths of that type with every collection dimension the address keys. Thus the up operation on each Docker Compose service is `?service=up&docker-compose-project=e2e&docker-compose-service=web`, and the batch operation on a project's services is `?service=batch/up&docker-compose-project=e2e`. Collection dimension names start with their module, so a short key with collection keys names the same path whichever modules are loaded.
- A batch that runs every item of its collection omits that collection's item keys: `?check=test&go-package=internal/bundle` for one `go test` of a whole package, not one `go-test` key per test. A batch of selected items keeps their keys.

A projection holds as a whole. A short type key given without the keys it was chosen with can name several paths. A key that names several paths selects them all, such as `?check=stale` for every staleness check. But a key that names both an operation on each item of a collection and the same operation on the collection's batch would run it twice: `?service=up&docker-compose-project=e2e` names `services/up` and `services/batch/up`. That is an error that lists the paths, never their union.

Distinct keys remain distinct even if `get` returns the same object. Filters never rewrite addresses or turn a collection artifact into a subset.

Alternatives in one filter use OR. Chained filters use AND. Unknown dimension names or keys match nothing. An ambiguous name is an error. A key filter applies to all collection values represented by its dimension. Skip keys that do not match, but retain the original collection for value lookup. A test absent from one module must not fail selection in another.

#### Table columns

Canonical coordinates have three parts: `module`, the enclosing collection item keys, and `name`. Use the full dot-separated path from the exposed module root for `name`. Keep literal schema field names, including collection fields. Examples: `python310.unit` and `modules.packages.tests.test`. Use `.` for the module root itself.

Legacy type-path coordinates remain compatibility selectors. They are not canonical table columns.

`Artifacts` is the table. `items` supplies its rows. Do not add a table wrapper.

Flattening produces all workspace dimensions and each artifact's full coordinates. Filters must not change these dimensions or coordinates. Column hiding is a separate display rule.

`Artifacts.dimensions()` returns the dimensions that vary across the selected rows. Hide a dimension when all cells are absent or every row has the same present value. A missing cell differs from every string, including `""`. An empty selection or a selection with one row has no varying dimensions.

`Artifacts.dimensions(all: true)` returns all workspace schema dimensions. This includes dimensions outside the selection and dimensions for empty collections. Filters and collection contents must not change this list. Do not discover collection keys to produce it.

`Artifact.dimensionKeys()` returns the row's coordinates in the visible dimensions of its source `Artifacts` snapshot. Use exactly the dimension set that the parent's `dimensions()` returns. Do not compute visibility from that row alone. A missing coordinate has no entry.

`Artifact.dimensionKeys(all: true)` returns all coordinates of that artifact. Keep these full coordinates internally, regardless of column hiding. Preserve the source snapshot's visible dimensions when an artifact is passed to another client or loaded by ID. An artifact with no source display context returns its full coordinates.

`dimensionItems` retains its source selection for display pruning. Read that source snapshot only when default `dimensionKeys()` needs it. `dimensionKeys(all: true)` must not discover descendant collections only to choose visible columns. Retain this source context across persistence.

The `all` argument applies only to that field. To render all columns, request `all: true` on both `Artifacts.dimensions` and `Artifact.dimensionKeys`.

Dimension identifiers are sorted and contain no duplicates. Collection `keys` and `list` retain author order. `Artifacts.dimensionKeys(dimension: ...)` remains the list of distinct present keys for that dimension in the selection.

#### Table columns and DAG links

These operations use different reference sets:

| Operation | Reference set | Rule |
| -- | -- | -- |
| Choose table columns | Selected artifacts | Omit constant or absent columns |
| Choose a link's selectors | Entire addressed workspace | Use the fewest selectors that select exactly that artifact |

`Artifact.uri()` chooses its pairs independently of the source selection. Account for any path and type constraints already encoded in the address. A hidden table column can still be required in a link. Conversely, a visible column can be redundant in a link.

For example, a table of docs checks can hide `module=docs`. If another module has the same check name, the link can still need `module=docs` to select the right check. Copying the table's visible coordinates into a link is not sufficient.

Use the complete, unfiltered workspace snapshot to choose the pairs. A child collection key can be unique across its parent collections. Do not keep a parent key when the remaining pairs already identify the artifact. If the same child key occurs under two parents, keep another pair that separates them.

Find the smallest set of selectors. Among sets of the same size, prefer fewer absence tests. Then prefer present collection keys, starting with the deepest collection, followed by `name` and `module`. Read depth from the parent-to-child coordinate order. Use dimension identifier order for remaining ties. Print absence tests after present keys, in identifier order.

For example, `dag+check://?golang-test=TestCloseAndReopen` is sufficient when that test names one Check in the full workspace. A package item can use `dag+golang-package://?golang-package=cmd/codegen` when that package key is unique across Go modules.

Filtering must not change an artifact's link. Compute the proof against the source workspace with all selection filters cleared. The scheme supplies the type constraint. Types with the same scheme spelling belong to the same address proof. An untyped address needs a proof across all artifact types. Cache the unfiltered discovery snapshot. Never use the displayed rows as the address reference set.

Present keys can also match artifacts with extra collection coordinates. An absence test excludes those artifacts. For example, a parent check can use `dag+check://?row=top&!cell`, while a child uses `dag+check://?cell=left`.

If runtime discovery cannot prove a short link, use the full path and required parent keys. Keep schema presence and absence tests in this form too. The same rule applies to stored addresses, bound values, and synthetic staleness checks. Add the module selector when public paths from different modules overlap, or when the workspace schema is incomplete. A schema-only link selects a collection level; it does not read keys.

A workspace content change can require a longer address. A relative link is resolved against the receiving workspace. An absolute link pins the source workspace commit. If the full snapshot is unavailable, retain the existing complete address.

The scheme selects the artifact type. Apply `+check` as a type filter before choosing the artifact. With `uri(typeAssertion: true)`, the scheme can replace the type constraint in the query. For a package and test with one check, a link can be `dag+check://?go-package=./app/foo&go-test=TestFoo`. If that test has several checks, keep the field discriminator.

No new link API is needed. The `all` argument controls table metadata. It does not control `uri()`. The existing `absolute` option supplies the workspace address when the link must name its source workspace.

Load schema metadata across modules to compute column names. Enumerate items for selected rows or a complete address proof. If the schema is incomplete, keep the full field path and every parent collection key. Use exact dimension identifiers when the catalog cannot prove that an alias is unique. A failed unfiltered discovery also keeps a full address.

In the CLI, `dagger check --list --all --format=link` prints links for individual checks. Tables use canonical coordinates and the same display pruning rule as the API. They put collection columns first, then `module` and `name`, and always include the DAG link. `--format=cli` prints complete commands. Use `--name` with the full module-root field path, such as `--name=python310.unit`. Do not shorten that path to a suffix. Omit it only when the remaining selectors identify the schema path. If the same name occurs below a child collection, add an exact path selector. Older engines can retain their type-path selectors. `dagger list <collection>` lists individual items by default. A collapsed row represents a selection and retains its field path.

#### Discovery and evaluation

Listing can construct collections and read their keys. To discover nested keys, it can call a parent's `get` and the field that returns a child collection. These parent objects can also be artifacts. Listing must not call leaf value functions, such as `GoTest.container`. It must not run checks, generators, or batch functions.

For static object discovery, traverse module-defined fields that can be called without user input. Traverse engine-defined fields only when they carry `@check`, `@generate`, `@up`, or `@agent`. The rule applies to the field definition. A module field returning a Container is an artifact, but its unmarked `rootfs` field stops discovery. A Changeset exposes its marked `stale` check. Stop when an object type repeats on the current path.

Batch results are leaf artifacts. Discovery lists their addresses but does not traverse them.

Store the discovery scope and filters in `Artifacts`. Expand collections when a result is requested. Apply path, type, and parent-key filters before expanding child collections. Keep the existing traversal limits for cycles, nullable values, raw lists, and required arguments.

Reading metadata from an existing `Artifact` does not evaluate its value. `value()` follows the complete address in the source workspace, including after a module call or loading by ID.

#### Key text

Artifact and delta keys use the same string form. Strings and string scalars use their value. Enums use the GraphQL value name, such as `RED`. Numbers and booleans use their JSON form. Conversion back uses the declared key type and must preserve key identity. Keep the typed key for `get` and `subset`. Do not infer its type from the text.

### 4. Select items by dimension

Use dimension keys in the query part of a DAG address:

```text
dag://go/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

Each pair accepts an exact dimension identifier, a short name, or a qualified name. Names must be unambiguous in the workspace. Repeat a dimension to select alternative keys:

```text
dag://go/modules/tests/container?go-module=sdk/go&go-test=TestConnect&go-test=TestQuery
```

Use the key text defined above. `/` needs no escape in a key. Percent-encode `&`, `=`, `#`, `+`, `%`, and spaces. A bare dimension name, such as `?go-module`, selects any key. With `=`, an empty value selects the empty string key.

When an exact path ends at a collection field, omit its dimension to select the collection object. Supply its dimension to select items:

| Address | Selection |
| -- | -- |
| `dag://go/modules` | The `GoModules` collection |
| `dag://go/modules?go-module=sdk/go` | One `GoModule` item |
| `dag://go/modules?go-module` | All items in that collection |

The path is a short form of the type dimension key. These addresses select the same artifact:

```text
dag://go/modules/tests/container?go-module=sdk/go&go-test=TestConnect
dag://?container=go/modules/tests/container&go-module=sdk/go&go-test=TestConnect
```

The two forms differ in one way: the path accepts glob patterns, such as `go/**`. A type dimension key names the path it equals, so a stored address stays exact. Otherwise it names each path it is a suffix of among the paths with the address's collection dimensions (see [Projections](#projections)). A leading `/` accepts only a full path.

Parent dimensions still select the containing objects. A complete address has one key for each item selection along the path. Missing parent keys or alternative keys can select several artifacts. `Workspace.resolve` uses the Artifacts rule: require exactly one match.

For each dimension, `Artifact.uri()` tries its short name, then its qualified name, then its exact identifier. It uses the first name that is unambiguous in the workspace. Order pairs from parent to child. `Artifacts.uri` uses exact identifiers for selectors over several paths.

When `uri` returns an address, `artifacts.filterUri(a.uri)` must select the same set as `a`. One address cannot express OR across different dimensions or collection key exclusions. Reject `uri` for those selections; keep the filters valid for listing and execution.

### 5. Use the existing CLI and resolver

```console
$ dagger list go-tests go --go-module=sdk/go
TestConnect
TestQuery

$ dagger list -a go/modules/tests/container --go-module=sdk/go --go-test=TestConnect
dag://?container=go/modules/tests/container&go-module=sdk/go&go-test=TestConnect
```

Use the Artifacts flags `--<dimension>=<key>`. They accept exact identifiers, short names, or qualified names, unambiguous in the workspace. If a short name conflicts with a command flag, help and runnable lists use the qualified name. A DAG link query also accepts dimension names that conflict with flags. Repeat flags for alternatives; do not split values on commas. Use schema metadata to register flags, including for empty collections.

A flag has the same meaning as one query pair, and the two combine. Quote an address that contains `&`:

```console
$ dagger list -a 'dag://go/modules/tests/container?go-module=sdk/go&go-test=TestConnect'
dag://?container=go/modules/tests/container&go-module=sdk/go&go-test=TestConnect
```

`Workspace.resolve` accepts the same complete address:

```text
ws.resolve("dag://go/modules/tests/container?go-module=sdk/go&go-test=TestConnect").container()
```

`resolve` requires exactly one match. Missing keys or repeated dimensions can produce several matches; they use the same selection rules as `artifacts`. Unknown or unused selectors produce no match. Reject malformed selectors and paths that require two item selections for the same dimension. Workspace errors never trigger external resolution.

Default `uri()` output must resolve to the same value.

`dagger call`, the shell, and generated clients use the standard collection API directly:

```console
dagger call go modules get --key=sdk/go tests subset --keys=TestConnect --keys=TestQuery batch run sync
```

### 6. Select checks and generators

`dagger check` and `dagger generate` use the shared Artifacts selection API. Add dimension filters to their path and directive filters. The dimension names, key text, and filter rules are the same as for `dagger list`.

Resolve dimension names in the workspace. Then merge keys for the same dimension with OR, and combine different dimensions with AND. An omitted dimension selects all keys. An empty key list matches nothing.

For execution, intersect the filters with each collection's keys, then call `subset` with the matching keys. Passing the raw filter to `subset` would fail when a requested test exists in another Go module only.

For each collection value, a batch check replaces a check with the same name on its item type. Run it once on the selected subset. Other item checks run once per selected item. Apply the same rule to generators. Do not run a batch for an empty selection, and do not combine subsets from different parent items.

Apply batch replacement in the shared Artifacts evaluator, before reading selected checks or evaluating generators. The evaluator needs the full selection to form each subset. It must not invoke the item functions that the batch replaces. Direct calls to an item function keep their normal behavior.

The base design can read each static `Check.pass` directly. Collection selections with a batch must first pass through batch replacement. This is an extension to Artifacts evaluation.

```console
$ dagger check go/modules/tests/run --go-module=sdk/go --go-test=TestConnect --go-test=TestQuery
# One batch run for these two tests in sdk/go.

$ dagger check go/modules/tests/lint --go-module=sdk/go --go-test=TestConnect --go-test=TestQuery
# Two item checks if GoTests has no batch lint check.

$ dagger check 'dag://go/modules/tests/run?go-module=sdk/go&go-test=TestConnect&go-test=TestQuery'
# The same selection as the first command.
```

Path filters select the effective check or generator name, before batch replacement. `check -l` and `generate -l` print one address per line, with the selected keys, and show whether execution uses a batch. A batch function with no matching item function runs once on the subset under its own name.

### First implementation

Ship collection declarations, the standard API, delta, dimensions, nested discovery, keyed resolution, and batch selection together. Support Go, Dang, Python, and TypeScript authoring. Generate clients from the public collection schema. Expose the new collection fields and types in the v1 API.

Extend the discovery and evaluation supplied by #14178. Use its address parser, check projection, error handling, and command integration.

Verify:

- Collection validation, typed keys, author order, empty subsets, and rejection of unknown or duplicate subset keys.
- Delta naming and overrides, unchanged selections, chained subsets, and key text conversion.
- Nested and empty collections, one collection type exposed through several fields sharing one column, and directly nested distinct collection types.
- Short, qualified, and exact names select the same dimension. Name conflicts and filter order do not change the selected dimension.
- Adding unrelated fields does not change complete addresses. Equal objects at different keys retain distinct addresses.
- Discovery does not evaluate leaf values or excluded parent items.
- Collection and item addresses at the same path, flag and query equivalence, and dimension filters preserved by `uri` and `filterUri`.
- Artifacts, subsets, and base references survive module calls and loading by ID. Normal state copies preserve the base without a declared delta field. New collections start a new base. Evaluation uses the source workspace, including its edits.
- Batch replacement runs once per selected collection value, receives the selected keys and delta, and skips empty selections.
- Item functions replaced by a batch do not run. Other item functions still run once per selected item.
