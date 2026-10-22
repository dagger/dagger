# Implement filtered artifact views

## Problem

Native batching lets the engine replace item checks or generators with a collection function. It does not prevent repeated work across levels. A Go test can run individually, in a package batch, and in a module batch.

Artifact filters select entries but leave parent collections unchanged. Module code cannot tell which items were selected.

## Solution

Give each selection one filtered workspace view. Replace collection references with filtered copies. Expose individual checks. Let module code share their work through normal calls and keep separate results.

Remove native batching and `Artifacts.values()`. Defer `withArtifacts()`.

**Status:** Design handoff. Not implemented.

Code reviewed: `ce6dc9f9403b52067f5831149eab4871f857185d`. Check the current code before editing.

## Contents

- [Problem](#problem)
- [Solution](#solution)
- [Terms](#terms)
- [Schema changes](#schema-changes)
- [Filter behavior](#filter-behavior)
- [CLI behavior](#cli-behavior)
- [Shared execution](#shared-execution)
- [Implementation](#implementation)
- [Required checks](#required-checks)
- [Open decisions](#open-decisions)

## Terms

| Term | Meaning |
|---|---|
| Artifact | A workspace field identified by its path and any collection keys needed to reach it. Its description has an address; evaluating it produces a value. |
| Selection | The set of artifacts described by an `Artifacts` value. |
| Collection | An object with stored keys, item lookup through `get`, and filtering through `subset`. |
| Collection dimension | An axis used to select collection keys, such as `go-package` or `go-test`. |
| Static filter | A filter on schema fields or result types, rather than collection keys. |
| Workspace view | The collection state and object references used to evaluate a selection. |
| Union | Combining separate selections into one. `withArtifacts()` performs union today. |

Selection and view are different. Selection says which fields to evaluate. View says which collection objects those fields use.

## Schema changes

- **Filters:** Keep their methods. Change the collection state used to evaluate matching artifacts.
- **Conversions:** Keep `asXXX()`. These methods return typed objects, such as `Check`.
- **Returns:** Keep the current lists for this work. Review their shape separately.

Docstrings describe behavior. Comments mark changes. The shown `Artifacts` signatures stay unchanged. Other fields are omitted.

```graphql
# CHANGED: artifacts and converted objects retain the filtered view.
"""A selection of workspace artifacts and the view used to evaluate them."""
type Artifacts {
  # UNCHANGED.
  """Keep the listed types. If exclude is true, remove them instead."""
  filterTypes(types: [String!]!, exclude: Boolean! = false): Artifacts!

  # CHANGED: also reduce collections and replace references in the new view.
  """
  Keep artifacts with any listed key in this dimension.
  For collections, use matching subsets. Skip absent keys.
  Keep the source view unchanged.
  """
  filterDimensionKeys(dimension: String!, keys: [String!]!): Artifacts!

  # CHANGED: returned artifacts retain this view for later evaluation.
  """List artifact descriptions. Discover collection keys as needed."""
  items: [Artifact!]!

  # CHANGED: all conversions below use this view.
  """Return checks without running them. Fail on other types."""
  asChecks: [Check!]!

  """
  Return generators without running them. Fail on other types.
  Skip module load failures, because generation can repair them.
  """
  asGenerators: [Generator!]!

  """Return services without starting them. Fail on other types."""
  asServices: [Service!]!

  """Evaluate Changeset fields and return their values. Fail on other types."""
  asChangesets: [Changeset!]!

  """Return expertise functions without running them. Fail on invalid signatures."""
  asExpertise: [Expertise!]!

  # REMOVED: values. Do not add another general execution method.
  # DEFERRED: withArtifacts. Do not merge separate views in this version.
}
```

Remove the batch namespace. Move ordinary author functions onto the collection. This example shows an existing `selected` function:

```graphql
# BEFORE: unrelated fields are omitted.
type GoTests {
  # REMOVED FIELD.
  """Return collection-level functions."""
  batch: GoTests_Batch!
}

# REMOVED TYPE: move its functions onto GoTests.
type GoTests_Batch {
  # MOVED to GoTests.selected.
  """Return the selected test keys."""
  selected: [String!]!
}
```

```graphql
# AFTER: example module type, not a new engine type.
"""A collection of tests."""
type GoTests {
  # UNCHANGED: standard collection operations.
  """Return current keys in author order."""
  keys: [String!]!

  """Return current tests in key order."""
  list: [GoTest!]!

  """Return this test. Fail if the key is absent."""
  get(key: String!): GoTest!

  """Copy the collection with these keys in author order. Fail on absent or duplicate keys."""
  subset(keys: [String!]!): GoTests!

  # ADDED HERE: moved from GoTests_Batch, not a new author function.
  """Return the selected test keys."""
  selected: [String!]!

  # REMOVED: batch and its generated type.
  # ALSO REMOVED: CollectionTypeDef.batchType metadata.
}
```

Keep `delta` support. Remove `ArtifactResult` if no remaining API uses it. New public types or methods require approval.

## Filter behavior

Apply the view to every artifact type. Filtering must leave the source selection and its objects unchanged.

For each collection dimension filter:

1. Find requested keys that exist in each collection.
2. Call `subset` with those keys.
3. Replace ordinary references to the source collection in the new view.

References through other fields, including child-to-parent references, must see the same subset. Preserve `CollectionBase`, the original collection used by `delta` to report key changes. Do not replace this history reference.

If TestFoo and TestBar are selected, both checks see both keys through their parent. They can therefore request the same shared test command.

Direct `subset` calls reject absent keys. Artifact filters must remove absent keys before calling `subset`, because a requested test may exist in only some packages. An empty match produces an empty subset.

Static filters only narrow the selection. They retain the current collection view. Excluded fields remain callable. Thus, module code can see selected collection members but cannot infer the complete artifact selection.

| Filters | Result |
|---|---|
| One call with `[TestFoo, TestBar]` | Either matching test |
| `[TestFoo, TestBar]`, then `[TestBar]` | TestBar |
| `[TestFoo]`, then `[TestBar]` | No tests |

## CLI behavior

Repeated flags form one filter with alternative keys:

```sh
dagger check -l --go-test=TestFoo --go-test=TestBar
```

Core listing query, with command settings omitted:

```graphql
query {
  currentWorkspace {
    artifacts {
      filterTypes(types: ["Check"]) {
        filterDimensionKeys(
          dimension: "go-test"
          keys: ["TestFoo", "TestBar"]
        ) {
          items { uri }
        }
      }
    }
  }
}
```

`items` returns descriptions, not `Check` values. Discovery may construct parent objects to find keys. It must not run checks.

Without `-l`, replace `items { uri }` with:

```graphql
asChecks {
  sync {
    pass
    error { message }
  }
}
```

Checks are deferred: creating them does not run them. `sync` runs each check and retains its result. Read `pass` and `error` from that result to avoid another execution request. The CLI fails if a check fails or the query returns an error.

This query does not define scheduling or handling of failures during conversion. Those need a separate implementation.

Keep multiple paths with shared filters in one selection. Reject separately filtered addresses that require union. Applying their filters globally could select different combinations.

## Shared execution

Expose one check per test. A normal module helper builds a command from the filtered parent collections and returns structured results:

```text
TestFoo.check -> module.testResults() -> shared command
TestBar.check -> module.testResults() -> shared command
```

Each check reads its own result. Expected test failures must still produce results. Missing reports and command failures need explicit handling.

Matching calls can share cached work. Keep check addresses for reporting, but out of the helper's cache key. This does not guarantee exactly one execution.

Remove check and generator markers from functions used only to group item work. These markers make functions visible to the commands. Keeping them would retain both group and item checks and repeat work.

Each generator must return its own intended output. Do not return a full shared Changeset from every item. One shared execution also has one trace parent; define how its output relates to individual checks.

## Implementation

1. Resolve the open decisions for the code being changed. Build a nested collection example with parent references and a shared results helper.
2. Implement view state and reference replacement. Copying `Query` is insufficient; it does not contain all module objects.
3. Preserve the view through module calls, stored ID loads, cached results, deferred execution, and later object creation. Include the view in cache keys before lookup. Preserve source objects and IDs.
4. Migrate all `values()` callers: check, generate, start, call, and cloud execution. Preserve arguments, command settings, cancellation, concurrency limits, error reporting, and trace names.
5. Send the full view and individual target to cloud execution. Related checks need a shared execution scope. Current remote checks use separate sessions.
6. Remove native batch replacement and batch schema. Migrate module functions and markers. Update affected SDKs, generated clients, and documentation.

CLI and module callers must use the same view. Equivalent views must still allow common helper calls to share work.

| Area | Files |
|---|---|
| Filters and discovery | [artifacts.go](../../core/artifacts.go), [artifact_collection.go](../../core/artifact_collection.go) |
| Subset, base, and delta | [collection_object.go](../../core/collection_object.go) |
| References and cache inputs | [object.go](../../core/object.go), [objects.go](../../dagql/objects.go) |
| Conversions | [schema/artifacts.go](../../core/schema/artifacts.go) |
| Batch removal | [artifact_batch.go](../../core/artifact_batch.go), [collection_typedef.go](../../core/collection_typedef.go), [schema/collection.go](../../core/schema/collection.go) |
| CLI | [artifact_execution.go](../../internal/cmd/dagger/artifact_execution.go), [artifacts.go](../../internal/cmd/dagger/artifacts.go) |
| Cloud | [artifact_cloud.go](../../core/artifact_cloud.go) |

## Required checks

Read [engine-debugging](../../skills/engine-debugging/SKILL.md) before running tests.

| Case | Required result |
|---|---|
| Filtering | All artifact types see the subset. Sources stay unchanged. Empty subsets work. |
| References | Parent references agree. Delta retains the original base. |
| Calls and caching | The view survives ID loads, deferred calls, and later objects. Different views do not return each other's results. |
| Shared work | Helpers share work. Group markers do not cause duplicate work. Checks retain separate outcomes. |
| Result reads | Reading pass and error from a retained result does not rerun the check. |
| CLI | Repeated keys use OR; chained filters intersect. Listing runs no checks. Unsupported unions fail clearly. |
| Execution controls | Cancellation, concurrency limits, and failure reporting retain the required behavior. |
| Cloud and SDKs | Remote calls retain the full view and shared work. Schema and clients contain only intended changes. |

## Open decisions

- **Child keys:** Can a parent subset change child keys before child filters run? Proposed rule: keep retained item paths and child keys stable. This is not enforced today.
- **Stored data:** Proposed guidance: build commands and summaries from current keys when called. Stored commands can contain old arguments. Configuration and parent fields remain useful.
- **Shared references:** Define how filters on different paths combine when they refer to the same collection ID. Traversal order must not decide the subset.
- **Later objects:** Define how a new collection ID maps to the filtered dimension.
- **Removal filters:** Does `withoutUri` change collection state or only selection membership?
- **Conversion failures:** `asChecks()` can fail before returning its list. Choose how the CLI retains other results and supports `--fail-fast`, which cancels remaining work after a failure. The query alone does not preserve the current behavior.
- **API names and release:** Resolve author method conflicts. Agree on cloud delivery and compatibility for removed fields. Read [version gating](../../internal-docs/version-gating.md). All v1.0.0 prereleases share one schema view; a later beta gate cannot separate them.
