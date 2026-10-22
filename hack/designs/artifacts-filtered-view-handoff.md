# Artifacts filtering and removal of native batching

Implementation handoff. Design status: agreed direction, with the open decisions below. No implementation has started.

This document replaces the union proposal in the [earlier brief](https://gist.github.com/shykes/50328038e38c40996ac4c72bea0a0fe0). Defer `Artifacts.withArtifacts()`.

Code reviewed: `dagger/dagger`, commit `ce6dc9f9403b52067f5831149eab4871f857185d`. Verify the current code before editing. References below point to the reviewed commit.

## 1. Intended behavior

`Artifacts` describes a selection of workspace artifacts and the workspace view used to evaluate them. An artifact identifies a workspace field and the collection keys used to reach it.

Filtering has two effects:

1. Remove entries from the selection.
2. When filtering a collection dimension, change the collection values used to evaluate the selected entries.

Each filtered `Artifacts` value has its own view. The source value stays unchanged. The behavior applies to all artifact types, not only checks.

For a collection filter:

1. Keep requested keys present in each collection. A key absent from one collection can exist in another.
2. Use `subset` to create the reduced collection.
3. Make ordinary references to that source collection resolve to the reduced collection in the new view.

All references to the same source collection ID must see the same reduced collection. This includes references through other fields and references from children back to parents.

Preserve `CollectionBase`, the original collection used to compute `delta`. It is a history reference. Do not apply ordinary reference replacement to it.

For a static field filter, change selection membership only. Do not remove fields from object types. Module code can still call fields excluded from the selection. Thus, the view exposes collection membership; it does not expose the complete artifact selection.

## 2. Filter contract for the first version

Keep intersection and exclusion behavior where already defined. Defer union through `withArtifacts()`. Do not introduce another public union operation.

Repeated keys in one filter are alternatives. Successive filters narrow the selection:

| Input | Result |
|---|---|
| One filter with keys `[TestFoo, TestBar]` | Either matching test |
| Filter `[TestFoo, TestBar]`, then `[TestBar]` | TestBar |
| Filter `[TestFoo]`, then `[TestBar]` | No matching tests |
| Requested key absent in one package | Skip that key in that package |
| Empty matching key set | Empty collection subset; no matching item artifacts |

Do not confuse repeated CLI flags with successive API filters. The CLI must collect repeated flags into one key list.

For example:

```sh
dagger check -l --go-test=TestFoo --go-test=TestBar
```

The core selection is equivalent to this API notation:

```text
currentWorkspace()
  .artifacts()
  .filterTypes(types: ["Check"])
  .filterDimensionKeys(
    dimension: "go-test",
    keys: ["TestFoo", "TestBar"]
  )
  .items()
```

This omits command settings and dimension-name binding. The listing must not run checks.

One view can include several paths with shared filters. Preserve that CLI behavior without building separate views and joining them.

Defer commands that combine independently filtered addresses. Reject them with a clear error. Do not silently apply all address filters globally: that would change which combinations match. Commands with one address and several key alternatives remain supported.

When two path dimensions refer to the same collection ID, their effects must agree with identity-wide replacement. Confirm how those constraints combine before implementing this case. Never let traversal order select the winning subset.

## 3. Remove native batching

Remove these concepts from the target design:

- Automatic replacement of item checks and generators with batch functions.
- The public `<CollectionType>.batch` field and synthetic batch types.
- Batch metadata used only for that mechanism.

Keep `subset`, keys, `get`, and `delta`. They have uses outside batching.

Preserve ordinary collection functions. Today the batch namespace contains functions unrelated to checks, including functions that return selected keys or modified collections. Expose those functions as ordinary collection methods. Identify name conflicts with standard collection operations. Escalate a conflict that requires a new public naming rule.

Do not move batch replacement into `asChecks()` or another conversion.

## 4. Module-controlled shared execution

Expose checks at the smallest useful assertion, such as one test. A common module helper chooses the execution group from the filtered parent view.

```text
TestFoo.check -> filteredModule.testResults() -> shared command
TestBar.check -> filteredModule.testResults() -> shared command
```

The helper produces structured results. Each check reads its own result and returns its own failure.

A normal test failure must not make the common helper fail before producing results. Capture expected runner failure codes, then inspect the report. Handle infrastructure failures, missing reports, and interrupted runs explicitly.

Normal call caching can share calls with matching identities and inputs. It is not an exactly-once guarantee. Do not put the current leaf address into the common helper's identity. Different leaf checks must be able to reach the same helper call.

Keep individual check identities and outcomes. A shared execution span cannot have several parents. Do not promise that simple span reparenting will attach all shared output to every check. Define the required result and trace behavior separately.

For generators, each selected artifact must produce its intended output. Returning the same full Changeset from every leaf is not sufficient.

## 5. Public API boundaries

Remove `Artifacts.values()` from the target public API. It was not approved as the execution interface.

Keep the approved `asXXX()` conversions: `asChecks`, `asGenerators`, `asServices`, `asChangesets`, and `asExpertise`.

Their current raw-list return types remain open for later review. Keep the existing shapes for this implementation unless a change is approved. Shared execution alone does not justify a new wrapper type.

Keep deferred behavior where it exists. Converting to checks must not run them. The CLI and module callers must use the same filtered view. `asChangesets()` can evaluate source fields; do not claim that all conversions defer all work.

Do not add `ArtifactSelection`, typed selection wrappers, new public execution methods, or other schema changes without approval. The agreed removals and exposure of existing collection methods still need a compatibility plan.

## 6. Internal implementation requirements

The proposed new `Query` is an evaluation-root model, not a sufficient implementation by itself. `Query` does not contain all workspace module objects.

Implement view state that survives:

- Collection discovery and calls to author `get` functions.
- Stored object fields, nested references, receivers, and object arguments.
- Calls from module runtimes back into the engine.
- Loading an object by ID.
- Cached results and deferred Check or Generator execution.
- Collections constructed by later calls.
- Persistence and remote execution.

Do not mutate an existing result behind its original ID. Create view-specific results or references. Keep the source view available and unchanged.

Include the effective collection view in call identity before cache lookup. Keep that identity separate from the current leaf target and static selection filters. Equivalent helper calls in one view must still share work.

Define how a collection created later maps to its filtered dimension. Exact replacement of IDs found during initial discovery is insufficient: reevaluation can create a new ID.

Stored scalar values, arbitrary JSON, and prebuilt commands cannot be repaired by replacing typed object references. State this limit in author guidance.

## 7. Proposed author contract

These rules need confirmation before they become a published module contract:

- Retained item paths and descendant keys remain stable under subset. Execution may adapt to the selected items.
- Compute selection-dependent commands and aggregate data in functions. Do not store them before filtering.
- Store source directories, configuration, and parent references as needed. Do not ban all custom collection fields.

Example of unstable discovery: an API service adds a fallback test when the database service is absent. Filtering out the database would then add an artifact. The stable-membership rule would prohibit that behavior. This is a hypothetical permitted author behavior, not an observed production defect.

Example of stale stored data: a Container command already contains every original package name. Changing collection references cannot update its arguments. Build that command from the current collection when the helper is called.

The engine cannot enforce these author rules in full. Do not silently change discovery semantics to hide violations.

## 8. Cloud execution

Current remote evaluation sends one leaf address and opens a separate session for each check. That does not preserve the full view or provide sharing of active calls.

The new path must carry both:

1. The complete filtered collection view.
2. The individual artifact target.

Related checks also need a common execution scope or scheduling behavior that preserves shared work. Reusing the same engine alone is insufficient when sessions remain separate.

Do not remove native batching while claiming equivalent cloud efficiency until this behavior is demonstrated. If cloud work must ship later, agree an explicit release boundary. Do not silently run a complete suite once per leaf check.

## 9. Code map

All links use the reviewed commit.

| Area | Starting point |
|---|---|
| Selector composition and artifact set operations | [core/artifacts.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/artifacts.go#L507) |
| Collection discovery | [core/artifact_collection.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/artifact_collection.go#L367) |
| Subset, delta, and collection base | [core/collection_object.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/collection_object.go#L111) |
| Stored-reference traversal | [core/object.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/object.go#L522) |
| Pre-cache implicit inputs | [dagql/objects.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/dagql/objects.go#L622) |
| Conversions and current universal evaluator | [core/schema/artifacts.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/schema/artifacts.go#L891) |
| Native batch planner | [core/artifact_batch.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/artifact_batch.go) |
| Synthetic batch types | [core/collection_typedef.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/collection_typedef.go#L22) |
| Batch type metadata | [core/schema/collection.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/schema/collection.go#L33) |
| CLI address combination and execution | [internal/cmd/dagger/artifact_execution.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/internal/cmd/dagger/artifact_execution.go#L113) |
| CLI list selection combination | [internal/cmd/dagger/artifacts.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/internal/cmd/dagger/artifacts.go#L285) |
| Remote artifact evaluation | [core/artifact_cloud.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/core/artifact_cloud.go#L29) |
| Go collection client generation | [cmd/codegen/generator/go/templates/introspect_collection.go](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/cmd/codegen/generator/go/templates/introspect_collection.go#L54) |

Also inspect SDK authoring and generated output for all affected languages. Do not limit the change to Go clients.

## 10. Implementation sequence

1. Confirm the open rules below and write down the compatibility boundary.
2. Build one nested collection fixture. Give child objects parent references and a common results helper.
3. Implement view identity and reference handling. Prove local behavior through ordinary artifact values and deferred checks.
4. Make conversions and CLI consumers use that view. Migrate callers of `values()` without adding a universal public replacement.
5. Preserve supported CLI filters. Remove internal union dependencies for shared-filter paths. Reject unsupported independent selections.
6. Prove remote view transport and shared execution.
7. Remove batch replacement, batch schema, and batch-specific code generation. Preserve ordinary collection functions.
8. Update affected SDKs, tests, and documentation. Verify compatibility views.

Treat this as an implementation order, not permission to ship intermediate behavior with missing guarantees.

## 11. Required validation

Before choosing or running repository test commands, read `skills/engine-debugging/SKILL.md`.

Use meaningful behavior tests:

| Case | Required result |
|---|---|
| Parent and child filters | Child and parent navigation see all matching subsets |
| Non-check artifact | Ordinary values use the same filtered view |
| Repeated CLI keys | One OR filter; both matching tests remain |
| Chained filters | Intersection; source selection unchanged |
| Missing and empty keys | Skip missing per-collection keys; preserve valid empty subsets |
| Aliases | The same source collection has one subset; traversal order has no effect |
| Base and delta | Delta reports removals against the original collection |
| Deferred calls, ID loads, and later construction | View survives each boundary |
| Cache isolation | Different views do not return each other's results |
| Shared helper | Concurrent checks in one view share expensive work under normal cache conditions |
| Mixed test outcomes | One failed test does not make every check fail |
| Listing | Does not run leaf checks or the test command |
| Supported CLI selection | Multiple shared-filter paths work without `withArtifacts()` |
| Unsupported CLI selection | Independently filtered addresses fail clearly |
| Cloud checks | Full view arrives; shared work and separate outcomes are demonstrated |
| Public schema and SDKs | No unintended additions; ordinary collection methods remain usable |

Keep useful existing subset and delta tests. Replace batch-specific expectations with shared-execution checks. Do not delete tests merely because they expose a missing guarantee.

## 12. Decisions to resolve before release

1. Confirm the stable-membership author rule and behavior when it is violated.
2. Define intersection of path constraints that refer to the same collection ID.
3. Define how later collection values retain their filter origin.
4. Check removal APIs, including `withoutUri`, against the new view contract. Deferring union does not settle subtraction semantics.
5. Select the cloud delivery boundary and scheduling approach.
6. Select the compatibility boundary for `values()`, `withArtifacts()`, and `.batch`.

Read [schema version gating](https://github.com/dagger/dagger/blob/ce6dc9f9403b52067f5831149eab4871f857185d/internal-docs/version-gating.md) before changing public fields. All v1.0.0 prereleases share a schema view. A later beta gate cannot isolate earlier beta clients. Do not assume that unreleased design changes permit breaking existing clients without a release decision.

A change to the `asXXX()` return types remains a separate design decision. Keep the current returns for this work unless directed otherwise.
