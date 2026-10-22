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

Keep the existing filters and `asXXX()` conversions. Change how filtered values are evaluated. Keep the current list return types for this work. Their later review is a separate decision.

Relevant target fields are shown below. Other existing fields are omitted.

```graphql
type Artifacts {
  filterTypes(types: [String!]!, exclude: Boolean! = false): Artifacts!
  filterDimensionKeys(dimension: String!, keys: [String!]!): Artifacts!
  items: [Artifact!]!

  asChecks: [Check!]!
  asGenerators: [Generator!]!
  asServices: [Service!]!
  asChangesets: [Changeset!]!
  asExpertise: [Expertise!]!
}
```

Remove these fields and types from the target schema:

| Field or type | Change |
|---|---|
| `Artifacts.values` | Remove the general execution method. Migrate its callers. |
| `Artifacts.withArtifacts` | Defer union of separate selections. |
| `<CollectionType>.batch` | Remove the batch namespace. |
| `<CollectionType>_Batch` | Remove the generated batch type. |
| `CollectionTypeDef.batchType` | Remove batch type metadata. |

Remove `ArtifactResult` if no remaining approved API uses it.

Keep ordinary author functions. Move them from the batch type to the collection type. For example, suppose a module has a `selected` function:

```graphql
# Before: the engine puts the author function under batch.
type GoTests {
  keys: [String!]!
  list: [GoTest!]!
  get(key: String!): GoTest!
  subset(keys: [String!]!): GoTests!
  batch: GoTests_Batch!
}

type GoTests_Batch {
  selected: [String!]!
}
```

```graphql
# After: the function is an ordinary collection method.
type GoTests {
  keys: [String!]!
  list: [GoTest!]!
  get(key: String!): GoTest!
  subset(keys: [String!]!): GoTests!
  selected: [String!]!
}
```

The collection names and author function above are examples. Do not add new engine types or methods for them.

Keep `delta` support. Do not add a public selection type or another general execution method without approval. Confirm any conflict between author method names and standard collection methods.

## Filter behavior

A workspace view is the object state used to evaluate selected artifacts.

Each filtered `Artifacts` value has its own view. The source value stays unchanged. Apply this rule to all artifact types.

For a collection filter:

1. Keep requested keys that exist in each collection.
2. Call `subset` to create the reduced collection.
3. Replace ordinary references to the source collection with references to the reduced collection in the new view.

References through other fields and from children to parents must see the same subset.

Preserve `CollectionBase`, the original collection used to compute `delta`. Do not replace this history reference.

Static field filters remove selected entries only. They do not remove callable fields or change object values. Thus, collection state does not expose the complete artifact selection.

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

Listing must not run checks.

Keep multiple paths with shared filters. Use one selection. Reject commands that require union of separately filtered addresses. Give a clear error. Do not apply their filters globally, because that can select different combinations.

## Shared execution

Remove automatic replacement of individual checks and generators with batch functions.

Expose individual checks, such as one check per test. A common module helper runs the selected tests and produces structured results:

```text
TestFoo.check -> module.testResults() -> shared command
TestBar.check -> module.testResults() -> shared command
```

Each check reads its own result. Expected test failures must still produce readable results. Handle missing reports and execution failures separately.

Normal call caching can share matching calls. Do not include the current check address in the common helper's identity. Caching does not guarantee exactly one execution.

Keep individual results when work is shared. A shared trace span cannot have several parents. Define its display separately. Each generator must also return its intended output; do not return the full shared Changeset from every item.

## Implementation

1. Confirm the open decisions below.
2. Build a nested collection example with child-to-parent references and a common results helper.
3. Implement view state and reference replacement. Copying `Query` alone is insufficient: it does not contain all module objects.
4. Preserve the view through module calls, stored ID loads, cached results, deferred execution, and objects created later.
5. Include the collection view in cache identity before lookup. Preserve source objects and IDs. Keep different views separate.
6. Make CLI callers and `asXXX()` use the same view. Converting to checks must not run them. `asChangesets()` can evaluate source fields.
7. Fix cloud execution. Send the full view separately from the artifact target. Related checks need a shared execution scope. Current remote checks use separate sessions.
8. Remove batch code and schema. Preserve ordinary collection methods. Update all affected SDKs, generated clients, and documentation.

Do not claim equivalent cloud performance until sharing is demonstrated.

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
- Listing does not execute checks.
- Supported CLI calls still work. Unsupported unions fail clearly.
- Cloud execution preserves the full view and shared work.
- Schema and SDK output contain only the intended changes.

## Open decisions

- **Author rules:** Require stable artifact paths and child keys under subset? Recommend computing selection-dependent commands and summaries in functions. A stored Container can contain obsolete test arguments. Keep configuration and parent fields.
- **Shared references:** Define how filters on different paths combine when they refer to the same collection ID. Traversal order must not decide the subset.
- **Later objects:** Define how newly created collections receive the correct filter.
- **Removal filters:** Check `withoutUri` and other removal operations against the new view rules.
- **Release:** Agree on cloud scope and compatibility for removed fields. Read [version gating](../../internal-docs/version-gating.md). All v1.0.0 prereleases share one schema view; a later beta gate cannot separate them.
