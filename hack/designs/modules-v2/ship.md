# Ship

## Status: Designed

Depends on: [Artifacts](./artifacts.md), [Execution Plans](./plans.md)

## Table of Contents

- [Definition](#definition)
- [Problem](#problem)
- [Solution](#solution)
- [Authoring](#authoring)
- [Schema](#schema)
- [Plan Construction](#plan-construction)
- [Caching](#caching)
- [CLI](#cli)
- [Out Of Scope](#out-of-scope)

## Summary

`ship` is an authored verb.
Authors mark ship handlers with `+ship`.
`filterVerb(SHIP)` only picks artifacts.
`Artifacts.ship` turns the selected local `+ship` handlers into a Plan.

By default, `ship(A)` runs after `check(A)`.
`noCheck: true` and `dagger ship --no-check` remove that edge.
`ship` does not walk all references and ship them too.

This layer does not choose preview, staging, or prod.
This layer does not handle approvals, branch gates, secret policy, or
protected environments.
This layer also does not define structured ship results yet.
Ship actions are never cached.

Example:

- A ship handler may create a deploy URL.
- `dagger ship` still reports only success or failure today.

## Definition

A ship function reconciles external systems with artifact `X`, so repeated
runs keep converging toward the same result.
That is what `+ship` is for.

An external system is any system that Dagger does not own.

Examples:

- Push an image to a registry tag like `my-app:latest`
- Deploy an app to Kubernetes
- Publish docs to a website
- Send a release notification

These are not ship functions:

- Build an image
- Run tests
- Generate source files
- Read a deploy status

Why?
Because these do not reconcile external systems with the artifact.

For module developers:

- Use `+ship` when your function is meant to reconcile outside systems with an
  artifact.
- Do not use `+ship` for build, check, generate, read-only, or one-off
  notification functions.

Example:

- `PublishDocs()` uploads docs to a website. Use `+ship`.
- `BuildDocs()` builds a static site locally. Do not use `+ship`.

For Dagger users:

- Use `dagger ship` when you want Dagger to reconcile outside systems with an
  artifact now.
- Do not use `dagger ship` when you only want data, checks, or generated
  files.

Example:

- “Deploy this app now” → `dagger ship`
- “Run tests” → `dagger check`
- “Generate code” → `dagger generate`

## Problem

1. **No ship meaning yet** - `Execution Plans` gives us the DAG model, but it
   does not say what `ship` means.
2. **Need a safe default** - Shipping usually needs a check step before it
   does side effects.
3. **Targets are a different problem** - Preview, staging, prod, and approval
   policy do not belong in Artifacts or Plans.

## Solution

Add `ship` on top of Artifacts and Plans.
`filterVerb(SHIP)` picks artifacts by structure.
`Artifacts.ship` compiles local `+ship` handlers into a Plan.
By default, it adds `check(A) → ship(A)` for each selected artifact.
Target choice and approval policy stay in a higher layer.

Example:

- Pick one app artifact.
- Build a plan.
- Run `check(app)` first.
- Then run `ship(app)`.

## Authoring

`+ship` follows the same authored-verb idea as `+check` and `+generate`.

- Only direct `+ship` handlers join ship plan construction.
- `+ship` marks intent. It does not enforce a function shape by itself.
- A `+ship` function may have required args in code. That is not an error.
- But it is only actionable in the default ship UX if Dagger can run it with
  no user-supplied args after defaults and config are applied.
- Those values may come from function defaults, user defaults, module config,
  or workspace config.
- A function named `deploy` or `publish` does not join `ship` unless it is
  marked `+ship`.
- `filterVerb(SHIP)` only filters. It does not compile or run anything.

Example:

```go
type Docs struct{}

// +ship
func (m *Docs) Publish(ctx context.Context) error {
	// side effects happen inside the function; dagger ship itself returns void
	return nil
}
```

Another example:

```go
type App struct{}

// +ship
func (m *App) Deploy(ctx context.Context, env string) error {
	return nil
}
```

This is still valid.
If `env` comes from config, then `dagger ship` can run it.
If `env` does not come from config, then this handler is not actionable in the
default ship UX.

## Schema

`filterVerb(SHIP)` is part of the Artifacts selector surface defined in
[artifacts.md](./artifacts.md).
This document adds ship plan compilation:

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

Raw references are too broad for automatic ship propagation.

Example:

- App `A` uses image `B`.
- `ship(A)` does not also call `ship(B)` by default.

### Default Validation

- Unless `noCheck` is true, compile `check(A)` before local ship handlers on
  `A`.
- This reuses the `check` construction rules from [plans.md](./plans.md).

So default shipping gets recursive validation, but not recursive shipping.

Example:

- `A` depends on `B`.
- `check(A)` may check `B` first.
- `ship(A)` still only ships `A`.

### Plan Examples

Default behavior:

```text
check(go deps...) ──▶ check(go) ──▶ ship(go)
```

Opt out:

```text
ship(go)
```

## Caching

Ship functions are never cached.
Dagger must always run them.
In other words, they are always invalidated.

Why?
Because ship functions have side effects.
They also have a hidden input: the current state of the external system.
Dagger does not know that state.
So Dagger cannot build a safe cache key for a ship action.

Example:

- A ship function deploys to a cluster.
- The cluster may have changed since the last run.
- Dagger cannot safely say “cache hit”.
- So Dagger must run the deploy again.

Another example:

- A ship function pushes `my-app:latest`.
- The tag may already point to a different image now.
- Dagger must run the push again.

## CLI

```bash
$ dagger ship --module=go
# compile default check(go) -> ship(go) behavior

$ dagger ship --module=go --no-check
# compile ship(go) only
```

The CLI is a thin layer over the engine API:

- `dagger ship` → `workspace.artifacts.filterVerb(SHIP).ship()`
- `dagger ship --no-check` → `workspace.artifacts.filterVerb(SHIP).ship(noCheck: true)`

## Out Of Scope

- preview, staging, or prod target selection
- explicit approvals or confirmations
- branch or event gating
- secret availability and protected-environment policy

Those concerns belong to a higher layer.

Example:

- Deciding between preview and prod is not part of `ship`.
- A higher layer must decide that.
