# `ModuleSource.generateLocalDependencies` — codegen a module's local dependency closure

Scope: in-module codegen ordering for local dependencies. Implements [#13706]
("option 3" of [#13662]); builds on CWD-aware SDKs ([#13688]).

## Two "SDKs" (don't conflate)

- **runtime SDK** — bundled in the engine image (`sdkFunctions`,
  `core/sdk/consts.go`). Still exposes `codegen` today; **long-term only
  `runtime`.**
- **dang SDK** — the external dang module, e.g. `github.com/dagger/typescript-sdk`
  (`core/sdk/workspace_module.go`). Exposes the `@generate` workspace generator
  and **owns codegen going forward**.

`dagger generate` runs the dang SDK's `@generate`. Because codegen is migrating
out of the engine into the dang SDK, this API routes through `@generate`, never
the engine's `runGeneratedContext`/`codegen`.

## Problem

TOML modules (`dagger-module.toml`) never runtime-codegen — they build from
committed files (`useRuntimeCodegen`, `core/sdk/utils.go`). And loading is
recursive: `asModule` builds its deps via `loadDependencyModules`
(`core/schema/modulesource.go:3313`), so loading a module loads its whole local
dep tree. Editing several inter-dependent local modules at once therefore
deadlocks: to codegen `app-py` (python → `lib-go`, go) the engine loads
`lib-go`; if `lib-go`'s committed bindings are stale they can't be repaired at
load, so codegen fails. `lib-go` must be generated first.

The generator scheduler can't express that:

1. **No ordering** — `@generate` functions run concurrently, no topo sort
   (`GeneratorGroup.Run`).
2. **No staged visibility** — changesets merge only after all generators finish
   (`GeneratorGroup.Changes`); one's output never feeds another.
3. **Cross-SDK** — `lib-go` is the Go dang SDK's, `app-py` the Python dang SDK's;
   per-SDK leaf ordering ([#13661]) can't see the edge.

## Design

`generateLocalDependencies(workspace)` walks the module's **transitive local
dependency closure leaf-first**, and for each dep invokes **that dep's dang SDK
`@generate`** against a workspace scoped to the dep with the already-generated
deps overlaid. It accumulates each dep's artifacts and returns the closure
changeset; the module's own `@generate` overlays it, then generates itself.

```text
dagger generate
  └─ dang SDK @generate(workspace: ws)               # ws.cwd = X; auto-injected at top level
       ├─ deps = X.generateLocalDependencies(ws)
       │     for D in leafFirst(transitiveLocalClosure(X)):   # e.g. C then B
       │        wsD  = ws.clone(Cwd=D.path).withChanges(acc)  # scope to D + stage prior deps
       │        acc += dangSDK(D).@generate(workspace: wsD)   # D's artifacts only
       │     return acc                                        # e.g. B + C
       ├─ staged = ws.withChanges(deps)
       └─ codegen X against staged → diff against staged → X only
```

Ordering is introduced **locally, per module, on demand** — the global scheduler
stays concurrent, and the per-dep entrypoint is the *same* `@generate` a user
runs, so they never drift.

Three rules make it correct:

- **`@generate` returns module-only** (not its `generateLocalDependencies`
  output). At the root every module is also generated on its own, so this keeps
  each dep's codegen out of others' changesets and the octopus merge
  conflict-free. Staged deps are ephemeral.
- **`generateLocalDependencies` is transitive.** Since `@generate(B)` no longer
  carries `C` upward and loading `B` recursively loads `C`, `A` needs **B + C**
  staged — so the walk goes to the leaves. It regenerates the whole closure
  (idempotent/cache-cheap when a dep was already current, since staleness isn't
  knowable without generating). `Dependencies` is direct-only
  (`core/modulesource.go:202`); recurse it, filtering `LOCAL_SOURCE`
  (`core/modulesource.go:50-60`).
- **Each dep dispatches to its own dang SDK** (`depSrc.SDKImpl`) — a Go dep → Go
  SDK, a Python dep → Python SDK. This cross-SDK fan-out is why it's an engine
  API, not something one SDK can do.

The nested `@generate(D)` re-derives `D`'s own deps via *its*
`generateLocalDependencies`, redundant with the parent's `acc` but idempotent and
cache-deduped — the accepted cost of the single entrypoint.

## Passing the scoped workspace

`@generate` reads `.cwd` from its `workspace` **argument**, which is auto-injected
from `currentWorkspace` only for calls outside a module function (the `dagger
generate` walk); explicit args skip injection (`core/modfunc.go:660`), and module
functions must pass `Workspace` explicitly (`loadWorkspaceArg`,
`core/modfunc.go:1247`). So the engine clones the workspace, sets its `Cwd` field
to the dep path, applies the overlay, and passes that one workspace — no separate
cwd param, no client-context override. `generateLocalDependencies` receives its
base workspace the same way.

## API

```graphql
extend type ModuleSource {
  """
  Generate this module's transitive local dependency closure leaf-first and
  return the accumulated changeset, to be overlaid (withChanges) before
  generating this module. Each local dependency is generated by its own SDK
  generator, scoped to it. Remote (git) dependencies are assumed committed and
  skipped. This is not the module's own generated code.
  """
  generateLocalDependencies(workspace: Workspace!): Changeset!
}
```

## Implementation

- New `dagql.NodeFunc("generateLocalDependencies", …)` near
  `core/schema/modulesource.go:212`, gated `View(AfterVersion("v1.0.0-0"))`
  (`internal-docs/version-gating.md`).
- Handler: build + leaf-first sort the transitive `LOCAL_SOURCE` closure, loop
  constructing `wsD` and invoking the dep's `@generate`, folding with
  `Changeset.WithChangeset` (`core/changeset.go:913`).
- Each dang SDK's `@generate` calls `generateLocalDependencies(ws)` →
  `withChanges` → generates its module → returns module-only. Cross-repo rollout:
  go/python/typescript, then java/php/….

## Scope / non-goals

- Local dependencies only; git deps assumed committed. Local toolchains (legacy):
  TBD.
- Global scheduler unchanged.
- Not the Go-SDK self-bootstrap cycle ([#13605]).
- Not option 2 (`Workspace.generate(path)`) — kept for Collections, not needed
  here.

## Open

- **Cache stability of the staged workspaces.** Each per-dep `@generate` runs
  against a distinct `(cwd, overlay)` workspace; keeping cost bounded relies on
  codegen purity so shared deps at the same staged state hit cache (and the nested
  re-derivation dedupes). Confirm with a prototype.

## Status

Converged on mechanism; no code yet. Branch
`feat/module-source-generate-local-dependencies`.

[#13706]: https://github.com/dagger/dagger/issues/13706
[#13662]: https://github.com/dagger/dagger/issues/13662
[#13688]: https://github.com/dagger/dagger/issues/13688
[#13661]: https://github.com/dagger/dagger/pull/13661
[#13605]: https://github.com/dagger/dagger/issues/13605
