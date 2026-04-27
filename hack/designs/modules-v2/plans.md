# Execution Plans

Depends on: [Artifacts](./artifacts.md)

## Table of Contents

- [Summary](#summary)
- [Schema](#schema)
- [Design Decisions](#design-decisions)
- [Actions](#actions)
- [Plan Construction](#plan-construction)
- [Check And Generate Construction](#check-and-generate-construction)
- [Plan Execution](#plan-execution)
- [Locked Decisions](#locked-decisions)

## Summary

Execution Plans introduces a generic `Action`/`Plan` substrate for verb
orchestration. A Plan is a DAG of Actions with "after" edges. Each Action
implements one verb (defined in [artifacts.md](./artifacts.md)).

This unit introduces one public choke point for plan compilation:

- `Artifacts.plan(verb, include, exclude)`

Artifact filtering happens first on `Artifacts`. Exact entrypoint selection,
dependency closure, batching, and deduplication all happen inside `plan(...)`.
`dagger check` and `dagger generate` lower to that same API. Later docs such
as [Ship](./ship.md) extend the same substrate with additional
verb-specific rules.

Replaces CheckGroup. Transition path: CheckGroup → Execution Plans.

This document builds on the artifact model from [artifacts.md](./artifacts.md):

- top-level module objects are artifacts
- collection items are artifacts
- ordinary nested objects are structural glue

Action discovery walks that structural glue and produces **artifact-relative**
function paths such as `["lint"]`, `["tests", "run-bun"]`, or
`["tests", "generate-fixtures"]`.

## Schema

```graphql
# `Verb` is defined in artifacts.md.

"""
Engine-owned function selector syntax used to match plan entrypoints.
Examples: `lint`, `tests:run-bun`, `go:*`, `foo:**:lint`.
"""
scalar FunctionPattern

extend type Artifacts {
  """
  Compile a plan for the selected artifacts and one verb.

  `include` and `exclude` match entrypoints only. Dependencies are added
  automatically and are not filtered directly.
  """
  plan(
    verb: Verb!
    include: [FunctionPattern!]! = []
    exclude: [FunctionPattern!]! = []
  ): Plan!
}

extend type Artifact {
  """
  Reachable local action occurrences on this artifact.
  If `verbs` is provided, only include actions of those verbs.
  """
  actions(verbs: [Verb!]): [Action!]!

  """Create one exact action targeting this single artifact."""
  action(verb: Verb!, functionPath: [String!]!): Action!
}

"""
A callable action: one verb, one target scope, one exact function path,
and one compiled execution mode.
Actions are the building blocks of execution plans.
"""
type Action {
  """
  The verb this action implements.
  """
  verb: Verb!

  """
  Exact artifact scope targeted by this action.
  Preserves the source scope's dimension order and narrows only the selected
  artifacts.
  """
  target: Artifacts!

  """
  Exact artifact-relative function path.
  Each element is one path segment.
  Examples: `["lint"]`, `["tests", "run-bun"]`.
  """
  functionPath: [String!]!

  """
  True if this action invokes a collection batch function once for the selected
  subset represented by `target`, rather than invoking the same function once
  per selected artifact.

  If true, `target` must contain artifacts from exactly one collection
  occurrence.
  """
  collectionBatched: Boolean!

  """Actions that must complete before this one runs."""
  after: [ActionID!]!

  """Return a new Action with additional ordering dependencies."""
  withAfter(actions: [ActionID!]!): Action!

  """Execute this action according to its verb."""
  run: Void
}

"""
A compiled execution plan — a DAG of actions.
Each action is a function call on one or more artifacts.
Edges are "after" dependencies between actions.
Parallel execution is implicit: actions with no pending dependencies run
concurrently.

NOTE FOR IMPLEMENTERS: Each action is backed by a DAGQL call chain under the
hood. The Action/Artifact API is a clean projection over engine-internal DAGQL
structures. Use existing engine-internal call chain representations rather than
building parallel ones.
"""
type Plan {
  """The single verb implemented by every action in this plan."""
  verb: Verb!

  """All actions in this plan, including prerequisites."""
  nodes: [Action!]!

  """
  Evaluate this plan as an UP plan and return the resulting services in stable
  node order.
  Valid only when `verb = UP`.
  """
  services: [Service!]!

  """
  Evaluate this plan as an UP plan and return exactly one resulting service.

  Errors before execution unless the compiled plan structurally resolves to
  exactly one service-producing invocation.
  Valid only when `verb = UP`.
  """
  service: Service!

  """
  Evaluate this plan as a GENERATE plan and return the combined changeset.
  Valid only when `verb = GENERATE`.
  """
  changes(onConflict: ChangesetsMergeConflict = FAIL_EARLY): Changeset!

  """
  Execute the plan. Returns void on success, error on failure.
  Some verbs also expose typed result accessors (`changes`, `services`,
  `service`) when callers need the evaluated results without realizing the
  full verb effect.
  """
  run: Void
}
```

## Design Decisions

- **Plan = DAG of Actions.** Each action is
  `(verb, target, functionPath, collectionBatched)` with "after" edges.
  Parallel is implicit — actions with no pending dependencies run concurrently.
- **Verb is part of identity.** The same function path may be callable
  under different verbs, and those are different actions.
- **Artifacts select artifacts; plans select entrypoints.** Exact action
  selection does not happen on `Artifacts`. It happens inside
  `Artifacts.plan(verb, include, exclude)`.
- **Function paths are artifact-relative.** The action model is `(artifact,
  functionPath)`, not one global path grammar.
- **Function paths and function patterns are different.**
  `Action.functionPath` is exact and normalized as `[String!]!`.
  `FunctionPattern` is the selector syntax used by `Artifacts.plan(...)`.
- **The engine owns selector parsing and matching.** `FunctionPattern`
  uses the same path-style selector language the CLI already speaks,
  including globbing and doublestar matching.
- **One public choke point.** `Artifacts.plan(...)` is the only public
  plan-construction entrypoint in this unit.
- **Include/exclude only match entrypoints.** Dependencies are never matched
  or filtered directly. Selected entrypoints always pull in all transitive
  prerequisites.
- **Batching is compiled.** Batch-vs-item execution is resolved before plan
  nodes are created. Executors do not infer it.
- **Always compiled.** `dagger check` and `dagger generate` always compile a
  Plan, then execute it. `--plan` stops before execution and displays the plan.
- **Engine compiles, CLI displays.** The engine owns plan compilation
  (`Artifacts.plan(...) → Plan`). The CLI decides whether to call `run` or
  display the plan nodes.
- **Plans materialize all implicit config.** Workspace defaults, filter
  results, include/exclude matching, batch-vs-item decisions — all collapsed
  into concrete Actions.
- **Some verbs have typed evaluated results.** `GENERATE` evaluates to a
  combined `Changeset`. `UP` evaluates to one or more `Service`s. `Plan.run()`
  realizes the full verb effect from those results.
- **No mini-VM.** Plans are finite DAGs. No loops, conditionals, variables.
- **Run returns void.** `Plan.run` and `Action.run` return void on success,
  error on failure.

## Actions

An Action bridges artifacts and functions:

- `Artifact.action(CHECK, ["lint"])` → one action on one artifact
- `Artifact.action(CHECK, ["tests", "run-bun"])` → nested action on one artifact
- `Artifact.actions([CHECK])` → local reachable check actions on one artifact

Each action implements one verb. Verbs are defined in
[artifacts.md](./artifacts.md).

Actions are the building blocks of Plans. A Plan is a DAG of Actions with
"after" edges.

### Reachability And Naming

Action discovery starts at one artifact root.

It walks recursively through:

- object-valued fields
- zero-arg object-valued functions

It does not walk through:

- members that require arguments
- non-object fields
- cross-artifact references
- the next artifact boundary

Whenever that walk reaches a direct non-traversal function annotated with one
or more supported verbs, that function becomes a reachable action for each
applicable verb.

The function path is the exact segment path from the artifact root to that
function.

Example:

```dang
type Go {
  pub lint: Void! @check

  pub tests: Tests! {
    Tests()
  }
}

type Tests {
  pub runBun: Void! @check
}
```

On the `Go` artifact, the reachable check actions are:

```console
lint
tests:run-bun
```

In structured form:

```text
["lint"]
["tests", "run-bun"]
```

The artifact root name itself is **not** part of `functionPath`. So this is
the normal form:

```console
$ dagger check --type=go lint
```

not:

```console
$ dagger check --type=go go:lint
```

### Enumeration

`Artifact.actions(verbs)` returns all reachable local action occurrences
on that artifact.

These are **unbatched** occurrences:

- one `Action` row per exact `(verb, target, functionPath)` occurrence
- `target.items` has length 1
- `collectionBatched` is always `false`

Set-level behavior is expressed by compiling a `Plan` from an `Artifacts`
scope, then inspecting `Plan.nodes`.

### Examples

One artifact:

```text
workspace.artifacts
  .filterCoordinates("type", ["go"])
  .items[0]
  .actions([CHECK])
```

might produce:

```console
lint
tests:run-bun
tests:run-nodejs
```

Compiled listing:

```text
workspace.artifacts
  .filterCoordinates("type", ["go-test"])
  .plan(verb: CHECK, include: ["run"])
  .nodes
```

might produce:

```console
(CHECK, TestFoo, ["run"], false)
(CHECK, TestBar, ["run"], false)
```

### CLI Listing

`dagger check -l` and `dagger generate -l` list compiled plan nodes, not raw
action discovery.

If all listed actions belong to one artifact, the CLI prints plain
artifact-relative function paths:

```console
$ dagger check --type=go -l
lint
tests:run-bun
```

If several artifacts are in play, the CLI prints a table:

- one column per artifact dimension needed to distinguish the listed artifacts
- one `ACTION` column

Example with only `type` varying:

```console
$ dagger check -l
TYPE   ACTION
go     lint
go     tests:run-bun
js     lint
js     tests:run-bun
```

Example with collection artifacts:

```console
$ dagger check --type=go-test -l
GO TEST   ACTION
TestFoo   run
TestBar   run
```

Example with more than one varying artifact dimension:

```console
$ dagger check -l
GO MODULE   GO TEST   ACTION
./my-app    TestFoo   run
./my-lib    TestFoo   run
```

Do not print dimensions that are constant or entirely null across the listed
artifacts.

### Compatibility Input

For compatibility, the CLI may also accept:

```console
<type>:<function-selector>
```

as shorthand for:

```console
--type=<type> <function-selector>
```

Examples:

```console
$ dagger check go:lint
$ dagger check go-test:run
```

This is input sugar only. The primary listing format is the plain path or the
table format above.

The engine, not the CLI, owns selector parsing and matching. CLI positional
selectors lower directly into `Artifacts.plan(verb, include, exclude)` as
`FunctionPattern`s.

## Plan Construction

Plan compilation has six parts:

1. **Selection.** User-provided filters such as `--type=go` or
   `--type=go-test --go-test=TestFoo`
   become `filterDimension` / `filterCoordinates` chains on `Artifacts`.
2. **Plan request.** The caller asks for `scope.plan(verb, include, exclude)`.
3. **Action discovery.** The engine reads `Artifact.actions([verb])` from the
   selected artifacts and discovers the candidate entrypoints for that verb.
4. **Entrypoint matching.** `include` and `exclude` are matched against those
   candidate entrypoints. Empty `include` means "all entrypoints for this verb."
   `exclude` is applied after `include`.
5. **Action compilation and batching.** The engine turns retained entrypoints
   into concrete `Action`s. Dependencies are added automatically. Rollup
   through structural glue and batch-vs-item decisions are resolved here.
6. **Deduplication and ordering.** Duplicate compiled actions are collapsed by exact
   identity: `(verb, target, functionPath, collectionBatched)`.
   `target` equality here means the same dimension order and the same row set,
   not pointer identity.
   Ordering may come from explicit user composition (`withAfter`) or from the
   construction rules of the compiled verb.

This document defines automatic construction rules only for `check` and
`generate`. Later docs add additional verb-specific construction rules for
`UP` and `SHIP`. This document still locks the shared `Plan.services()` /
`Plan.service()` surface for `UP`, because that affects the common `Plan` API.

## Check And Generate Construction

### `check`

The most recursive verb in this unit.

`scope.plan(verb: CHECK, include, exclude)` compiles from the current
`Artifacts` scope. The compiler:

1. reads the selected artifacts with `.items`
2. discovers local check occurrences with `artifact.actions([CHECK])`
3. applies `include` and `exclude` to the discovered check entrypoints
4. groups retained occurrences by exact `functionPath`
5. resolves collection batching for each grouped path
6. recursively compiles referenced checks
7. adds `after` edges from referenced checks to the selected check entrypoints
8. deduplicates exact compiled actions

`include` and `exclude` apply only to entrypoints. Referenced checks are always
retained when they are prerequisites of a selected check entrypoint.

If the selected artifacts for a candidate `functionPath` belong to one
collection occurrence and that collection exposes the same check handler on its
batch type, compile one `collectionBatched = true` action.

Otherwise, compile item-level actions with `collectionBatched = false`.

This makes aggregate artifacts useful by default.

Example:

```text
workspace.artifacts
  .filterDimension("go-test")
  .plan(verb: CHECK)
  .run
```

If the selected `go-test` artifacts are `TestFoo` and `TestBar`:

- with `GoTests.batch.run`, the plan may contain one compiled action with
  `functionPath = ["run"]` and `collectionBatched = true`
- without `GoTests.batch.run`, the plan contains one compiled action per test
  with `functionPath = ["run"]` and `collectionBatched = false`

### `generate`

Conservative — no recursive expansion.

- Include local generate handlers on artifact A.
- Do not recursively generate through references by default.
- Do not make `generate` an implicit prerequisite of other verbs.
- `Plan.changes()` merges the selected generate results into one `Changeset`.

This avoids surprising workspace mutations.

## Plan Execution

Once a Plan is constructed, execution has two layers:

1. **Evaluate actions.** Execute the selected functions and obtain their
   typed results.
2. **Realize the verb effect.** Turn those results into the user-visible
   effect of the plan.

For `CHECK`, these mostly collapse together.

For `GENERATE`, evaluation yields one `Changeset` per selected generate action.
`Plan.changes()` merges those results into one combined `Changeset`. `Plan.run()`
performs the full generate behavior.

For `UP`, evaluation yields one `Service` per selected up action. `Plan.services()`
and `Plan.service()` expose those evaluated results directly. `Plan.run()`
performs the full long-running `UP` behavior from them.

`Plan.service()` is a preflight singleton check. It errors before executing
anything expensive unless the compiled plan structurally resolves to exactly
one service-producing invocation. A collection-batched action counts as one;
an item-level action counts once per targeted artifact.

Within a plan, actions with no pending "after" dependencies run concurrently.
`--plan` stops after compilation and displays the DAG without executing it.

## Locked Decisions

- **`Action.withAfter` is part of the public API.** The engine uses it
  internally during plan compilation, and users can use it to build custom
  plans.
- **Action and Plan stay separate.** `Action` is one compiled execution unit;
  `Plan` is a DAG of `Action`s.
- **Artifact-local discovery only.** Reachable action discovery is exposed on
  `Artifact.actions(...)`, not on `Artifacts`.
- **`Artifacts.plan(...)` is the public choke point.** Exact entrypoint
  selection does not live on `Artifacts` as a separate filter API.
- **Function paths are artifact-relative and exact.** There is no separate
  canonical workspace-global action path in this design.
- **`FunctionPattern` is engine-owned selector syntax.** It is distinct from
  exact compiled `functionPath` arrays.
- **Action identity is exact and compiled.** One `Action` is identified by
  `(verb, target, functionPath, collectionBatched)`.
- **Entrypoint selectors only select entrypoints.** Dependencies are pulled
  in automatically and are never filtered directly.
- **Plan edges are semantically static.** DagQL IDs are only the reference
  mechanism for already-compiled edges; they are not late-bound selectors.
- **`Plan.changes()` and `Plan.services()` expose evaluated verb results.**
  `GENERATE` yields a combined `Changeset`; `UP` yields one or more `Service`s.
- **`dagger check -l` is table-capable.** It prints plain paths for one
  artifact and a minimal distinguishing table for several artifacts.
