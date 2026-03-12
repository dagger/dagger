# Workspace Porcelain

## Status: In Progress (`workspace-porcelain`)

## Builds On

- [Workspace Foundation + Compatibility](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/workspace-foundation-compat.md)
- [Lockfile: Lookup Resolution](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/lockfile.md)
- [Workspace Binding and Access Control](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/workspace-binding-and-access-control.md)

This branch is a spin-off of the remaining `workspace` branch implementation on top
of `workspace-plumbing`.

## Pinned Source Snapshot

The exact source snapshot for PR B restore work is:

- ref: `origin/workspace`
- commit: `89af484341aa7ab3d547eb1f1de22afbcf9d239f`

Use the commit hash, not the moving branch name, as the authoritative restore source.

Primary implementation diff to triage:

```bash
git diff --name-status workspace-plumbing..89af484341aa7ab3d547eb1f1de22afbcf9d239f
```

For exact file content during restore:

```bash
git show 89af484341aa7ab3d547eb1f1de22afbcf9d239f:path/to/file
```

This makes the branch reproducible even if `origin/workspace` moves later.

This document is the running source of truth for PR B. Update it after every
substantive change so implementation can resume from the filesystem alone.

## Branch Contract

`workspace-porcelain` restores the user-facing workspace product on top of the
runtime bones already landed in `workspace-plumbing`.

This branch owns:

- initialized workspace detection and loading from `.dagger/config.toml`
- explicit workspace management UX
- explicit workspace targeting UX
- `dagger migrate`
- workspace-authored `.dagger/lock` support for `modules.resolve`
- tests, docs, and generated API/SDK surface for the restored workspace product

This branch does not own:

- generic non-workspace lockfile consumers
- lockfile-enabled `container.from`
- changing PR A compat repos to create or depend on `.dagger/lock`

## Problem

After PR A, the engine understands workspaces and legacy `dagger.json` repos keep
working, but the initialized-workspace product is still absent.

That leaves a gap:

1. Users cannot create or manage `.dagger/config.toml` workspaces from this branch.
2. Explicit workspace targeting syntax is absent even though workspace binding exists.
3. Workspace-authored module pins (`modules.resolve`) are deferred, so initialized
   workspaces are not deterministic yet.
4. The migrated product story is incomplete without `dagger migrate`.

PR B needs to restore those user-facing pieces coherently, without dragging PR C's
generic lockfile work back in.

## Scope

### Restore In PR B

- `.dagger/config.toml` detection, parsing, and serialization
- public `Workspace` fields for initialized state and config path
- workspace config-backed module loading in the engine
- workspace-owned `.dagger/lock` read/write behavior for `modules.resolve`
- lock mode plumbing (`--lock`, client metadata, engine handling) only for
  workspace module resolution
- `dagger migrate`
- workspace management commands:
  - `dagger workspace`
  - `dagger workspace info`
  - `dagger workspace config`
  - `dagger init`
  - `dagger install`
  - `dagger update`
- explicit workspace targeting for:
  - `dagger call`
  - `dagger functions`
  - `dagger check`
  - `dagger generate`
- remote workspace targeting UX
- workspace-only tests, docs, and codegen outputs

### Keep From PR A

- direct compat loading for eligible legacy `dagger.json`
- direct legacy pin handling for compat repos with no `.dagger/config.toml`
- workspace argument injection and lazy workspace file/directory access
- current-CWD flows continuing to work without initialization

### Keep Deferred To PR C

- `container.from` lockfile behavior
- generic lookup-lock helpers when they are only needed by non-workspace consumers
- any broader lockfile rollout beyond workspace `modules.resolve`

## Porcelain Principles

PR B is the author-intent layer on top of PR A's runtime truth.

This branch should optimize for:

- explicitness: users can see which workspace they are operating on and why
- trust: repo-owned state changes are understandable before and after they happen
- ownership: initialized-workspace config and lock state belong to the repo, not to
  implicit connect-time compat behavior

If a behavior would be acceptable only because the engine happened to infer it, it is
probably still plumbing. Porcelain should make user intent legible.

## Design Constraints

- No silent auto-conversion on connect.
- `.dagger/config.toml` support and `dagger migrate` stay in the same branch.
- Compat repos without `.dagger/config.toml` must keep using direct legacy pins and
  must not synthesize `.dagger/lock`.
- Workspace-authored `.dagger/lock` applies only to initialized workspaces.
- PR B may restore generic lockfile substrate types/helpers, but only workspace
  `modules.resolve` may consume them in this branch.
- If a restore choice is ambiguous, bias toward reusing the `workspace` branch
  behavior rather than inventing a new product shape here.
- Keep the CLI dumber than the engine when possible:
  - CLI should declare workspace binding / requested lock mode.
  - engine should own detection, config loading, module resolution, and lock use.
- Do not replace the current split ledger with the old `workspace` branch docs.
  Continue recording progress in this branch's own doc.

## Mutation Policy

Repo-owned workspace state begins in PR B, so mutation rules need to be explicit.

- compat repos without `.dagger/config.toml` must not gain `.dagger/config.toml` or
  `.dagger/lock` unless the user runs an explicit porcelain flow such as
  `dagger migrate`
- initialized workspaces may read and write `.dagger/config.toml` and `.dagger/lock`,
  but only through explicit commands and engine operations that are clearly part of
  workspace authoring
- if config or lock mutation rules are ambiguous, prefer leaving the repo unchanged
  over guessing

Surprising repo mutation is a product failure for this branch.

## Engine And CLI Boundary

PR B restores more CLI surface, but the engine still owns workspace semantics.

- CLI selects intent: target workspace, requested operation, requested lock mode
- engine owns meaning: detection, config loading, module resolution, migration, and
  lockfile application
- avoid adding CLI-only targeting or lock semantics that the engine does not also
  understand

## PR B Success Criteria

PR B is successful when:

- initialized workspaces are explicit and inspectable through `.dagger/config.toml`
- `dagger migrate` produces a coherent initialized workspace story, including
  workspace-owned pins for `modules.resolve`
- workspace targeting and management are understandable without knowing PR A
  internals
- config and lock updates happen only in expected, explainable cases
- restored docs and generated surfaces describe the same workspace product users see

## PR B Test Priorities

The highest-value coverage for this branch is:

- migration idempotence and migrated output correctness
- config and lock mutation rules
- workspace root and path-boundary behavior
- explicit workspace targeting semantics
- initialized-workspace module resolution, including `modules.resolve`

## Product Surface To Restore

| Surface | Target behavior in PR B |
| --- | --- |
| Initialized workspace detection | `.dagger/config.toml` marks an initialized workspace and exposes `initialized`, `hasConfig`, `configPath` |
| Workspace management | `workspace info`, `workspace config`, `workspace init` |
| Workspace authoring | `install`, `update`, `moduleInit`, config mutation |
| Workspace targeting | `[workspace --]` / implicit workspace-ref parsing for `call`, `functions`, `check`, `generate` |
| Migration | `dagger migrate` rewrites legacy projects into initialized workspaces |
| Locking | `.dagger/lock` contains workspace `modules.resolve` pins only |
| Docs / generated API | CLI reference, GraphQL schema, SDK bindings reflect restored surface |

## Selective Restore Rules

### Restore Mostly As-Is From `workspace`

- `core/workspace/config.go`
- `core/workspace/migrate.go`
- `core/workspace/migrate_test.go`
- `cmd/dagger/workspace.go`
- `cmd/dagger/workspace_target_args.go`
- `cmd/dagger/call_target_args_test.go`
- `cmd/dagger/checks_test.go`
- `cmd/dagger/functions_test.go`
- `cmd/dagger/generators_test.go`
- `core/integration/workspace_test.go`

### Restore Selectively

- `engine/server/session.go`
- `core/schema/workspace.go`
- `core/schema/lockfile.go`
- `core/workspace/detect.go`
- `core/workspace.go`
- `engine/opts.go`
- `engine/client/client.go`
- `cmd/dagger/engine.go`
- `cmd/dagger/main.go`
- `cmd/dagger/module.go`
- `cmd/dagger/call.go`
- `cmd/dagger/functions.go`
- `cmd/dagger/checks.go`
- `cmd/dagger/generators.go`
- `cmd/dagger/shell_commands.go`
- docs / GraphQL schema / SDK generated files

Selective means:

- preserve PR A's direct legacy-pin compat path for uninitialized repos
- restore initialized-workspace behavior without reintroducing PR C consumers
- restore lock mode transport only insofar as workspace `modules.resolve` needs it

### Do Not Restore From `workspace`

- `core/schema/container.go` lockfile callsites
- any `container.from` lock tests or behavior
- replacing this split ledger with `hack/designs/done/workspace-compat.md`

## Restore Manifest From Source Snapshot

This is the grouped file manifest from:

```bash
git diff --name-status workspace-plumbing..89af484341aa7ab3d547eb1f1de22afbcf9d239f
```

### CLI And Targeting Surface

- `cmd/dagger/workspace.go`
- `cmd/dagger/workspace_target_args.go`
- `cmd/dagger/main.go`
- `cmd/dagger/engine.go`
- `cmd/dagger/module.go`
- `cmd/dagger/call.go`
- `cmd/dagger/functions.go`
- `cmd/dagger/checks.go`
- `cmd/dagger/generators.go`
- `cmd/dagger/shell_commands.go`
- `cmd/dagger/shell_completion_test.go`
- `cmd/dagger/suite_test.go`
- `cmd/dagger/call_target_args_test.go`
- `cmd/dagger/checks_test.go`
- `cmd/dagger/functions_test.go`
- `cmd/dagger/generators_test.go`
- `cmd/dagger/engine_lock_test.go`
- `cmd/dagger/cloud_test.go`

### Workspace Substrate

- `core/workspace.go`
- `core/workspace/config.go`
- `core/workspace/detect.go`
- `core/workspace/detect_test.go`
- `core/workspace/lock.go`
- `core/workspace/lock_test.go`
- `core/workspace/migrate.go`
- `core/workspace/migrate_test.go`
- keep current `core/workspace/legacy_test.go`
  do not delete PR A compat pin coverage just because the `workspace` branch no
  longer had it in this form

### Engine And Schema

- `engine/server/session.go`
- `engine/server/session_lock_test.go`
  restore selectively from the source snapshot; keep the current test coverage
  that still applies and only rename/re-scope where needed
- `engine/opts.go`
- `engine/client/client.go`
- `core/schema/workspace.go`
- `core/schema/workspace_test.go`
- `core/schema/lockfile.go`
- `core/schema/lockfile_test.go`

### Integration Coverage

- `core/integration/workspace_test.go`
- `core/integration/engine_test.go`
- `core/integration/module_call_test.go`
- `core/integration/module_cli_test.go`
- `core/integration/module_shell_test.go`
- `core/integration/module_test.go`

### Docs And Generated Surface

- `docs/current_docs/reference/cli/index.mdx`
- `docs/docs-graphql/schema.graphqls`
- `docs/static/api/reference/index.html`
- `docs/static/reference/php/Dagger/Client.html`
- `docs/static/reference/php/Dagger/ModuleSource.html`
- `docs/static/reference/php/Dagger/Workspace.html`
- `docs/static/reference/php/doc-index.html`
- `docs/static/reference/php/doctum-search.json`
- `docs/static/reference/php/doctum.js`
- `docs/static/reference/php/traits.html`
- `sdk/go/dagger.gen.go`
- `sdk/php/generated/Client.php`
- `sdk/php/generated/Workspace.php`
- `sdk/python/src/dagger/client/gen.py`
- `sdk/rust/crates/dagger-sdk/src/gen.rs`
- `sdk/typescript/src/api/client.gen.ts`

### Lockfile Utility Layer

- `util/lockfile/lockfile.go`
- `util/lockfile/lockfile_test.go`

Restore for PR B because workspace `modules.resolve` needs it.
Do not use the restored utility layer to re-enable non-workspace lock consumers.

### Explicit Non-Restore Items

- `core/schema/container.go`
  source snapshot includes lockfile-enabled `container.from` behavior; leave
  that out of PR B
- `hack/designs/done/workspace-compat.md`
  source snapshot moved docs there, but PR B should continue using
  `hack/designs/workspace-porcelain.md` as the active ledger

## Implementation Plan

### Phase 1: Restore Initialized Workspace State

Goal:

- restore `.dagger/config.toml` detection and the public initialized-workspace shape

Files:

- `core/workspace/detect.go`
- `core/workspace/config.go`
- `core/workspace.go`
- `core/workspace/detect_test.go`

Required behavior:

- `workspace.Detect()` again finds `.dagger/` and parses `config.toml`
- detected workspace carries:
  - `Initialized`
  - `Config`
  - `ConfigPath`
  - `HasConfig`
- git-root fallback and PR A compat fallback remain intact

Acceptance notes:

- initialized workspaces must become distinguishable from bare CWD/git-root workspaces
- no compat repo should require migration to run

### Phase 2: Restore Workspace Lock Substrate For `modules.resolve`

Goal:

- restore workspace-owned `.dagger/lock` and lock mode plumbing only for workspace
  module resolution

Files:

- `core/workspace/lock.go`
- `core/workspace/lock_test.go`
- `util/lockfile/lockfile.go`
- `util/lockfile/lockfile_test.go`
- `core/schema/lockfile.go`
- `engine/opts.go`
- `engine/client/client.go`
- `cmd/dagger/engine.go`
- `cmd/dagger/main.go`
- `cmd/dagger/engine_lock_test.go`

Required behavior:

- `workspace.ParseLockMode` and `LockMode` client metadata return
- `.dagger/lock` parse/marshal and `modules.resolve` helpers return
- `--lock strict|auto|update` returns, but only as a workspace-module feature

Hard boundary:

- do not wire restored helpers into `core/schema/container.go`
- if `core/schema/lockfile.go` is restored, it must only be used from workspace
  module loading in PR B

### Phase 3: Restore Engine Loading For Initialized Workspaces

Goal:

- engine loads config-backed workspace modules, applies workspace lock state, and
  still preserves PR A compat behavior for uninitialized repos

File:

- `engine/server/session.go`

Required behavior:

- restore `ws.Config` module gathering path
- restore initialized-workspace warning/info behavior
- restore workspace `modules.resolve` lock lookup for config modules
- preserve current PR A direct legacy-pin flow for:
  - legacy toolchains
  - legacy blueprints
  - other compat-only refs
- populate workspace metadata needed by CLI and schema:
  - `Initialized`
  - `HasConfig`
  - `ConfigPath`
  - `DefaultModule`

Module-gathering order should remain explicit:

1. workspace config modules
2. legacy compat modules
3. implicit CWD module
4. extra modules (`-m`)

### Phase 4: Restore Workspace GraphQL Mutations

Goal:

- restore the engine-owned mutation surface that backs the CLI

Files:

- `core/schema/workspace.go`
- `core/schema/workspace_test.go`

Restore:

- `workspace.init`
- `workspace.install`
- `workspace.moduleInit`
- `workspace.configRead`
- `workspace.configWrite`
- `workspace.update`

Also restore the needed host I/O helpers for config and lock writes, but do not add
new product behavior beyond what `workspace` already had.

### Phase 5: Restore CLI Porcelain And Targeting

Goal:

- bring back the explicit workspace command tree and explicit targeting syntax

Files:

- `cmd/dagger/workspace.go`
- `cmd/dagger/workspace_target_args.go`
- `cmd/dagger/main.go`
- `cmd/dagger/call.go`
- `cmd/dagger/functions.go`
- `cmd/dagger/checks.go`
- `cmd/dagger/generators.go`
- `cmd/dagger/module.go`
- `cmd/dagger/shell_commands.go`
- `cmd/dagger/shell_completion_test.go`
- `cmd/dagger/suite_test.go`

Restore shape from `workspace`:

- top-level workspace management commands
- explicit workspace binding in `client.Params.Workspace`
- target parsing for `call`, `functions`, `check`, and `generate`

Command tree source of truth:

- root registration in `workspace` branch `cmd/dagger/main.go`
- parser behavior in `cmd/dagger/workspace_target_args.go`

### Phase 6: Restore Migration Product Story

Goal:

- `dagger migrate` upgrades legacy repos into initialized workspaces coherently

Files:

- `core/workspace/migrate.go`
- `core/workspace/migrate_test.go`
- `cmd/dagger/workspace.go` (migrate command wiring)

Migration requirements:

- write `.dagger/config.toml`
- write `.dagger/lock` when migrated workspace modules have pinned refs
- relocate `source != "."` modules into workspace layout
- convert toolchains/blueprints into workspace modules
- preserve or rewrite path-based dependencies/includes as in `workspace`
- keep migration explicit

Pin-specific requirement:

- compat mode must continue to honor legacy pins directly without `.dagger/lock`
- migration must convert migrated blueprint/toolchain pins into workspace
  `modules.resolve` lock entries
- audit migrated dependency-pin handling from `workspace` before merge; do not assume
  it is correct without tests

### Phase 7: Restore Tests

Restore and rerun:

- `core/workspace/detect_test.go`
- `core/workspace/migrate_test.go`
- `core/workspace/lock_test.go`
- `core/schema/workspace_test.go`
- `core/schema/lockfile_test.go`
- `core/integration/workspace_test.go`
- `cmd/dagger/call_target_args_test.go`
- `cmd/dagger/checks_test.go`
- `cmd/dagger/functions_test.go`
- `cmd/dagger/generators_test.go`
- `cmd/dagger/engine_lock_test.go`
- workspace-related updates in:
  - `core/integration/engine_test.go`
  - `core/integration/module_call_test.go`
  - `core/integration/module_cli_test.go`
  - `core/integration/module_shell_test.go`
  - `core/integration/module_test.go`

PR B-specific test requirements:

- initialized workspaces load modules from config
- `dagger migrate` writes both config and lock state as intended
- explicit workspace targeting selects the requested workspace
- `update` mutates lock state without corrupting config
- PR A split buckets stay green after the porcelain is restored

### Phase 8: Regenerate Docs And SDKs

Only after the command/API surface is stable, regenerate:

- `docs/current_docs/reference/cli/index.mdx`
- `docs/docs-graphql/schema.graphqls`
- `docs/static/api/reference/index.html`
- PHP reference outputs
- `sdk/go/dagger.gen.go`
- `sdk/php/generated/Client.php`
- `sdk/php/generated/Workspace.php`
- `sdk/python/src/dagger/client/gen.py`
- `sdk/rust/crates/dagger-sdk/src/gen.rs`
- `sdk/typescript/src/api/client.gen.ts`

## Suggested Commit Sequence

1. `workspace: restore initialized config detection`
2. `workspace: restore modules.resolve lock substrate`
3. `engine: load initialized workspace modules from config`
4. `workspace: restore workspace mutation schema`
5. `cli: restore explicit workspace targeting`
6. `workspace: restore migrate and update UX`
7. `test: restore workspace porcelain coverage`
8. `docs: regenerate workspace porcelain references`
9. `sdk: regenerate workspace porcelain clients`

## Verification Checklist

- `go test ./core/workspace`
- `go test ./cmd/dagger`
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/schema ./engine/server ./core/integration`
- relevant `dagger call engine-dev test --pkg=...` slices for restored unit/integration areas
- `dagger check test-split:test-cli-engine`
- `dagger check test-split:test-call-and-shell`
- restored workspace-specific integration coverage passes

## No-Loss Ledger

Every `workspace-plumbing..workspace` restore must land in exactly one bucket:

1. Lands in PR B as-is
2. Lands in PR B selectively adapted
3. Stays deferred to PR C
4. Intentionally dropped

No unlabeled restores.
