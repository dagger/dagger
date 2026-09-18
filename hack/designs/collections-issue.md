# Collections

## Context

[Workspace artifacts](https://github.com/dagger/dagger/issues/14164), implemented in [#14178](https://github.com/dagger/dagger/pull/14178), give workspace objects an address. They provide discovery, filters, and resolution in the source workspace.

The first implementation handles static objects. This design adds dynamic collections and keyed selection.

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

The author function returns `Void`. The engine exposes it as `Check!`; see [7](#7-project-checks-as-artifacts).

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

Use dimension names for artifact selectors. Rename the collection selector fields reserved in #14178 to the dimension fields below. Rename `pretty` to `uri`.

```graphql
extend type Workspace {
  artifacts(include: [String!]): Artifacts!
  resolve(value: String!): Address!
}

type Artifact implements Node {
  dimensionKeys: [ArtifactDimensionKey!]!
  directives: [String!]!
  id: ID!
  path: [String!]!
  uri(absolute: Boolean = false, dimensionKeys: Boolean = true, typeAssertion: Boolean = false): String!
  value(arguments: JSON = "{}"): Node!
}

type ArtifactDimensionKey implements Node {
  dimension: String!
  id: ID!
  key: String!
}

extend type Artifacts {
  filterTypes(types: [String!]!): Artifacts!
  filterPath(path: [String!]!): Artifacts!
  filterDirectives(directives: [String!]!): Artifacts!
  filterDimensions(dimensions: [String!]!): Artifacts!
  filterDimensionKeys(dimension: String!, keys: [String!]!): Artifacts!
  filterUri(uri: String!): Artifacts!
  withoutUri(uri: String!): Artifacts!
  dimensions: [String!]!
  dimensionKeys(dimension: String!): [String!]!
  types: [String!]!
  items: [Artifact!]!
  one: Artifact!
  uri: String!
  values(failFast: Boolean = false): [ArtifactResult!]!
}

type ArtifactResult {
  artifact: Artifact!
  value: Node
  error: Error
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

Its `uri()` is a DAG address. See [4](#4-dag-address-syntax).

```text
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

Collection selection inserts `get(key: ...)` calls. `get` and the keys are not path segments. The engine enumerates item objects and their children. It also retains the collection object at its own path, without a key for that collection. Thus `golang/modules` is a `GoModules` artifact; adding a `Golang.modules` key at the same path selects a `GoModule` artifact. `batch` is an explicit path on the collection object, not a child of each item.

Artifact identity is the path plus all dimension keys. Distinct keys remain distinct even if `get` returns the same object. Filters never rewrite addresses or turn a collection artifact into a subset.

Alternatives in one filter use OR. Chained filters use AND. Unknown dimension names or keys match nothing. An ambiguous name is an error. A key filter applies to all collection values represented by its dimension. Skip keys that do not match, but retain the original collection for value lookup. A test absent from one module must not fail selection in another.

`dimensions()` reports identifiers represented by selected artifacts. `dimensionKeys()` reports their keys. Both results are sorted and contain no duplicates. Collection `keys` and `list` retain author order.

#### Discovery and evaluation

Listing can construct collections and read their keys. To discover nested keys, it can call a parent's `get` and the field that returns a child collection. These parent objects can also be artifacts. Listing must not call leaf value functions, such as `GoTest.container`. It must not run checks, generators, or batch functions.

Store the discovery scope and filters in `Artifacts`. Expand collections when a result is requested. Apply path, type, and parent-key filters before expanding child collections. Keep the existing traversal limits for cycles, nullable values, raw lists, and required arguments.

Reading metadata from an existing `Artifact` does not evaluate its value. `value()` follows the complete address in the source workspace, including after a module call or loading by ID.

#### Key text

Artifact and delta keys use the same string form. Strings, string scalars, and enums use their value. Numbers and booleans use their JSON form. Conversion back uses the declared key type and must preserve key identity. Keep the typed key for `get` and `subset`. Do not infer its type from the text.

### 4. DAG address syntax

A DAG address is a URL that selects workspace artifacts:

```text
[dag[+<type>]://][<workspace>@<version>:][<path>][?<dimension>=<key>&...]
```

| Part | Meaning |
| -- | -- |
| `dag://` | Marks a workspace artifact address. |
| `+<type>` | The artifact type, in CLI case: `dag+container://`. |
| `<workspace>@<version>:` | The Git address of the workspace, written as a Go import path, at a branch, tag, or commit. Default: the current workspace. |
| `<path>` | The field path. In a filter, it can be a pattern. An empty path selects all artifacts. |
| `?<dimension>=<key>` | A dimension name and a key. |

Input examples:

```text
golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
dag+container://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
dag://github.com/dagger/dagger@main:golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
dag://github.com/dagger/dagger@release/v1:golang/modules/test
dag://github.com/dagger/dagger@c624e1f:engine-dev/playground
```

#### Scheme

`dag://` is optional where the argument can only be an artifact. It is required where the argument also accepts an external reference, such as a container image. There, a value without the scheme keeps its external meaning. An address with `dag://` never triggers external resolution.

#### Workspace and version

`@<version>:` ends the workspace. A ref can contain `/` but not `:`, so the split is exact. Git uses the same `ref:path` form. The `:` is required even when the version has no `/`.

`@` accepts a branch, tag, or commit on input. `@HEAD` is the remote default branch. Output always has the commit.

The transport is not part of the address. Git configuration selects it. All addresses in one command must name the same workspace, and it must agree with `--workspace`.

#### Tree addresses

The same scheme also accepts a Git tree address for a module, workspace, or file. Its form is `<repo>/<path>@<version>`, with nothing after the version:

```text
dagger -m dag://dagger.io/go api functions
dagger -W dag://github.com/dagger/dagger/docs@main shell -l
dagger call with-file --source=dag://github.com/dagger/dagger/README.md@main
```

The position of `@` tells the two forms apart. A path before `@` is in the tree. A path after `@<version>:` is an artifact. Use the existing `-m` behavior to find the repository root. Local paths keep `./` and `-W ../x`.

#### Query

Pairs accept exact dimension identifiers, short names, or qualified names. Names must be unambiguous on the selected paths. Use the key text defined above. A complete address has one pair per item selection. In a filter, repeat a dimension for alternatives. `/` needs no escape. Percent-encode `&`, `=`, `#`, `+`, `%`, and spaces in keys.

#### Selection

An address selects a set. A complete address has an exact path and all item keys, and matches at most one artifact. A pattern, an empty path, a missing parent key, or a repeated dimension can match several.

When an exact path ends at a collection field, omit its dimension to select the collection object, and supply it to select items. `dag://golang/modules` selects `GoModules`. `dag://golang/modules?go-module=sdk/go` selects one `GoModule`. `dag://golang/modules?go-module` selects all its items.

`Artifacts.filterUri(uri)` applies an address as one filter. It equals the chain of `filterPath`, `filterTypes`, and `filterDimensionKeys` that the address encodes. `include` on `Workspace.artifacts` takes path patterns only. `Workspace.resolve(uri)` is `artifacts.filterUri(uri).one()`, and `Address` stays a single-artifact API. When `one()` finds two or more matches, the error lists them, one `uri` per line. Copy the correct line to fix the address.

In a set, `+<type>` is a filter. On one artifact, it is an assertion: a different type is an error.

#### Output

`Artifact.uri()` and the CLI print `dag://` and no type. For each dimension, try its short name, then its qualified name, then its exact identifier. Use the first name that is unambiguous on the selected path. Order pairs by their position along the path, from parent to child.

- `uri(absolute: true)` adds `<workspace>@<commit>:`. It fails if the workspace has no Git address.
- `uri(dimensionKeys: false)` omits the query. The result is a path selector, and can select a collection object or several artifacts.
- `uri(typeAssertion: true)` adds `+<type>`.

`Artifacts.uri` is the selector for the whole selection, and `artifacts.filterUri(a.uri)` selects the same set as `a`. Encode several include patterns as `{a/**,b/**}`, several types as `dag+container+directory://`, and "any key in this dimension" as a query key with no value. Use exact identifiers when the selector spans several paths. Normalize chained filters without changing which keys apply to which paths.

### 5. Use the existing CLI and resolver

```console
$ dagger artifact dimensions
NAME               IDENTIFIER
app-dependencies   App.dependencies
go-test            GoModule.tests
golang-modules     Golang.modules

$ dagger artifact dimensions golang
NAME        IDENTIFIER
go-module   Golang.modules
go-test     GoModule.tests

$ dagger artifact keys go-test golang --go-module=sdk/go
TestConnect
TestQuery

$ dagger artifact list golang/modules/tests/container --go-module=sdk/go --go-test=TestConnect
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

Keep the dynamic flags from #14178 as `--<dimension>=<key>`. Rename its generic flag to `--dimension-key=DIMENSION=KEY`. Both accept exact identifiers, short names, or qualified names, unambiguous on the selected paths. The generic form also works when a name conflicts with a command flag. Repeat flags for alternatives; do not split values on commas. Use schema metadata to register flags, including for empty collections.

A flag has the same meaning as one query pair, and the two combine. Flags need no shell quotes; an address with `&` does:

```console
$ dagger artifact list 'dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect'
dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect
```

`Workspace.resolve` accepts the same complete address:

```text
ws.resolve("dag://golang/modules/tests/container?go-module=sdk/go&go-test=TestConnect").container()
```

`resolve` requires exactly one match. Missing keys or repeated dimensions can produce several matches; they use the same selection rules as `artifacts`. Unknown or unused selectors produce no match. Reject malformed selectors and paths that require two item selections for the same dimension. Workspace errors never trigger external resolution.

Keep `/` as the path separator. In a relative address, accept `:` as a path separator for compatibility. In an absolute address, the first `:` after `@` ends the version, and later colons in the path are the old separator. Neither separator splits a key. Default `uri()` output must resolve to the same value.

`dagger call`, the shell, and generated clients use the standard collection API directly:

```console
dagger call golang modules get --key=sdk/go tests subset --keys=TestConnect --keys=TestQuery batch run sync
```

### 6. Select checks and generators

`dagger check` and `dagger generate` build their selection with the `Artifacts` filters of section 3: path patterns, `filterDimensionKeys`, and `filterDirectives`. The dimension names, key text, and filter rules are the same as for `dagger artifact`.

Resolve dimension names within the selected paths. Then merge keys for the same dimension with OR, and combine different dimensions with AND. An omitted dimension selects all keys. An empty key list matches nothing.

For execution, intersect the filters with each collection's keys, then call `subset` with the matching keys. Passing the raw filter to `subset` would fail when a requested test exists in another Go module only.

For each collection value, a batch check replaces a check with the same name on its item type. Run it once on the selected subset. Other item checks run once per selected item. Apply the same rule to generators. Do not run a batch for an empty selection, and do not combine subsets from different parent items.

```console
$ dagger check golang/modules/tests/run --go-module=sdk/go --go-test=TestConnect --go-test=TestQuery
# One batch run for these two tests in sdk/go.

$ dagger check golang/modules/tests/lint --go-module=sdk/go --go-test=TestConnect --go-test=TestQuery
# Two item checks if GoTests has no batch lint check.

$ dagger check 'dag://golang/modules/tests/run?go-module=sdk/go&go-test=TestConnect&go-test=TestQuery'
# The same selection as the first command.
```

Path filters select the effective check or generator name, before batch replacement. `check -l` and `generate -l` print one address per line, with the selected keys, and show whether execution uses a batch. A batch function with no matching item function runs once on the subset under its own name.

### 7. Project checks as artifacts

Two things block a uniform artifact walk:

- A `+check` function returns `Void`, which is not an object.
- A `+generate` function's path names two things: its `Changeset`, and the synthetic check that passes when the `Changeset` is empty. One address must name one artifact.

**Check type.** The engine exposes each `+check` function as `Check!` in place of `Void`. The module contract does not change: calling the function performs the check, and an error is a failure. The function description supplies `assertion`.

```graphql
"""
One check. Reading `pass`, `error`, or `sync` runs it. Reading other fields does not.
"""
type Check {
  """The assertion, in present tense. False when the check fails."""
  assertion: String
  """Runs the check."""
  sync: Check!
  """The result. Runs the check."""
  pass: Boolean!
  """The failure, if any. Runs the check."""
  error: Error
  """An optional report: junit files, screenshots, or other domain-specific content."""
  report: Directory
}
```

Rewrite the return type where the engine adds module namespaces to functions (`__withReturnType` in `core/module.go`). Reshape the existing `Check` object to this schema. Require author check functions to return `Void`.

**Generator check.** Add one field to `Changeset`, and remove the synthetic check at the generator's path:

```graphql
extend type Changeset {
  """A check that passes when the changeset is empty. Runs the generator."""
  stale: Check!
}
```

A generator's check now has the address `<generator path>/stale`. `dagger check foo/bar` still selects it: a plain path filter selects that path and its children. `check -l` prints the real address. Generators supplied by the engine must also be schema fields that return `Changeset`.

**Walk rule.** The walk visits eligible object fields, with the existing limits for cycles, nullable values, raw lists, and required arguments. It collects an artifact and descends when one of two rules applies:

- The field belongs to a module type.
- The field has a user-facing directive: `@check`, `@up`, `@generate`, or `@agent`.

So a module publishes every eligible object, and the engine publishes only targets. `Changeset.stale` is a check, so the engine marks it `@check` and the walk collects it. `Container.rootfs` and `Check.report` have no directive, so the walk stops at those fields and gives them no address. The walker holds no list of types or fields.

### 8. Replace the group APIs with Artifacts

Use `Artifacts` for discovery, selection, and evaluation. Remove `CheckGroup`, `GeneratorGroup`, `UpGroup`, `TerminalGroup`, and `AgentMiddlewareGroup`. Remove the workspace fields that return them: `checks`, `generators`, `services`, `terminals`, and `agents`. Callers use `Workspace.artifacts` directly.

Keep the individual result types, such as `Check`, `Changeset`, and `Service`. Commands keep their final actions: report checks, apply changes, start services, open terminals, or compose agent middleware. Selection and evaluation are shared.

Today `core/modtree.go` has six walks over the same tree: `RollupChecks`, `RollupUp`, `RollupGenerator`, `RollupAgents`, `RollupTerminals`, and `ModuleArtifactNodes`. Replace them in the following order. Land each step on its own, with the integration suite green after each one. All four steps are required for this release.

1. **One walk.** The walk predicate returns two values: collect and descend. Section 7 gives the collect rule. A directive alone does not stop descent. The walk must reach `Changeset.stale` below a generator. The six walks become filters over this walk. Terminal selection uses `filterTypes(["Container", "Directory"])`.
2. **Directives on artifacts.** `Function.Directives()` already emits `@check`, `@up`, and `@agent`. Add `@generate`. Carry them onto `Artifact.directives` and `Artifacts.filterDirectives`. Remove the four booleans on `ModTreeNode`.
3. **Select through artifacts.** Port CLI commands and internal callers to `Workspace.artifacts`. Use `filterDirectives` for checks, generators, services, and agent middleware. Use `filterTypes` for terminals. Remove calls to the group APIs.
4. **One evaluation.** `Artifacts.values(failFast:)` evaluates independent artifacts in parallel, with one span per artifact. It records each error in an `ArtifactResult`. The wrapper is necessary: a raised error in a non-null field ends the whole list. Commands apply their final actions to the results. Agent middleware keeps its composition order. Remove the group types, their workspace fields, the separate rollup methods, and the per-command runners in `modtree.go`.

`Check.pass` returns `false` and fills `error` for an assertion failure. Callers can read checks directly in one query. The CLI uses `values(failFast:)` to preserve failure isolation and cancellation:

```graphql
artifacts(include: ["golang/modules/tests/run"])
  .filterDimensionKeys(dimension: "Golang.modules", keys: ["sdk/go"])
  .filterDirectives(directives: ["check"])
  .items { uri  value { ... on Check { pass  error { message } } } }
```

`dagger shell` needs neither `values` nor a directive: `filterTypes(["Container", "Directory"]).one`, then `terminal` on the value. Port it first. It proves steps 1 to 3 with the smallest diff.

Keep these execution behaviors through all four steps:

- Bind the overlay workspace into each target, so an injected `Workspace!` resolves against overlay edits.
- An entrypoint module drops its prefix in the printed name. Artifacts print the same name.
- Scale-out sends a workspace recipe and artifact address to a remote engine. The check command reads `pass` and `error` there. It does not transfer engine-local result handles.
- A module that does not load is an artifact whose value is a failed `Check`.

### First implementation

Ship collection declarations, the standard API, nested artifact discovery and resolution, and check and generator selection together. Support Go, Dang, Python, and TypeScript authoring. Generate clients from the public collection schema. Expose new schema fields in the v1 API only, as in #14178.

Commands support relative addresses and absolute Git workspace addresses, with an optional `dag://` scheme, type assertions, paths, and queries. One command selects one workspace. An absolute address binds that workspace before discovery. Tree addresses remain reserved. API filters operate on their existing workspace; they do not load a different workspace.

Ship the `Check` projection and `Changeset.stale` with the walk, and all four steps of the port, `dagger shell` first. The release removes the group APIs and their separate runners.

Verify:

- Collection validation, typed keys, subset order, and batch receivers with the selected keys and the correct delta.
- Nested collections, empty collections, one collection type exposed through several dimensions, and equal objects at distinct addresses.
- Name resolution: short, qualified, and exact names select the same dimension; conflicts fall back in that order; adding unrelated fields does not change a complete address; filter order does not matter.
- Discovery without evaluation of leaf values, and no work in excluded parent items.
- Address encoding and resolution with and without `dag://`; flag and query equivalence; `artifacts.filterUri(a.uri)` selects the same set as `a`; `one()` errors list every match.
- Artifacts, subsets, base, and delta preserved across workspace edits, module calls, and loading by ID.
- The `Check` projection, `Changeset.stale`, and the walk rule.
- Batch replacement, per-item fallback, and unchanged execution results for static artifacts.
- All group API callers use `Artifacts`. No separate group walk, filter, or runner remains.
