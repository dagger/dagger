# Implement filtered artifact views

## Problem

Batching repeats work across levels. A Go test can run three times: individually, in a package batch, and in a module batch.

Artifact filters select entries but leave parent collections unchanged. Module code cannot tell which items were selected.

## Solution

Give each selection one filtered workspace view. Replace collection references with filtered copies. Expose individual checks. Let module code share their work through normal calls. Keep individual check results. Remove native batching and `Artifacts.values()`. Defer `withArtifacts()`.

**Status:** Design handoff. Not implemented.

Code reviewed: `ce6dc9f9403b52067f5831149eab4871f857185d`. Check the current code before editing.

## Contents

- [Problem](#problem)
- [Solution](#solution)
- [Schema changes](#schema-changes)
- [Filter behavior](#filter-behavior)
- [CLI behavior](#cli-behavior)
- [Shared execution](#shared-execution)
- [Implementation](#implementation)
- [Required checks](#required-checks)
- [Open decisions](#open-decisions)

## Schema changes

- **Filters:** Keep the existing filter methods. Evaluate selected artifacts against the filtered workspace view.
- **Conversions:** Keep `asXXX()` methods. They convert selected artifacts to objects of a specific type, such as `Check`.
- **Return types:** Keep lists such as `[Check]` for this work. Decide later whether to replace these lists.

Docstrings describe behavior. Comments mark changes. All shown signatures stay unchanged. Other fields are omitted.

```graphql
# CHANGED: selected values use the filtered workspace view.
"""A selection of artifacts and the workspace view used to evaluate them."""
type Artifacts {
  # UNCHANGED.
  """Keep the listed types. If exclude is true, remove them instead."""
  filterTypes(types: [String!]!, exclude: Boolean! = false): Artifacts!

  # CHANGED: also reduce collections and replace references in the new view.
  """
  Keep artifacts with any listed key in this dimension.
  For collections, use matching subsets in the new view. Skip absent keys.
  Keep the source view unchanged.
  """
  filterDimensionKeys(dimension: String!, keys: [String!]!): Artifacts!

  # CHANGED: each returned artifact retains this view for later evaluation.
  """List selected artifacts without evaluating their values."""
  items: [Artifact!]!

  # CHANGED: all conversions use this view.
  """Return selected checks without running them. Fail on other types."""
  asChecks: [Check!]!

  """
  Return selected generators without running them. Fail on other types.
  Skip module load failures, because generation can repair them.
  """
  asGenerators: [Generator!]!

  """Return selected services without starting them. Fail on other types."""
  asServices: [Service!]!

  """Evaluate selected Changeset fields and return their values. Fail on other types."""
  asChangesets: [Changeset!]!

  """Return selected expertise functions without running them. Fail on invalid signatures."""
  asExpertise: [Expertise!]!

  # REMOVED: values. No general execution method replaces it.
  # DEFERRED: withArtifacts. Do not merge separate views in this version.
}
```

Remove the batch fields and types too:

| Field or type | Change |
|---|---|
| `<CollectionType>.batch` | Remove the batch namespace. |
| `<CollectionType>_Batch` | Remove the generated batch type. |
| `CollectionTypeDef.batchType` | Remove batch type metadata. |

Remove `ArtifactResult` if no remaining approved API uses it.

Keep ordinary author functions. Move them from the batch type to the collection type. For example, suppose a module has a `selected` function:

```graphql
# BEFORE: only fields affected by the move are shown.
type GoTests {
  # REMOVED: access through the batch namespace.
  """Return collection-level functions."""
  batch: GoTests_Batch!
}

# REMOVED TYPE: move its author functions onto GoTests.
type GoTests_Batch {
  # MOVED: this function remains available as GoTests.selected.
  """Return the selected test keys."""
  selected: [String!]!
}
```

```graphql
# AFTER: author functions are on the collection type.
"""A collection of tests."""
type GoTests {
  # UNCHANGED: standard collection fields and methods.
  """Return current keys in author order."""
  keys: [String!]!

  """Return current test items in key order."""
  list: [GoTest!]!

  """Return the test for this key. Fail if the key is absent."""
  get(key: String!): GoTest!

  """
  Return a collection with only the requested keys, in author order.
  Keep the source unchanged. Fail on absent or duplicate keys.
  """
  subset(keys: [String!]!): GoTests!

  # ADDED HERE: moved from GoTests_Batch; not a new author function.
  """Return the selected test keys."""
  selected: [String!]!

  # REMOVED: batch field. GoTests_Batch no longer exists.
}
```

These are example module types, not new engine types.

Remove check or generator markers from functions that only group item work. Make them helpers, or remove them. Keeping the markers would repeat work through both group and item checks.

Keep `delta` support. Confirm conflicts between author method names and standard collection methods. New public types or methods require approval.

## Filter behavior

A workspace view is the object state used to evaluate selected artifacts.

Each filtered `Artifacts` value has its own view. The source value stays unchanged. Apply this rule to all artifact types.

For a collection filter:

1. Keep requested keys that exist in each collection.
2. Call `subset` to create the reduced collection.
3. Replace ordinary references to the source collection with references to the reduced collection in the new view.

References through other fields and from children to parents must see the same subset.

Preserve `CollectionBase`, the original collection used to compute `delta`. Do not replace this history reference.

Static field filters only narrow the selection. They retain the current collection view. Excluded fields remain callable. Collection state therefore does not expose the complete artifact selection.

Keys in one filter are alternatives. Successive filters intersect:

| Filters | Result |
|---|---|
| One filter: `[TestFoo, TestBar]` | Either matching test |
| `[TestFoo, TestBar]`, then `[TestBar]` | TestBar |
| `[TestFoo]`, then `[TestBar]` | No tests |

Skip requested keys absent from a particular collection. Keep valid empty subsets.

## CLI behavior

Preserve repeated key flags:

```sh
dagger check -l --go-test=TestFoo --go-test=TestBar
```

Build one filter with both keys. Do not build two successive filters or join two selections.

The equivalent core query is shown below. Command settings are omitted.

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

`items` returns artifact descriptions. Here it lists their addresses without running checks.

Without `-l`, replace `items { uri }` with:

```graphql
asChecks {
  sync {
    pass
    error { message }
  }
}
```

`sync` runs each check and returns its retained result. Read `pass` and `error` from that result. This avoids separate execution requests for each result field. The CLI fails if any check fails or the query returns an error.

Command scheduling and conversion failures remain open decisions.

Keep multiple paths with shared filters in one selection. Reject separately filtered addresses that require union. Do not apply their filters globally; that can select different combinations.

## Shared execution

Remove automatic replacement of individual checks and generators with batch functions.

Expose individual checks, such as one check per test. A common module helper runs the selected tests and produces structured results:

```text
TestFoo.check -> module.testResults() -> shared command
TestBar.check -> module.testResults() -> shared command
```

Each check reads its own result. Expected test failures must still produce readable results. Handle missing reports and execution failures separately.

Caching can share matching calls. Preserve check addresses for reporting, but keep them out of the helper's cache key. Caching does not guarantee exactly one execution.

A shared trace span cannot have several parents. Define its display separately. Each generator must return its intended output; do not return the full shared Changeset from every item.

## Implementation

1. Confirm the open decisions below.
2. Build a nested collection example with child-to-parent references and a common results helper.
3. Implement view state and reference replacement. Copying `Query` is insufficient; it does not contain all module objects.
4. Preserve the view through module calls, stored ID loads, cached results, deferred execution, and objects created later.
5. Include the collection view in the cache key before lookup. Preserve source objects and IDs. Keep different views separate and allow equivalent views to share work.
6. Migrate every caller of `values()`, including check, generate, start, call, and cloud execution. Keep command settings, argument passing, cancellation, concurrency limits, failure reporting, and trace names. Do not replace the method with a renamed general evaluator.
7. Make CLI callers and `asXXX()` use the same view.
8. Fix cloud execution. Send the full view separately from the artifact target. Related checks need a shared execution scope. Current remote checks use separate sessions.
9. Remove batch code and schema. Migrate group functions and their markers. Update all affected SDKs, generated clients, and documentation.

Start here:

| Area | Files |
|---|---|
| Filters and discovery | [artifacts.go](../../core/artifacts.go), [artifact_collection.go](../../core/artifact_collection.go) |
| Subset, base, and delta | [collection_object.go](../../core/collection_object.go) |
| Object references and cache inputs | [object.go](../../core/object.go), [objects.go](../../dagql/objects.go) |
| Conversions and execution | [schema/artifacts.go](../../core/schema/artifacts.go) |
| Batch removal | [artifact_batch.go](../../core/artifact_batch.go), [collection_typedef.go](../../core/collection_typedef.go), [schema/collection.go](../../core/schema/collection.go) |
| CLI | [artifact_execution.go](../../internal/cmd/dagger/artifact_execution.go), [artifacts.go](../../internal/cmd/dagger/artifacts.go) |
| Cloud | [artifact_cloud.go](../../core/artifact_cloud.go) |

## Required checks

Read [engine-debugging](../../skills/engine-debugging/SKILL.md) before running tests.

Verify these behaviors:

- All artifact types see the filtered parent state.
- Repeated keys use OR; successive filters intersect.
- Source views stay unchanged. Empty subsets work.
- Shared references see one subset. Delta retains the original base.
- Deferred calls, ID loads, and later objects retain the view.
- Different views do not share incorrect cached results.
- Individual checks share helper work and report separate outcomes.
- Old group functions do not remain marked and repeat item work.
- Reading pass and error from a retained result does not rerun the check.
- Listing does not execute checks.
- Supported CLI calls still work. Unsupported unions fail clearly.
- Cancellation, concurrency limits, and failure reporting retain their required behavior.
- Cloud execution preserves the full view and shared work.
- Schema and SDK output contain only the intended changes.

## Open decisions

Resolve these rules before implementing the affected behavior:

- **Artifact membership:** Can filtering change child keys below retained items? Proposed rule: keep those paths and keys stable. This is not enforced today.
- **Stored data:** Proposed guidance: build commands and summaries from current keys when called. A stored Container can retain old test arguments. Keep configuration and parent fields.
- **Shared references:** Define how filters on different paths combine when they refer to the same collection ID. Traversal order must not decide the subset.
- **Later objects:** Define how a newly created collection maps to the filtered dimension, even if it has a new ID.
- **Removal filters:** Define whether `withoutUri` changes collection state or only selected entries.
- **Execution policy:** Define cancellation and concurrency limits. `asChecks()` can fail before returning its list; `values()` can retain other artifact results. Define how commands continue after a conversion failure. The query alone does not preserve this behavior.
- **Release:** Agree on cloud scope and compatibility for removed fields. Read [version gating](../../internal-docs/version-gating.md). All v1.0.0 prereleases share one schema view; a later beta gate cannot separate them.
