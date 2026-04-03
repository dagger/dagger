# Execution Plans

## Status: Designed

Depends on: [Artifacts](./artifacts.md)

## Table of Contents

- [Summary](#summary)
- [Implementation](#implementation)
- [Schema](#schema)
- [Design Decisions](#design-decisions)
- [Actions](#actions)
- [Plan Construction](#plan-construction)
- [Check And Generate Construction](#check-and-generate-construction)
- [Plan Execution](#plan-execution)
- [Future Work](#future-work)
- [Locked Decisions](#locked-decisions)
- [Open Questions](#open-questions)

## Summary

Execution Plans introduces a generic `Action`/`Plan` substrate for verb
orchestration. A Plan is a DAG of Actions with "after" edges. `dagger check`
and `dagger generate` compile to inspectable plans in this unit. Later docs
such as [Ship](./ship.md) extend the same substrate with additional
verb-specific construction rules.

Replaces CheckGroup. Transition path: CheckGroup → Execution Plans.

## Implementation

This design is intended to land as one primary implementation unit:

- **PR:** `verbs: add plans, migrate check + generate, remove old path`
- **API:** `Action`, `Plan`, `Artifacts.actions`, `Artifact.actions`,
  `Artifacts.action`, `Artifact.action`, `Artifacts.filterVerb`,
  `Artifacts.check`,
  `Artifacts.generate`
- **UI:** `dagger check --plan`, `dagger generate --plan`, plus the first
  public rollout of the Artifacts selector UX: `dagger list`,
  `dagger list <dimension>`, `dagger list <dimension> --check`, and the first
  non-collection typed filters on `check` / `generate`

Included in this unit:

- the Action/Plan substrate
- migration of `dagger check` and `dagger generate` onto `Artifacts`
- public rollout of the selector surfaces defined in [artifacts.md](./artifacts.md)
- removal of the old `ModTree` / `CheckGroup` / `GeneratorGroup` path

Deferred to [ship.md](./ship.md):

- `Artifacts.ship`
- `+ship`
- ship-specific plan construction rules

Deferred to [collections.md](./collections.md):

- collection-provided dimensions
- collection-aware batch lowering and shadowing

### Pull Request Description

```text
This PR implements the Execution Plans design unit. It adds `Action`, `Plan`,
`Artifacts.actions`, `Artifact.actions`, `Artifacts.action`, `Artifact.action`,
`Artifacts.filterVerb`, `Artifacts.check`, and `Artifacts.generate`; makes
`dagger list` public;
migrates `dagger check` and `dagger generate` onto the Artifacts/Plans stack;
publicly rolls out the selector surfaces defined in Artifacts, including the
first non-collection typed filters; and removes the old `ModTree` /
`CheckGroup` / `GeneratorGroup` path.
```

## Schema

```graphql
extend type Artifacts {
  """
  Union of direct non-traversal function names across all in-scope artifacts.
  Zero-arg object-returning members participate in selector traversal instead.
  """
  actions: [String!]!

  """
  Create an action targeting the in-scope artifacts that directly expose this
  function name. Errors if no in-scope artifact exposes it.
  """
  action(name: String!): Action!

  """Compile a check execution plan for the selected artifact set."""
  check: Plan!

  """Compile a generate execution plan for the selected artifact set."""
  generate: Plan!
}

extend type Artifact {
  """Available direct non-traversal function names on the underlying object."""
  actions: [String!]!

  """Create an action targeting this single artifact."""
  action(name: String!): Action!
}

"""
A callable action: one or more artifacts + a function.
Actions are the building blocks of execution plans.
"""
type Action {
  """The artifacts this action targets."""
  artifacts: [Artifact!]!

  """The function to call."""
  name: String!

  """Type definition of the function, for introspection."""
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
Parallel execution is implicit: actions with no pending "after"
dependencies run concurrently.

NOTE FOR IMPLEMENTERS: Each action is backed by a DAGQL call chain
under the hood. The Action/Artifact API is a clean projection over
engine-internal DAGQL structures. Use existing engine-internal call
chain representations rather than building parallel ones.
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

- **Plan = DAG of Actions.** Each action is (artifacts, function) with "after"
  edges. Parallel is implicit — actions with no pending dependencies run
  concurrently.
- **Always compiled.** `dagger check` and `dagger generate` always compile a
  Plan, then execute it. `--plan` stops before execution and displays the plan.
- **Engine compiles, CLI displays.** The engine owns plan compilation
  (`Artifacts.check → Plan`, `Artifacts.generate → Plan`). The CLI decides
  whether to call `run` or display the plan nodes.
- **Plans materialize all implicit config.** Workspace defaults, filter
  results, batch-vs-item decisions — all collapsed into concrete Actions.
  The plan is the "fully resolved" view. No more "what will this actually
  do?"
- **No mini-VM.** Plans are finite DAGs. No loops, conditionals, variables.
  "Query plan, not bytecode VM."
- **The dang test.** If a plan step can't be rendered as a readable public API
  action, it's the wrong layer. Every Action is backed by a DAGQL call chain
  that the user could understand and run themselves.
- **Eager binding.** Plan DAGs are built bottom-up: leaf actions first, then
  dependents referencing their IDs. All references are resolved at plan
  construction time via DAGQL's native ID system.
- **Run returns void.** `Plan.run` and `Action.run` return void on success,
  error on failure. Outputs are side effects (telemetry, TUI, filesystem).
  Structured per-action results for CI are a known extension point, deferred.

## Actions

An Action bridges artifacts and functions:

- `Artifact.action("lint")` → action on one artifact
- `Artifacts.action("lint")` → action on all in-scope artifacts (batch)
- `Action.after` → DAG edges to other actions
- `Action.run` → execute just this action

Actions are the building blocks of Plans. A Plan is a DAG of Actions with
"after" edges. The engine compiles verb invocations such as `Artifacts.check`
into Plans by selecting the relevant actions from the current artifact scope
and adding appropriate ordering.

### Example — two checks, batched

```
workspace.artifacts
  .filterVerb(CHECK)
  .filterDimension("module", ["go"])
  .filterDimension("platform", ["linux", "darwin"])
  .action("run")
  # → one Action targeting the selected platform artifacts
```

### Example — two checks, per-item

```
a1 = workspace.artifacts
       .filterVerb(CHECK)
       .filterDimension("module", ["go"])
       .filterDimension("platform", ["linux"])
       .action("run")
a2 = workspace.artifacts
       .filterVerb(CHECK)
       .filterDimension("module", ["go"])
       .filterDimension("platform", ["darwin"])
       .action("run")
# → one Action per selected artifact
```

### Example — DAG with ordering

```
prepare = artifacts.filterDimension("module", ["go"]).action("prepare")
run     = artifacts
           .filterVerb(CHECK)
           .filterDimension("module", ["go"])
           .filterDimension("platform", ["linux"])
           .action("run")
           # run.after = [prepare.id]
publish = artifacts.filterDimension("module", ["go"]).action("publish")
           # publish.after = [run.id]
```

Rendered as a visual DAG:

```
  prepare(go) ──▶ run(go,linux) ──▶ publish(go)
```

## Plan Construction

Plan compilation has three parts:

1. **Selection.** User-provided filters
   (`--module=go --platform=linux`) become `filterDimension` chains on
   `Artifacts`.
2. **Action discovery.** The engine turns the retained direct handlers for the
   selected verb into concrete Actions. Batch-vs-item decisions are resolved
   here.
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

## Future Work

### Structured Results

`Plan.run` and `Action.run` return void. Structured per-action results for
CI/programmatic consumers are a known extension point.

## Locked Decisions

- **`Action.withAfter` is part of the public API.** The engine uses it
  internally during plan compilation, and users can use it to build custom
  plans. This avoids a separate "engine-only" construction path and ensures
  the public API is self-sufficient from day one.

## Open Questions

1. Exact transition path from CheckGroup to Plan.
