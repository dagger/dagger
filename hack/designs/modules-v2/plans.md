# Execution Plans

Depends on: [Artifacts](./artifacts.md)

Execution Plans turns selected artifacts into an inspectable DAG of actions
through one choke point: `Artifacts.plan(verb, include, exclude)`. It rolls out
`dagger check` and `dagger generate`, and replaces `CheckGroup`.

## Table of Contents

- [Summary](#summary)
- [Schema](#schema)
- [Actions](#actions)
- [Plan Construction](#plan-construction)
- [Check and Generate](#check-and-generate)
- [Plan Execution](#plan-execution)
- [Decisions](#decisions)

## Summary

An **Action** is one callable unit: one verb, one target scope, one exact
function path, one execution mode. A **Plan** is a DAG of Actions joined by
"after" edges; actions with no pending dependency run concurrently. Plans are
finite — no loops, conditionals, or variables.

Filtering happens first on `Artifacts` (see [artifacts.md](./artifacts.md)).
Everything after — exact entrypoint selection, dependency closure, batching, and
deduplication — happens inside `Artifacts.plan(...)`. `dagger check` and
`dagger generate` lower to that API; [Ship](./ship.md) later extends the same
substrate.

Action discovery walks the structural glue between artifacts (see
[artifacts.md § Model](./artifacts.md#model)) and produces **artifact-relative**
function paths such as `["lint"]` or `["tests", "run"]`.

> **Implementers:** each Action is backed by an engine-internal DAGQL call
> chain. The Action/Plan API is a clean projection over those existing
> structures — reuse them rather than building parallel ones.

## Schema

```graphql
# `Verb` is defined in artifacts.md.
# `ActionID`, `Changeset`, and `ChangesetsMergeConflict` are existing
# engine/DagQL types referenced here, not introduced by this document.

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
  """The verb this action implements."""
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

## Actions

An Action bridges an artifact and a function:

- `Artifact.action(CHECK, ["lint"])` → one action on one artifact
- `Artifact.action(CHECK, ["tests", "run"])` → nested action on one artifact
- `Artifact.actions([CHECK])` → local reachable check actions on one artifact

Verb is part of Action identity: the same function path callable under two verbs
is two actions. Function paths are **artifact-relative** — the model is
`(artifact, functionPath)`, not one global grammar. `functionPath` is exact and
normalized (`[String!]!`); `FunctionPattern` is the separate, engine-owned
selector syntax (globbing, doublestar) used by `plan(...)`.

### Discovery and naming

Discovery starts at one artifact root and walks recursively through object-valued
fields and zero-arg object-valued functions. It does not walk through
argument-requiring members, non-object fields, cross-artifact references, or the
next artifact boundary — the same rules as verb reachability in
[artifacts.md](./artifacts.md#reachability). Each verb-annotated function it
reaches is a reachable action; its `functionPath` is the segment path from the
root.

For the canonical `Go` example (see
[artifacts.md § Canonical Example](./artifacts.md#canonical-example)), extended
with a nested `Tests` glue object:

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

the reachable check actions on `Go` are `["lint"]` and `["tests", "run-bun"]`.
The artifact root name is not part of the path, so the normal form is
`dagger check --type=go lint`, not `go:lint`.

### Enumeration

`Artifact.actions(verbs)` returns unbatched local occurrences: one `Action` per
exact `(verb, target, functionPath)`, `target.items` of length 1, and
`collectionBatched` always `false`. Set-level behavior comes from compiling a
`Plan` and inspecting `Plan.nodes`:

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

### CLI listing

`dagger check -l` and `dagger generate -l` list compiled plan nodes, not raw
discovery. For one artifact, plain paths:

```console
$ dagger check --type=go -l
lint
tests:run-bun
```

For several artifacts, a table with one column per dimension needed to
distinguish them, plus an `ACTION` column. Constant or all-null dimensions are
omitted.

```console
$ dagger check -l
TYPE   ACTION
go     lint
go     tests:run-bun
js     lint
js     tests:run-bun

$ dagger check --type=go-test -l
GO TEST   ACTION
TestFoo   run
TestBar   run
```

For compatibility, the CLI may accept `<type>:<function-selector>` as shorthand
for `--type=<type> <function-selector>` (`dagger check go:lint`). This is input
sugar; positional selectors lower into `plan(...)` as `FunctionPattern`s, and
the engine — not the CLI — owns selector parsing and matching.

## Plan Construction

`plan(verb, include, exclude)` compiles in six steps:

1. **Selection.** User filters (`--type=go`, `--go-test=TestFoo`) become
   `filterDimension`/`filterCoordinates` chains on `Artifacts`.
2. **Plan request.** The caller asks for `scope.plan(verb, include, exclude)`.
3. **Discovery.** The engine reads `Artifact.actions([verb])` from the selected
   artifacts.
4. **Entrypoint matching.** `include` then `exclude` are matched against those
   candidates. Empty `include` means all entrypoints for the verb. Both match
   **entrypoints only** — dependencies are never matched or filtered directly.
5. **Compilation and batching.** Retained entrypoints become concrete `Action`s.
   Dependencies are added automatically; rollup through glue and batch-vs-item
   decisions are resolved here, before nodes exist.
6. **Dedup and ordering.** Duplicate actions collapse by exact identity
   `(verb, target, functionPath, collectionBatched)`, where `target` equality
   means same dimension order and same row set. Ordering comes from explicit
   composition (`withAfter`) or the verb's construction rules.

This document defines construction for `check` and `generate`. `UP` and `SHIP`
add their own rules in later docs, but the shared `Plan.services()`/`service()`
surface for `UP` is locked here because it shapes the common `Plan` API.

## Check and Generate

### `check`

The recursive verb. From the current scope, the compiler:

1. reads selected artifacts with `.items`
2. discovers local check occurrences with `artifact.actions([CHECK])`
3. applies `include`/`exclude` to those entrypoints
4. groups retained occurrences by exact `functionPath`
5. resolves collection batching per grouped path
6. recursively compiles referenced checks
7. adds `after` edges from referenced checks to selected entrypoints
8. deduplicates

`include`/`exclude` apply only to entrypoints; referenced checks are always
retained as prerequisites.

If the selected artifacts for a `functionPath` belong to one collection
occurrence and that collection exposes the same handler on its batch type,
compile one `collectionBatched = true` action; otherwise compile item-level
actions. This makes aggregate artifacts useful by default. For the canonical
example's tests:

```text
workspace.artifacts.filterDimension("go-test").plan(verb: CHECK).run
```

if `GoTests` exposes `run` on its batch type (see
[collections.md § Batch shadowing](./collections.md#batch-shadowing)), this
yields one batched action over `TestFoo`+`TestBar`; without it, one action per
test.

### `generate`

Conservative — no recursive expansion:

- Include only local generate handlers on the artifact.
- Do not recurse through references.
- Do not make `generate` an implicit prerequisite of other verbs.
- `Plan.changes()` merges the selected generate results into one `Changeset`.

This avoids surprising workspace mutations.

## Plan Execution

Execution has two layers: **evaluate** the selected functions to their typed
results, then **realize** the verb effect. For `CHECK` these collapse together.

- **GENERATE** evaluates to one `Changeset` per generate action; `Plan.changes()`
  merges them; `Plan.run()` performs the full generate.
- **UP** evaluates to one `Service` per up action; `Plan.services()` /
  `Plan.service()` expose them; `Plan.run()` performs the long-running behavior.
  `Plan.service()` is a preflight singleton check: it errors before running
  anything expensive unless the plan structurally resolves to exactly one
  service-producing invocation (a batched action counts once; an item-level
  action counts once per targeted artifact).

`dagger check`/`generate` always compile a Plan and then run it. `--plan` stops
after compilation and displays the DAG. Within a plan, actions with no pending
"after" dependency run concurrently.

## Decisions

- A Plan is a finite DAG of Actions with "after" edges; parallelism is implicit.
  No loops, conditionals, or variables.
- Verb is part of Action identity; one Action is
  `(verb, target, functionPath, collectionBatched)`.
- Artifacts select artifacts; plans select entrypoints. `Artifacts.plan(...)` is
  the single public choke point — exact entrypoint selection is not a separate
  filter API on `Artifacts`.
- Function paths are artifact-relative and exact. `FunctionPattern` is a
  distinct engine-owned selector syntax.
- Entrypoint selectors select entrypoints only; dependencies are pulled in
  automatically and never filtered directly.
- Batching is resolved at compile time; executors never infer it.
- Plan edges are semantically static; DagQL IDs reference already-compiled
  edges, they are not late-bound selectors.
- `Action.withAfter` is public: used internally during compilation and available
  to users building custom plans. Action and Plan stay separate types; reachable
  discovery lives on `Artifact.actions(...)`, not on `Artifacts`.
- `Plan.changes()`/`services()`/`service()` expose evaluated verb results.
  `Plan.run()` and `Action.run()` return void on success, error on failure.
- `dagger check -l` prints plain paths for one artifact and a minimal
  distinguishing table for several.
- Replaces `CheckGroup`. Transition path: `CheckGroup` → Execution Plans.
