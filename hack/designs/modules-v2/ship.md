# Ship

## Status: Designed

Depends on: [Artifacts](./artifacts.md), [Execution Plans](./plans.md)

## Summary

`ship` extends Artifacts and Execution Plans with an explicit authored shipping
verb. Authors mark direct ship handlers with `+ship`. `filterShip` remains a
structural artifact projection, while `Artifacts.ship` compiles the selected
artifacts' local `+ship` handlers into a Plan.

By default, `ship(A)` depends on `check(A)` before running local ship handlers
on `A`. `noCheck: true` and `dagger ship --no-check` opt out of that
prerequisite. `ship` does not recursively ship every referenced artifact by
default.

Environment selection, approvals, branch/event gating, secret policy, and
protected-environment rules are out of scope for this layer.

## Problem

1. **Missing authored verb** - `Execution Plans` defines the generic DAG
   substrate, but it does not yet define what `ship` means.
2. **Missing default safety edge** - shipping needs a default validation step
   before side effects happen.
3. **Wrong layer for deployment policy** - preview/staging/prod selection and
   approval policy matter, but they are not Artifacts or Plans concerns.

## Solution

Introduce `ship` as an authored verb on top of the existing Artifacts/Plans
stack. `filterShip` selects artifacts structurally. `Artifacts.ship` compiles
local `+ship` handlers into a Plan and, by default, inserts `check(A) →
ship(A)` ordering for each selected artifact. Higher-layer deployment policy
remains out of scope.

## Authoring

`+ship` uses the same explicit authored-verb model as `+check` and
`+generate`.

- Only direct `+ship` handlers participate in ship plan construction.
- Functions named `deploy`, `publish`, or similar do not participate unless
  they are marked `+ship`.
- `filterShip` is structural only. It narrows the artifact scope; it does not
  compile or execute anything by itself.

Example:

```go
type Docs struct{}

// +ship
func (m *Docs) Publish() string {
	return "https://example.com/docs"
}
```

## Schema

`filterShip` is part of the Artifacts selector surface defined in
[artifacts.md](./artifacts.md). This document adds ship plan compilation:

```graphql
extend type Artifacts {
  """
  Compile a ship execution plan for the selected artifact set.
  By default, each selected artifact is checked before its local ship handlers
  run.
  """
  ship(noCheck: Boolean = false): Plan!
}
```

## Plan Construction

### Local Shipping

- Include local `+ship` handlers on artifact A.
- Do not recursively ship every referenced artifact by default.

Raw references are too broad to define automatic ship propagation.

### Default Validation

- Unless `noCheck` is true, compile `check(A)` before local ship handlers on
  A.
- This reuses the existing `check` construction rules from
  [plans.md](./plans.md), so dependency validation follows `check` semantics.

That means default shipping gets recursive validation without recursively
shipping every referenced artifact.

### Examples

Default behavior:

```text
check(go deps...) ──▶ check(go) ──▶ ship(go)
```

Opt out:

```text
ship(go)
```

## CLI

```bash
$ dagger ship --module=go
# compile default check(go) -> ship(go) behavior

$ dagger ship --module=go --no-check
# compile ship(go) only
```

The CLI is a thin mapping over the engine API:

- `dagger ship` → `workspace.artifacts.filterShip.ship()`
- `dagger ship --no-check` → `workspace.artifacts.filterShip.ship(noCheck: true)`

## Out Of Scope

- preview, staging, or prod target selection
- explicit approvals or confirmations
- branch or event gating
- secret availability and protected-environment policy

Those concerns belong to a higher layer that consumes `ship`, not to the core
meaning of the verb itself.
