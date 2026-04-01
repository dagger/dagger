# Execution Plans

## Status: Designed

Depends on: [Artifacts](./artifacts.md)

## Summary

Verbs (`check`, `generate`, `ship`, `up`) compile to an inspectable Plan
before execution. A Plan is a DAG of Actions with "after" edges. Parallel
execution is implicit: actions with no pending dependencies run concurrently.

Replaces CheckGroup. Transition path: CheckGroup → Execution Plans.

## Schema

```graphql
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
  All other outputs (check results, deployment URLs, generated files)
  are side effects observed through telemetry, TUI, or the filesystem.
  """
  run: Void
}
```

## Design Decisions

- **Plan = DAG of Actions.** Each action is (artifacts, function) with "after"
  edges. Parallel is implicit — actions with no pending dependencies run
  concurrently.
- **Always compiled.** `dagger check` always compiles a Plan, then executes
  it. `--plan` stops before execution and displays the plan.
- **Engine compiles, CLI displays.** The engine owns plan compilation
  (`Artifacts.check → Plan`). The CLI decides whether to call `run` or
  display the plan nodes.
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
"after" edges. The engine compiles verb invocations (`Artifacts.check`) into
Plans by selecting the relevant actions from the current artifact scope and
adding appropriate ordering.

### Example — two checks, batched

```
workspace.artifacts
  .filterCheck
  .filterBy("module", ["go"])
  .filterBy("platforms", ["linux", "darwin"])
  .action("run")
  # → one Action targeting the selected platform artifacts
```

### Example — two checks, per-item

```
a1 = workspace.artifacts
       .filterCheck
       .filterBy("module", ["go"])
       .filterBy("platforms", ["linux"])
       .action("run")
a2 = workspace.artifacts
       .filterCheck
       .filterBy("module", ["go"])
       .filterBy("platforms", ["darwin"])
       .action("run")
# → one Action per selected artifact
```

### Example — DAG with ordering

```
prepare = artifacts.filterBy("module", ["go"]).action("prepare")
run     = artifacts
           .filterCheck
           .filterBy("module", ["go"])
           .filterBy("platforms", ["linux"])
           .action("run")
           # run.after = [prepare.id]
publish = artifacts.filterBy("module", ["go"]).action("publish")
           # publish.after = [run.id]
```

Rendered as a visual DAG:

```
  prepare(go) ──▶ run(go,linux) ──▶ publish(go)
```

## Three Phases

1. **Selection.** User provides filters
   (`--module=go --platforms=linux`). The CLI translates these into
   `filterBy` chains on `Artifacts`.
2. **Compilation.** The engine compiles the filtered scope + verb into a Plan.
   All implicit config is materialized into concrete Actions with "after" edges.
3. **Execution.** The engine walks the DAG. Actions with no pending "after"
   dependencies run concurrently. `--plan` stops after compilation and displays
   the plan without executing.

## Verb Compilation Rules

Each verb has specific rules for how it compiles to a Plan. These rules
govern recursion, ordering, and expansion.

### `check`

The most recursive verb.

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

### `ship`

Stricter than `check`.

- Include local ship handlers on artifact A.
- Do not recursively ship every referenced artifact by default.
- Usually require `check(A)` first unless explicitly skipped.

Raw references are too broad to define automatic ship propagation.

### `up`

Similar to `ship`.

- Include local up handlers on artifact A.
- Do not recursively follow all references by default.
- Likely require `check(A)` or equivalent readiness checks first.

## Future Work

### Shipping in CI

`ship` is targetable via the Artifacts API, but the full CI shipping model is
not yet defined. Open areas:

- **Environment specificity.** The same artifact may ship to preview, staging,
  or prod depending on context. PR workflows should skew toward preview/dev.
- **Dependency policy.** `check` recurses over references; `ship` needs
  stricter and sometimes explicit dependencies.
- **Workflow shape.** Some teams want a custom declarative workflow composing
  `generate`, `check`, `ship`, and approvals. Whether that belongs in schema,
  workspace config, or external CI is open.
- **Safety and policy.** Manual approval, secret availability, branch/event
  gating, protected environments.

### Verb Policy

Workspace or user policy may add gates and ordering on top of core verb
semantics:

- Require `check` before `ship`.
- Require explicit confirmation or target selection for production `ship`.
- Default `ship` target to preview rather than prod.

Policy should refine orchestration, not redefine the core meaning of a verb.

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
