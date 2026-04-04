# Execution Plans

## Status: Designed

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
orchestration. A Plan is a DAG of Actions with "after" edges. `dagger check`
and `dagger generate` compile to inspectable plans in this unit. Later docs
such as [Ship](./ship.md) extend the same substrate with additional
verb-specific construction rules.

Replaces CheckGroup. Transition path: CheckGroup → Execution Plans.

This document builds on the artifact model from [artifacts.md](./artifacts.md):

- top-level module objects are artifact rows
- collection items are artifact rows
- ordinary nested objects are structural glue

Action discovery walks that structural glue and produces **artifact-relative**
action paths such as `lint`, `tests:run-bun`, or `tests:generate-fixtures`.

## Schema

```graphql
extend type Artifacts {
  """
  Reachable action occurrences on the current artifact set.
  One row per `(artifact, path)` occurrence.
  """
  actions: [Action!]!

  """
  Create an action targeting all in-scope artifacts that expose this
  artifact-relative path.
  """
  action(path: String!): Action!

  """Compile a check execution plan for the selected artifact set."""
  check: Plan!

  """Compile a generate execution plan for the selected artifact set."""
  generate: Plan!
}

extend type Artifact {
  """Reachable actions on this artifact, named by artifact-relative path."""
  actions: [Action!]!

  """Create an action targeting this single artifact."""
  action(path: String!): Action!
}

"""
A callable action: one or more artifacts plus one reachable handler path.
Actions are the building blocks of execution plans.
"""
type Action {
  """
  The artifacts this action targets.

  - `Artifact.actions` and `Artifacts.actions` return unbatched occurrences, so
    this list has length 1 there.
  - `Artifacts.action(path)` may batch several selected artifacts that expose
    the same path.
  - Plan compilation may also batch actions when the verb allows it.
  """
  artifacts: [Artifact!]!

  """
  Artifact-relative colon-separated path.
  Examples: `lint`, `tests:run-bun`.
  """
  path: String!

  """Leaf function name at the end of `path`."""
  functionName: String!

  """Type definition of the leaf function, for introspection."""
  function: Function

  """Actions that must complete before this one runs."""
  after: [ActionID!]!

  """Return a new Action with additional ordering dependencies."""
  withAfter(actions: [ActionID!]!): Action!

  """Execute this action."""
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
  """All actions in this plan."""
  nodes: [Action!]!

  """
  Execute the plan. Returns void on success, error on failure.
  All other outputs (check results, generated files, future verb side effects)
  are observed through telemetry, TUI, or the filesystem.
  """
  run: Void
}
```

## Design Decisions

- **Plan = DAG of Actions.** Each action is `(artifacts, path)` with "after"
  edges. Parallel is implicit — actions with no pending dependencies run
  concurrently.
- **Action paths are artifact-relative.** The action model is `(artifact,
  path)`, not one global path grammar.
- **Always compiled.** `dagger check` and `dagger generate` always compile a
  Plan, then execute it. `--plan` stops before execution and displays the plan.
- **Engine compiles, CLI displays.** The engine owns plan compilation
  (`Artifacts.check → Plan`, `Artifacts.generate → Plan`). The CLI decides
  whether to call `run` or display the plan nodes.
- **Plans materialize all implicit config.** Workspace defaults, filter
  results, batch-vs-item decisions — all collapsed into concrete Actions.
- **No mini-VM.** Plans are finite DAGs. No loops, conditionals, variables.
- **Run returns void.** `Plan.run` and `Action.run` return void on success,
  error on failure.

## Actions

An Action bridges artifacts and functions:

- `Artifact.action("lint")` → one action on one artifact
- `Artifact.action("tests:run-bun")` → nested action on one artifact
- `Artifacts.action("lint")` → one action over every selected artifact that
  exposes `lint`

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

Whenever that walk reaches a direct non-traversal function with the relevant
verb annotation, that function becomes a reachable action.

The action path is the colon-separated path from the artifact root to that
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

The artifact root name itself is **not** part of the action path. So this is
the normal form:

```console
$ dagger check --type=go lint
```

not:

```console
$ dagger check --type=go go:lint
```

### Enumeration

`Artifact.actions` returns all reachable action occurrences on that artifact.

`Artifacts.actions` returns all reachable action occurrences across the
selected artifact set.

These are **unbatched** occurrences:

- one `Action` row per `(artifact, path)` occurrence
- `action.artifacts` therefore has length 1 for these two enumeration APIs

By contrast, `Artifacts.action(path)` may batch several selected artifacts that
expose the same path.

### Examples

One artifact:

```text
workspace.artifacts
  .filterCoordinates("type", ["go"])
  .items[0]
  .actions
```

might produce:

```console
lint
tests:run-bun
tests:run-nodejs
```

Several selected artifacts:

```text
workspace.artifacts
  .filterCoordinates("type", ["go-test"])
  .actions
```

might produce:

```console
(TestFoo, run)
(TestBar, run)
```

Batched action lookup:

```text
workspace.artifacts
  .filterCoordinates("type", ["go-test"])
  .action("run")
```

means:

- keep the currently selected `go-test` rows
- retain only the rows that expose `run`
- create one Action targeting that retained set

### CLI Listing

`dagger check -l` and `dagger generate -l` list action occurrences.

If all listed actions belong to one artifact row, the CLI prints plain
artifact-relative action paths:

```console
$ dagger check --type=go -l
lint
tests:run-bun
```

If several artifact rows are in play, the CLI prints a table:

- one column per artifact dimension needed to distinguish the listed rows
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

Example with collection rows:

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
rows.

### Compatibility Input

For compatibility, the CLI may also accept:

```console
<type>:<action-path>
```

as shorthand for:

```console
--type=<type> <action-path>
```

Examples:

```console
$ dagger check go:lint
$ dagger check go-test:run
```

This is input sugar only. The primary listing format is the plain path or the
table format above.

## Plan Construction

Plan compilation has three parts:

1. **Selection.** User-provided filters such as `--type=go` or
   `--type=go-test --go-test=TestFoo`
   become `filterDimension` chains on `Artifacts`.
2. **Action discovery.** The engine turns the retained reachable handlers for
   the selected verb into concrete Actions. Rollup through structural glue and
   batch-vs-item decisions are resolved here.
3. **Action ordering.** The engine adds `after` edges between Actions.
   Ordering may come from explicit user composition (`withAfter`) or from the
   construction rules of the compiled verb.

This document defines automatic construction rules only for `check` and
`generate`.

## Check And Generate Construction

### `check`

The most recursive verb in this unit.

- Include local check handlers on artifact A.
- Recursively include `check(B)` for each artifact B referenced by A.
- If A references B, run `check(B)` before local check handlers on A.
- If collection-aware batch behavior exists for the current scope, prefer it
  over expanding to one item-level `check` per item.

This makes aggregate artifacts useful by default.

### `generate`

Conservative — no recursive expansion.

- Include local generate handlers on artifact A.
- Do not recursively generate through references by default.
- Do not make `generate` an implicit prerequisite of other verbs.

This avoids surprising workspace mutations.

## Plan Execution

Once a Plan is constructed, execution is generic DAG walking. Actions with no
pending "after" dependencies run concurrently. `--plan` stops after
compilation and displays the DAG without executing it.

## Locked Decisions

- **`Action.withAfter` is part of the public API.** The engine uses it
  internally during plan compilation, and users can use it to build custom
  plans.
- **Action paths are artifact-relative.** There is no separate canonical
  workspace-global action path in this design.
- **`dagger check -l` is table-capable.** It prints plain paths for one
  artifact row and a minimal distinguishing table for several rows.
