# Workspace Foundation + Compatibility

## Status: In Progress (`workspace-plumbing`)

## Branch Contract

This branch exists to spin out the fast-trackable part of `workspace` into a separate
PR.

The target is not "zero UI". The target is "no new primary workspace UX":

- keep the engine/runtime foundation and backward-compat behavior
- preserve existing CWD-based flows for current commands
- defer explicit workspace authoring and targeting UX to a follow-up PR

This document is the running source of truth for the branch. Update it after every
substantive change so an interruption can be resumed from the filesystem alone.

The Workspace API path-contract rollout is complete. Its temporary tracker is archived
in
[workspace-api-rollout-tracker.md](/Users/shykes/git/github.com/dagger/dagger_workspace/hack/designs/done/workspace-api-rollout-tracker.md).
The canonical contract itself currently lives in the `workspace` PR description.

## Rebase Survival Notes

When `workspace` is eventually rebased onto `workspace-plumbing`, preserve the
verified behavior, not the current patch shape or commit topology.

Local fix threads that must be reconciled explicitly:

- `cd4c63ff1` `engine: preserve legacy toolchain customizations`
  - keep unless upstream already restores legacy `defaultPath`,
    `defaultAddress`, and `ignore` through the same compat/runtime path
  - post-rebase proof:
    `TestToolchain/TestToolchainsWithConfiguration/override_constructor_defaultPath_argument`
- `fec23805b` `core: restore legacy blueprint caller env defaults`
  - keep unless upstream already restores caller `.env` propagation for legacy
    blueprints/toolchains before `ModuleSource.asModule()`
  - post-rebase proof:
    `TestUserDefaults/TestLocalBlueprint/inner_envfile`
- `6c93ddba9` `core/schema: restore legacy generator include matching`
  - keep unless upstream already preserves single-generator-module
    `generate-*` matching on the workspace path
  - post-rebase proof:
    `TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple`
    and `TestGenerators/TestGeneratorsAsBlueprint/java/generate`
- `1bd6065c7` `engine: seed runtime module content cache`
  - keep unless upstream already seeds or replaces the
    content-digest lookup path used by `_contextDirectory`
  - this commit is easy to misclassify as a generator fix; it is broader than
    that and protects runtime-client contextual dir/file reloads

Commits that are bookkeeping, not behavior:

- docs-only ledger commits such as `a35930cd5`, `f972163b3`, `1025bf8d1`
  - do not preserve them for their own sake during rebase
  - preserve the decisions they record by rewriting this ledger as needed

Commits likely to be partially or wholly superseded by the upstream entrypoint
module rework:

- any local behavior that only exists to paper over root-entrypoint selection,
  CLI focus, `defaultModule`, `call`, `functions`, or shell routing
- after the cherry-pick/rebase, rerun the entrypoint-sensitive hold bucket
  before deciding whether any local workaround still belongs here

Mandatory reruns after the later `workspace` rebase:

- `TestToolchain/TestToolchainsWithConfiguration/override_constructor_defaultPath_argument`
- `TestUserDefaults/TestLocalBlueprint/inner_envfile`
- `TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple`
- `TestGenerators/TestGeneratorsAsBlueprint/java/generate`
- then the entrypoint-sensitive hold bucket after the upstream entrypoint
  design is in place

## Workspace API Contract Adoption

`workspace-plumbing` adopts the Workspace API path contract defined in `workspace`
PR [#11812](https://github.com/dagger/dagger/pull/11812).

For this branch, that means:

- `.` and relative paths are resolved from the workspace directory
- `/` and absolute paths are resolved from the workspace boundary
- `ws.path` and `ws.address` are the intended public metadata surface
- no public `ws.root` should be reintroduced here
- compat/runtime fixes in this branch must conform to that contract rather than
  bending the contract around legacy `defaultPath` behavior

Current implementation status:

- shared path-contract source commit on `workspace`:
  - `5e0b1e4a7` `workspace: adopt path contract`
- cherry-picked onto `workspace-plumbing`:
  - `bc8d8668e` `workspace: adopt path contract`
- two plumbing-specific cherry-pick repairs were needed before repo-root official
  generators would run:
  - restore the missing `strings` import in
    [workspace.go](/Users/shykes/git/github.com/dagger/dagger_workspace/core/schema/workspace.go)
  - remove `workspace`-branch-only config-state references from
    [session.go](/Users/shykes/git/github.com/dagger/dagger_workspace/engine/server/session.go)
- the public schema/SDK/docs surface has been regenerated on this branch through
  official repo-root `dagger generate -y ...` functions and trimmed back to the
  Workspace path-contract-related files only
- next compat/runtime fix under that contract:
  - the root-`/` sandbox bug was fixed in `bae60c5b2`
    (`engine: fix sandboxing under root slash`)
  - the targeted blueprint repro moved past the boundary failure on that fix
  - the next remaining targeted runtime bug is legacy toolchain argument
    customizations (`defaultPath`, `defaultAddress`, `ignore`) being dropped
    during compat loading; the fix must stay inside the existing
    workspace/session loader path

## Entrypoint Module Design

The canonical entrypoint-module design now lives in the `workspace` PR
description for [#11812](https://github.com/dagger/dagger/pull/11812).

Design target:

- one designated user entrypoint module at most
- entrypoint methods appear directly at Query root for all clients
- constructors and existing root fields win conflicts
- conflicting entrypoint proxies are skipped rather than shadowing root fields
- the namespaced module constructor remains the explicit escape hatch
- schema construction, not client-specific `defaultModule` logic, is the source
  of truth

Implications for `workspace-plumbing`:

- `Workspace.defaultModule` and CLI-side prefixing are temporary workarounds,
  not target behavior
- do not spend more implementation effort on client-specific root-entrypoint
  fixes that the upstream schema-level entrypoint design will replace
- when triaging remaining failures, separate:
  - legacy/runtime compatibility bugs that are still worth fixing here
  - root-entrypoint UX failures (`call`, `functions`, shell, and related
    `defaultModule` behavior) that are likely to be superseded by the upstream
    entrypoint-module work

## Why This Split

The earlier lockfile split candidate was technically possible only for boring
substrate. The meaningful behavior was still workspace-bound, so it was not a good
fast-track carveout.

This split is better because it tells one coherent story:

1. The engine understands workspaces and eligible legacy `dagger.json` projects.
2. Existing `dagger call/functions/check/generate` flows keep working from the current
   directory.
3. Compatibility behavior can be stress-tested before the final workspace UX lands.

This is closer to the earlier workspace API carveout: land the runtime foundation
first, then land the product UX on top.

The lockfile now has a clearer split too:

- PR A must not create or depend on `.dagger/lock`
- PR B owns the workspace-authored lockfile story, including `modules.resolve`
- PR C can later add generic non-workspace lockfile consumers such as
  lockfile-enabled `container.from`

## Scope

### PR A: Foundation + Compatibility (this branch)

Keep:

- engine-side workspace detection, loading, and binding for the current CWD
- compat-mode loading for eligible legacy `dagger.json`
- direct honoring of legacy `dagger.json` pins during compat loading, without
  creating `.dagger/lock`
- workspace injection, default-module selection, and related caching/defaults needed
  for existing runtime behavior
- the minimum CLI adaptations needed so existing CWD-based
  `call`/`functions`/`check`/`generate` flows keep working
- warnings/diagnostics needed so compat mode is understandable

### PR B: Deferred New Primary UX

Defer:

- explicit workspace targeting and positional workspace target arguments
- top-level workspace management UX
- workspace-centric authoring flows such as config management and new workspace-first
  install/update/init flows
- workspace-owned `.dagger/lock` support, including `modules.resolve` read/write
  behavior and migration-generated lockfiles
- remote workspace targeting UX
- `--workdir` repurposing/hiding and related targeting semantics
- most docs/release-note churn for the final workspace workflow

### PR C: Deferred Generic Lockfile Consumers

Defer:

- generic lockfile consumers that are not part of the workspace product story
- lockfile-enabled `container.from`

## Layering Model

`workspace-plumbing` is the runtime model:

- what the engine loads at connect time
- how current-directory commands resolve modules
- how legacy `dagger.json` is adapted into generic module-loading inputs
- how workspace binding participates in identity and caching

`workspace-porcelain` is the author-intent model:

- what state is persisted in the repo
- how users target and manage workspaces
- when repo files are allowed to change
- how migration and locking become explicit and legible

Generic lockfile consumers remain follow-up work. They do not define the workspace
product boundary.

## Architectural Seam

The main seam for this split is connect-time module loading in
`engine/server/session.go`.

- PR A should be reviewed primarily in terms of "which modules are loaded for this
  session, and why", not in terms of Cobra command shape
- CLI changes in this branch are support work for existing current-directory flows,
  not the primary architecture
- if a change forces reviewers to reason about repo-owned workspace state, it
  belongs in PR B instead

## Legacy Adapter Rule

Legacy support in PR A is an adapter, not a parallel product model.

- parse legacy-only shape once during compat loading
- translate it into generic module-loading inputs
- stop leaking legacy concepts after that chokepoint

This keeps legacy compatibility localized instead of turning it into a second set
of semantics spread across the codebase

## Legacy Conversion Contract

For legacy `dagger.json` projects, the design target is:

- detect one legacy project shape
- convert that shape into one workspace module set
- then load that module set through the normal module loader

That converted module set should include:

- the legacy root module itself, when the legacy project defines its own module via
  `sdk` / `source`
- the legacy blueprint, if any
- the legacy toolchains, if any

Important constraint:

- generic implicit CWD-module loading is for non-legacy fallback only
- it must not be the mechanism by which the legacy root module enters the workspace

Current design debt:

- both `workspace` and `workspace-plumbing` currently still use the split shortcut
- legacy blueprint/toolchains come from compat extraction
- the legacy root module still comes from generic implicit CWD-module loading

History note:

- `workspace` briefly had the cleaner detect-time conversion shape in
  `4e92b04b0` and `a7982a72b`
- the current split shortcut was restored in `dab41cfbe`

This shortcut is now considered temporary implementation debt, not target behavior.

## Design Constraints

- No silent auto-conversion of legacy projects on connect.
- `dagger migrate`, if kept, stays explicit.
- If a cut is ambiguous, bias toward preserving old CWD-based behavior and deferring
  the new surface area.
- Keep the split crisp. If a feature makes reviewers reason about the whole workspace
  product, it belongs in the follow-up PR.
- PR A must not silently create `.dagger/lock` in a user's repo.
- PR A should honor legacy `dagger.json` pins directly from compat parsing rather
  than routing them through the workspace lockfile.
- Keep `.dagger/config.toml` runtime support and `dagger migrate` in the same PR.
  Splitting those apart would make migration self-contradictory.
- If `.dagger/config.toml` and `dagger migrate` are fully hidden until the follow-up
  PR, it is coherent to move both out of the plumbing PR together.
- In that tighter split, the plumbing PR covers legacy `dagger.json` compat plus
  current-CWD workspace bones, while the porcelain PR adds initialized-workspace
  support (`.dagger/config.toml`), `dagger migrate`, and all explicit workspace
  authoring/targeting UX.
- The workspace lockfile follows the same boundary: PR B owns the workspace-authored
  lock semantics, while generic lockfile consumers are follow-up work.

## PR A Success Criteria

PR A is successful when:

- existing current-directory `call` / `functions` / `check` / `generate` flows stay
  coherent in standalone, workspace-shaped, and eligible legacy repos
- legacy `dagger.json` pins are honored directly without introducing
  repo-authored `.dagger/lock`
- workspace binding, default-module selection, and related cache identity stay part
  of runtime truth
- no new primary workspace authoring or targeting UX is introduced
- no repo-owned state appears or mutates as a side effect of the compat path

## PR A Test Priorities

The highest-value coverage for this branch is:

- legacy pin honor in compat mode, without `.dagger/lock`
- multi-module default-module selection
- sibling workspace module entrypoints under existing commands
- workspace binding and cache identity
- current-directory `call` / `functions` / `check` / `generate` behavior

## Foundation Retained From `workspace`

The branch still keeps the compat work already landed on `workspace`:

- legacy toolchains are handled at workspace loading time instead of through
  module-level toolchain-aware loading
- eligible legacy `dagger.json` projects load without a migration gate
- structural warnings explain when Dagger is inferring workspace behavior
- the explicit migration path is deferred to PR B with initialized-workspace
  support

### Compat Behavior

| Legacy feature | PR A load-time behavior | PR B migration behavior |
|----------------|-------------------------|------------------------|
| `toolchains[]` | Extract as workspace-level modules | Convert to `.dagger/config.toml` entries |
| `source != "."` | Keep working through module loading | Relocate to `.dagger/modules/` |
| `pin` on blueprint/toolchain refs | Pass directly to module loading in compat mode | Convert to `.dagger/lock` `modules.resolve` entries |

## Rollback Boundary for This Branch

The working rollback boundary is:

- keep engine semantics and compat behavior underneath existing commands
- remove explicit workspace-targeting from CLI command surfaces
- remove or defer most new top-level workspace management commands
- preserve only the small admin/debug surface needed to keep compat mode legible

Mechanically, the first rollback pass is concentrated in `cmd/dagger/`:

- `main.go`
- `module.go`
- `workspace.go`
- `call.go`
- `functions.go`
- `checks.go`
- `generators.go`
- `workspace_target_args.go`
- `module_inspect.go`

## Checkpoint Log

### 2026-03-10: Recovered Interrupted Session

Recovered context from terminal scrollback:

1. A lockfile fast-track split was considered and rejected for product purposes. Only
   the substrate was separable, and that was not worth the surgery.
2. A better carveout is "workspace foundation + compatibility" without the new primary
   workspace UX.
3. The intended framing is not "no UI", but "no new primary UX". Existing CWD-based
   behavior stays; new workspace authoring/targeting flows move out.
4. `dagger migrate` should stay in the spinout. Silent project rewriting should not.
5. The interrupted implementation work had already created the branch
   `workspace-foundation-compat` from the `workspace` tip and started mapping the CLI
   rollback against `main`.

At interruption time, the next concrete step was:

- diff `cmd/dagger/` against `main`
- remove deferred workspace-targeting and management UX
- keep the minimum CLI support required for existing CWD-based flows

### 2026-03-10: Current Starting Point

- Branch: `workspace-foundation-compat`
- Branch base at recovery time: `89af48434`
- Worktree state at recovery time: clean
- Immediate next task: implement the CLI rollback boundary above, then record the
  exact kept/deferred commands here before moving on

### 2026-03-10: CLI Rollback Pass 1

Implemented the first CLI rollback pass to match the branch contract:

- removed the top-level workspace management surface from this branch
- removed explicit workspace-target syntax from `call`, `functions`, `check`, and
  `generate`
- kept workspace-aware loading underneath current-CWD execution flows
- kept `dagger migrate` as the explicit compat escape hatch
- rewired root command registration and shell builtins back to standalone
  `init`/`install`/`update` semantics

Concrete code changes in this pass:

- deleted `cmd/dagger/workspace.go`
- added `cmd/dagger/migrate.go`
- deleted `cmd/dagger/workspace_target_args.go`
- deleted the explicit-target parser tests
- rewired [main.go](/Users/shykes/git/github.com/dagger/dagger_workspace/cmd/dagger/main.go) to expose:
  `init`, `install`, `update`, `uninstall`, `develop`, `migrate`
- kept workspace-aware current-directory loading in:
  [call.go](/Users/shykes/git/github.com/dagger/dagger_workspace/cmd/dagger/call.go),
  [checks.go](/Users/shykes/git/github.com/dagger/dagger_workspace/cmd/dagger/checks.go),
  [generators.go](/Users/shykes/git/github.com/dagger/dagger_workspace/cmd/dagger/generators.go),
  [functions.go](/Users/shykes/git/github.com/dagger/dagger_workspace/cmd/dagger/functions.go),
  [module_inspect.go](/Users/shykes/git/github.com/dagger/dagger_workspace/cmd/dagger/module_inspect.go)

Intentional limitation in this pass:

- Restored top-level `init`/`update` without the old blueprint-specific behavior.
  On the current workspace branch, the old `ModuleSource.WithBlueprint` and
  `WithUpdateBlueprint` API is no longer available. Rather than re-inventing a new
  blueprint authoring path in this fast-track branch, this pass keeps standalone
  module authoring and defers blueprint-specific authoring UX.

Verification after this pass:

- `go build ./cmd/dagger` passes
- `go test -run 'TestOriginToPath|TestParseGit' ./cmd/dagger` passes

Follow-up fix in the same pass:

- `cmd/dagger/suite_test.go` no longer imports `internal/testutil`
- the tracing span helper was inlined locally so `cmd/dagger` tests stop importing
  `core` through `internal/testutil/query.go`
- the leftover `ParseTargetArgs` explicit-target plumbing was removed from
  [functions.go](/Users/shykes/git/github.com/dagger/dagger_workspace/cmd/dagger/functions.go)

Current verification caveat:

- Full `go test ./cmd/dagger` no longer dies immediately at compile time, but it is a
  slower-running CLI test target and was not used as the blocking verification signal
  for this checkpoint

Current blocker / next step:

- Audit remaining test and integration expectations for deferred commands such as
  `workspace`, explicit workspace targets, and workspace-first authoring flows
- If needed, do a follow-up prune/update pass for docs and integration tests after the
  CLI cut is stable

### 2026-03-10: Cut-Line Re-evaluation

Re-evaluated the `.dagger/config.toml` boundary under a stronger rollout assumption:
if PR A hides initialized-workspace support and `dagger migrate` from all users, then
there are no external repos in the wild depending on `.dagger/config.toml` between
PR A and PR B.

That changes the calculus:

- the earlier "do not disable config parsing" argument no longer holds on release
  compatibility grounds
- the engine already has a coherent no-config compat path:
  `workspace.Detect` can return an uninitialized workspace, then
  `detectAndLoadWorkspaceWithRootfs` extracts legacy toolchains/blueprints from nearby
  `dagger.json` and still sets a default module for current-directory execution
- therefore, a narrower PR A is viable if we move `.dagger/config.toml` parsing and
  `dagger migrate` together into PR B

If we adopt that narrower split, this branch needs one more rollback pass:

- remove read-time `.dagger/config.toml` support from the user-facing plumbing story
- move `dagger migrate` out of PR A with it
- keep legacy `dagger.json` compat and current-directory workspace loading

Concrete rollback set for that pass:

- `cmd/dagger/main.go`: stop registering `migrate`
- `cmd/dagger/migrate.go`: remove from PR A
- `core/workspace/detect.go`: stop treating `.dagger/config.toml` as an initialized
  workspace marker; detect only current-CWD workspace bones needed for compat/runtime
- `engine/server/session.go`: remove the `ws.Config` module-loading path and keep only
  legacy toolchains, legacy blueprint, and the implicit CWD module
- `engine/server/session.go`: stop telling users to run `dagger migrate` in compat
  warnings during PR A
- `core/workspace.go`: drop config-facing workspace fields from the public object if
  they are only meaningful for initialized workspaces
- `core/schema/workspace.go`: keep `currentWorkspace`, `directory`, `file`, `findUp`,
  `checks`, and `generators`; defer config-backed/mutating methods
  (`init`, `install`, `moduleInit`, `configRead`, `configWrite`, `update`)
- tests: update detection tests, remove migrate-only tests, and defer
  initialized-workspace integration coverage with the rest of PR B

### 2026-03-10: Is The Narrower Split Still Worth It?

Yes, but only if the goal is review and landing parallelism for the runtime bones,
not early exposure of initialized-workspace behavior.

Why it can still be worth it:

- PR A becomes a smaller and cleaner story: existing CWD commands gain the workspace
  runtime model plus legacy compat, without asking reviewers to reason about config
  files, migration, or final workspace UX.
- PR B becomes the concentrated user-facing workspace PR: initialized workspaces,
  migration, config, explicit targeting, and authoring flows land together.
- That boundary reduces hidden surface area in PR A and lowers the chance of merging
  half of a user story.

When it stops being worth it:

- if the rollback needed to exclude `.dagger/config.toml` and `migrate` is nearly as
  large as just finishing the full workspace PR
- if PR A no longer has enough product value to justify its own review and release
  overhead

Current judgment:

- still worth doing if we keep PR A tight and deliberately mechanical
- not worth doing if the branch turns into prolonged surgery on partially-hidden UX

### 2026-03-10: No-Loss Tracking Rule

To make sure nothing falls through the cracks, every workspace-branch change must be
placed in exactly one bucket:

1. Lands in PR A
2. Lands in PR B
3. Intentionally dropped

No unlabeled code paths, commands, tests, or docs.

Practical guardrails:

- keep this design doc as the authoritative scope ledger
- for each deferred surface, record both the feature and its destination PR
- before deleting or hiding code, record whether it is deferred or abandoned
- before opening PR A, audit the workspace diff and bucket every touched area
- before opening PR B, audit the remaining workspace-only diff against this ledger

Initial deferred inventory for PR B:

- read-time initialized-workspace support from `.dagger/config.toml`
- `dagger migrate`
- workspace-owned `.dagger/lock` substrate and serialization
- `modules.resolve` lockfile read/write behavior
- explicit workspace targeting syntax
- top-level workspace management commands
- workspace-first authoring flows (`init` / `install` / `update` / config)
- remote workspace targeting UX
- docs and tests that describe or depend on the deferred workspace UX

Initial deferred inventory for PR C:

- lockfile-enabled `container.from`
- generic lookup-lock helpers only needed by non-workspace lock consumers

### 2026-03-10: Narrower Split Rollback Pass

Applied the narrower PR A rollback to exclude initialized-workspace support and
`dagger migrate`, while keeping legacy `dagger.json` compat and current-CWD
workspace execution.

Concrete code changes in this pass:

- `core/workspace/detect.go` no longer reads or parses `.dagger/config.toml`
  during detection; it now detects workspace bones from `.git` or falls back to
  the current directory
- `engine/server/session.go` no longer loads modules from `ws.Config`; the
  pending workspace module set now comes only from:
  - legacy toolchains extracted from nearby `dagger.json`
  - legacy blueprint extracted from nearby `dagger.json`
  - the implicit CWD module
- `engine/server/session.go` no longer warns users to run `dagger migrate`
- `cmd/dagger/main.go` no longer registers `migrate`
- `core/schema/workspace.go` no longer registers config-backed or mutating
  workspace methods (`init`, `install`, `moduleInit`, `configRead`,
  `configWrite`, `update`)
- `core/workspace/config.go` was removed from PR A
- `core/workspace/migrate.go` and `core/workspace/migrate_test.go` were removed
  from PR A

Supporting cleanup:

- `core/workspace.go` keeps initialized-workspace fields only as internal state
  so deferred helpers still compile; they are no longer part of the public
  GraphQL surface for this split
- `core/workspace/detect_test.go` now tests the no-config detection path

Intentional limitation of this pass:

- initialized-workspace behavior is now deferred consistently, but the
  workspace-shaped execution model still lands under existing command names,
  so PR A still changes semantics before PR B adds the explicit workspace
  control surface

Verification after this pass:

- `go test ./core/workspace` passes
- `go test -run 'TestOriginToPath|TestParseGit|TestWorkspaceLoadLocation' ./cmd/dagger` passes
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/schema` passes
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./engine/server` passes
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/integration` passes

Verification caveat:

- native `go test ./core/schema` / `./engine/server` on this macOS host still
  runs into the known Linux-only engine dependency/build-constraint mismatch, so
  compile-only Linux-target checks were used there
- native `go test ./core/integration` hits the same Linux-only engine
  dependency/build-constraint mismatch on this macOS host

Remaining follow-up:

- `core/integration/workspace_test.go` is now explicitly deferred from PR A via
  a top-level `t.Skip`, because the suite is dominated by initialized-workspace
  and migration scenarios
- audit user-facing docs, especially
  `docs/current_docs/reference/cli/index.mdx`, to remove workspace/migrate
  surfaces that no longer exist in PR A

### 2026-03-10: CLI Reference Audit

Next doc step is to regenerate `docs/current_docs/reference/cli/index.mdx` from
the current branch command tree instead of hand-editing the stale generated file.

Confirmed generation path:

- `toolchains/cli-dev/main.go` exposes `reference(frontmatter,
  includeExperimental)` and internally runs `go run ./cmd/dagger gen`
- this is the right way to make the CLI reference match the PR A command
  surface after removing `migrate` and the workspace management commands

### 2026-03-10: CLI Reference Regenerated

Regenerated `docs/current_docs/reference/cli/index.mdx` from the current branch
command tree using the documented generator path:

- `dagger call -m ./toolchains/cli-dev reference --include-experimental --frontmatter=... export --path docs/current_docs/reference/cli/index.mdx`

Confirmed outcomes:

- `dagger workspace` and `dagger migrate` are no longer documented
- root `init`, `install`, and `update` now describe the restored
  standalone/module behavior
- the generated reference does not show `--workdir`, and that matches the CLI:
  `installGlobalFlags` still defines it in `cmd/dagger/main.go` but immediately
  hides it with `flags.MarkHidden("workdir")`

Result:

- the stale generated CLI reference no longer leaks deferred PR B workspace
  surfaces into PR A

### 2026-03-10: Generated API Reference Audit

After the CLI reference was fixed, the next stale surface showed up in the
generated API docs:

- `docs/docs-graphql/schema.graphqls` still describes initialized-workspace
  fields such as workspace init and config-backed state that PR A removed

Planned fix path:

- use `toolchains/docs-dev` as the narrowest supported generator entrypoint for
  the generated GraphQL schema and static API reference
- preview the `references` changeset before exporting it, so PR A only picks up
  the generated doc updates that actually reflect the narrowed schema

### 2026-03-10: Generated API Reference Regenerated

Regenerated the generated GraphQL schema and static API reference to match the
post-rollback `Workspace` type:

- exported `toolchains/engine-dev graphql-schema` directly to
  `docs/docs-graphql/schema.graphqls` to confirm the exact schema delta first
- then applied `dagger -s call -m ./toolchains/docs-dev references export --path .`
  so the generated API HTML caught up to the same schema

Confirmed outcomes:

- `docs/docs-graphql/schema.graphqls` no longer documents
  `configPath`, `configRead`, `configWrite`, `hasConfig`, `init`,
  `initialized`, `install`, or `moduleInit` on `Workspace`
- `docs/static/api/reference/index.html` dropped the same initialized-workspace
  fields and now documents `defaultModule` instead
- the generated docs churn stayed small and localized to the expected files

Result:

- PR A no longer leaks deferred initialized-workspace GraphQL surface through the
  generated API reference

### 2026-03-10: Generated SDK Surface Audit

After fixing the generated GraphQL/API docs, the next consistency gap was in the
generated clients:

- Go, TypeScript, Python, Rust, and PHP generated clients still exposed removed
  `Workspace` methods such as `configPath`, `configRead`, `configWrite`,
  `hasConfig`, `initialized`, and `moduleInit`
- the static PHP reference under `docs/static/reference/php` still documented the
  same stale methods

Planned fix path:

- regenerate Go and TypeScript via their normal codegen path
- regenerate Python, Rust, and PHP via their SDK toolchains
- let PHP own its generated static docs at the same time, since those docs are
  derived from the generated PHP client

### 2026-03-10: Generated SDK Surfaces Regenerated

Regenerated the schema-derived client surfaces after the `Workspace` rollback:

- Go via `dagger -s call -m ./toolchains/go-sdk-dev generate export --path .`
- TypeScript via
  `dagger -s call -m ./toolchains/typescript-sdk-dev client-library export --path .`
- Python via
  `dagger -s call -m ./toolchains/python-sdk-dev client-library export --path .`
- Rust via
  `dagger -s call -m ./toolchains/rust-sdk-dev apiclient export --path .`
- PHP via
  `dagger -s call -m ./toolchains/php-sdk-dev api export --path .`

Confirmed outcomes:

- Go, TypeScript, Python, Rust, and PHP generated clients no longer expose
  `Workspace` config/init/install mutation surface from PR B
- `defaultModule` remains in the generated clients where expected
- the PHP static reference was refreshed at the same time, including the
  cross-linked index/search assets that point at `Workspace`
- a repo-wide search no longer finds the removed `Workspace` methods in the
  generated SDK or published reference surfaces; only unrelated uses of the word
  "initialized" remain

Result:

- PR A's schema rollback is now reflected consistently across generated clients
  and published API references, not just in engine/schema code

### 2026-03-10: Generated SDK Verification

Post-regeneration verification:

- `go test ./...` in `sdk/go` passed
- `python3 -m py_compile sdk/python/src/dagger/client/gen.py` passed
- `cargo check -p dagger-sdk --manifest-path sdk/rust/Cargo.toml` passed
  with one pre-existing dead-code warning in `crates/dagger-sdk/src/core/session.rs`
- the TypeScript toolchain generation path completed successfully, including its
  internal `yarn install` and `eslint --fix` steps
- the PHP toolchain generation path completed successfully and refreshed both the
  generated client and Doctum output

Verification limitation:

- no local `php` binary is available on this host, so there was no extra host-side
  `php -l` lint step after the PHP toolchain run

### 2026-03-10: `cmd/dagger` Verification Cleanup

Tightened two `cmd/dagger` tests so local PR-prep verification is not blocked by
host-environment drift unrelated to the workspace foundation changes.

Concrete fixes:

- `cmd/dagger/cloud_test.go` now lets callers override `HOME` / `XDG_CONFIG_HOME`,
  and `TestCloudEngineUnauth` uses a temp home so cached real cloud credentials do
  not invalidate the unauthenticated expectation
- `cmd/dagger/shell_completion_test.go` now checks that the connected host
  `dagger` engine exposes the schema features required by the embedded shell
  introspection query (`Function.sourceModuleName` and
  `currentTypeDefs(includeCore:)`); if the host install is older, the test skips
  instead of failing as a false branch regression

Verification after these fixes:

- `go test ./cmd/dagger -run TestCloudEngineUnauth` passes
- `go test ./cmd/dagger -run TestDaggerCMD/TestShellAutocomplete` passes
- `go test ./cmd/dagger` passes

### 2026-03-11: Split-Suite Init Alignment

The first Linux split-suite rerun on `workspace-plumbing` showed that the
workspace-focused bucket was already green, but the `test-cli-engine` and
`test-call-and-shell` buckets still contained pre-plumbing setup that invoked
`dagger module init`.

Concrete follow-up:

- `core/integration/engine_test.go` now scaffolds version-compat and dagql-cache
  scenarios with the branch's top-level `dagger init` command
- `core/integration/module_call_test.go` now uses `dagger init` consistently for
  module setup, including host-side helpers and nested exec coverage, so the
  `TestCall` bucket exercises argument handling instead of failing in setup

Verification intent after this alignment:

- rerun `dagger check test-split:test-cli-engine`
- rerun `dagger check test-split:test-call-and-shell`
- rerun targeted `engine-dev test` cases if either split still fails, to keep the
  next checkpoint isolated and explainable

### 2026-03-11: Split-Suite Dependency Command Alignment

After the `init` cleanup, the remaining red cases in the split-covered
integration suite were still using the old grouped dependency-management
commands.

Concrete follow-up:

- `core/integration/module_call_test.go` now drives dependency setup through the
  top-level `dagger install` command for local-path and remote-ref `TestByName`
  coverage
- `core/integration/module_cli_test.go` now uses top-level `dagger install` and
  `dagger update` for the CLI scenarios that are part of the `test-cli-engine`
  and `test-call-and-shell` buckets

Verification intent after this alignment:

- rerun targeted `TestCall/TestByName`
- rerun `dagger check test-split:test-call-and-shell`
- rerun `dagger check test-split:test-cli-engine`

### 2026-03-11: Split-Suite `@` Path Init Alignment

The last red case in the targeted `TestCall/TestByName` rerun was not a product
regression. It was one remaining pre-plumbing `init` call that still passed the
module name positionally instead of via `--name`.

Concrete follow-up:

- `core/integration/module_call_test.go` now initializes the `local ref with @`
  fixture with `dagger init --name=mod-a test@test`, which matches the current
  top-level `init [path]` contract while preserving coverage for local module
  paths that contain `@`

Verification intent after this alignment:

- rerun targeted `TestCall/TestByName/local_ref_with_@`
- rerun `dagger check test-split:test-call-and-shell`
- rerun `dagger check test-split:test-cli-engine`

### 2026-03-11: Split-Suite Shared Shell Setup Alignment

Once the split definitions were checked directly, it was clear that
`test-call-and-shell` still exercised shared shell/module setup that had not
been updated by the earlier targeted `TestCall` fixes.

Concrete follow-up:

- `core/integration/module_test.go` now builds generic module fixtures with the
  current top-level `dagger init --name=... [path]` contract instead of the old
  grouped `dagger module init name path` form
- `core/integration/module_shell_test.go` now uses top-level `dagger install`
  and `dagger init --name=...` in the direct `TestShell` setup that was still
  bypassing the shared helper

Verification intent after this alignment:

- rerun `dagger check test-split:test-call-and-shell`
- rerun targeted `TestShell` cases if the split still reports shell-specific
  failures

### 2026-03-11: Split-Suite CLI Init Surface Alignment

`test-cli-engine` includes the full `TestCLI` suite, and that file was still
encoding the old grouped `dagger module init` surface across its init, develop,
install, and update coverage.

Concrete follow-up:

- `core/integration/module_cli_test.go` now uses the branch's top-level
  `dagger init` command consistently, while preserving the existing test
  semantics around source-root inference, absolute paths, nested modules, and
  develop/install flows
- the CLI suite did not need additional helper shims here; the changes are
  mostly direct command-surface alignment so the tests still describe the
  current UX explicitly

Verification intent after this alignment:

- rerun `TestCLI` under `engine-dev test`
- rerun `dagger check test-split:test-cli-engine`
- rerun `dagger check test-split:test-call-and-shell`

### 2026-03-11: Split-Suite Root-Query Call Guard

Once the stale setup churn was reduced, `TestCall/TestErrNoModule` was still
failing for a real CLI reason rather than an outdated test fixture. With no
default module selected, `dagger call` was reaching the synthetic `Query`
constructor and then attempting to subselect `id`, which produced a GraphQL
validation error instead of a user-facing CLI response.

Concrete follow-up:

- `cmd/dagger/functions.go` now intercepts `dagger call` at the workspace root
  when no function was selected and the current main object is `Query`
- if workspace module entrypoints are available, the CLI now shows help instead
  of executing an invalid root query
- if no module entrypoints are available, the CLI returns the existing
  `module not found` error again, which preserves the current integration
  expectation for empty workdirs

Verification intent after this guard:

- rerun targeted `TestCall/TestErrNoModule`
- rerun `dagger check test-split:test-call-and-shell`

### 2026-03-11: Develop Source Ordering Fix

The remaining `TestDaggerDevelop` failures were a real CLI regression rather
than stale split-suite expectations. `dagger develop --sdk=... --source=...`
was loading the module, applying `WithSDK`, and only then validating the
requested source path. After the workspace plumbing changes, `WithSDK` now
defaults an unset source path to the module root, so the later validation saw a
false conflict against `"."` and rejected legitimate first-time source
selection.

Concrete follow-up:

- `cmd/dagger/module.go` now reads the module's configured source path before
  applying `WithSDK`
- `develop` now validates `--source` against that configured value rather than
  the post-`WithSDK` implicit default
- the command now uses a local requested-source variable instead of mutating the
  global flag state while iterating modules

Verification intent after this fix:

- rerun targeted `TestCLI/TestDaggerDevelop`
- rerun `dagger check test-split:test-cli-engine`

### 2026-03-11: Split-Suite Expectation Tail Cleanup

After the command-surface alignment and root-query guard, the remaining split
red cases were all narrow expectation mismatches.

Concrete follow-up:

- `core/integration/module_cli_test.go` no longer pre-sets the root module's
  source path in the `develop --source ...` coverage, so those tests exercise
  the engine's source-root rewriting instead of failing the precondition
- the eager-runtime install assertion now matches the current dependency-scoped
  error wording (`failed to install dependency`)
- the no-module `dagger functions` coverage now asserts the current workspace
  UX explicitly: an empty function table rather than a `module not found` error,
  while normalizing the CLI's ANSI-styled header output first
- `core/integration/module_call_test.go` now treats `dagger call conflict
  --help` as a help-rendering path instead of expecting a flag-registration
  failure

Verification intent after this cleanup:

- rerun targeted `TestCLI/TestDaggerDevelop`
- rerun targeted `TestCLI/TestDaggerInstall/install_with_eager-runtime`
- rerun targeted `TestCLI/TestCLIFunctions`
- rerun targeted `TestCall/TestHelp`
- rerun `dagger check test-split:test-cli-engine`
- rerun `dagger check test-split:test-call-and-shell`

### 2026-03-11: Remove Deferred Workspace-Only Integration Suite

To keep PR A scoped to workspace plumbing rather than deferred workspace
porcelain, the branch now drops the skipped full-workspace integration suite
instead of carrying it forward as dead weight.

Concrete follow-up:

- removed `core/integration/workspace_test.go` from this branch
- kept the smaller workspace-adjacent coverage that still protects PR A behavior,
  including `cmd/dagger/module_inspect_test.go` for workspace load-location
  selection

Deferred coverage ledger for PR B restore:

- blueprint modules that take `Workspace` and access workspace-root files
- find-up behavior that stops at the workspace root
- workspace directory/file exposure, subdirectory behavior, and path-traversal
  protection
- workspace config read/write/default-value behavior
- workspace update behavior, especially lockfile-vs-config mutation rules
- workspace gitignore and content-addressed caching behavior
- `dagger migrate` coverage for local and non-local sources, lock pins, and
  summary output
- nested-module precedence inside a workspace and multi-module
  `functions`/`call` behavior
- module init behavior with and without an existing workspace config

Verification intent after this cleanup:

- keep the PR A split buckets green without carrying deferred PR B-only suite
  inventory in-tree

Verification after this cleanup:

- `go test ./cmd/dagger -run TestWorkspaceLoadLocation -count=1` passes
- previously rerun Linux `dagger check test-split:test-cli-engine` remains the
  relevant broad integration check for the retained CLI surface

### 2026-03-11: Lockfile Scope Re-Decision

Rechecked the original `workspace` branch against the actual production
lockfile call sites instead of the generic API shape.

Concrete findings:

- the lockfile format is namespaced and generic, but the original branch only
  wires up two real consumers:
  - workspace `modules.resolve`
  - lockfile-enabled `container.from`
- legacy `dagger.json` compat does not need `.dagger/lock` to honor pins
  correctly; the compat path already parses blueprint/toolchain pins directly
  from `dagger.json` and passes them into module loading

Scope decision from that audit:

- PR A keeps legacy pin compat, but does not create or depend on
  `.dagger/lock`
- PR B owns the workspace-authored lockfile story, including substrate,
  `modules.resolve`, and migrate-generated lockfiles
- PR C owns generic non-workspace lockfile consumers such as
  lockfile-enabled `container.from`

### 2026-03-11: Lockfile Surgery Landed

Implemented the agreed PR A lockfile removal so the branch contract now matches
the tree.

Concrete code changes in this pass:

- removed CLI/engine lock-mode plumbing from PR A, including the global
  `--lock` flag, client metadata propagation, and the old lock-mode tests
- removed the workspace lockfile substrate from PR A:
  `core/workspace/lock.go`, `core/schema/lockfile.go`, and `util/lockfile/`
- removed lockfile-enabled `container.from` behavior from PR A
- changed legacy compat loading to pass blueprint/toolchain pins directly as
  module `refPin` during session loading instead of routing through
  `.dagger/lock`
- removed the PR A lockfile-preservation integration case and renamed the
  leftover session tests to match their new non-lockfile scope
- removed stale generated CLI reference entries for `--lock`
- added focused coverage for legacy pin parsing and compat pending-module
  construction

Resulting scope ledger:

- PR A keeps direct legacy `dagger.json` pin compat and no `.dagger/lock`
- PR B keeps migration of legacy pins into workspace-authored lock entries
- PR C keeps lockfile-enabled `container.from`

Verification after this pass:

- `go test ./cmd/dagger ./core/workspace` passes
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/schema ./engine/server ./core/integration` passes
- `dagger call engine-dev test --pkg=./engine/server --run='TestPendingLegacyModule|TestIsSameModuleReference|TestEnsureWorkspaceLoadedInheritsParentWorkspace|TestEnsureWorkspaceLoadedKeepsExistingWorkspaceBinding|TestWorkspaceBindingMode|TestParseWorkspaceRemoteRef|TestGatherModuleLoadRequests|TestModuleLoadErr'` passes
- `dagger check test-split:test-cli-engine` passes
- `dagger check test-split:test-call-and-shell` passes

### 2026-03-12: Main-First Test Baseline Audit

Re-audited the preserved test changes with a stricter branch contract:
`workspace-plumbing` is supposed to preserve `main` behavior via workspace-aware
runtime loading, so test churn should default back to `main` unless a test is
specifically covering a new plumbing behavior.

Concrete follow-up in this pass:

- restored the changed `core/integration/` suites to their `main` versions
  instead of preserving workspace-branch command-surface rewrites
- restored `core/integration/toolchain_test.go` and
  `core/integration/workspace_test.go`, which had been dropped from this branch
- treated any newly exposed failures after that restore as compatibility
  regressions to investigate in code, not as stale tests to rewrite around

Scope rule after this audit:

- keep branch-specific coverage only where the test is explicitly about new
  workspace-plumbing behavior such as workspace detection, legacy compat
  loading, workspace binding, sibling module entrypoints, or related cache
  identity
- revert generic module, blueprint, toolchain, and CLI coverage to the `main`
  implementation by default

Verification intent after this pass:

- rerun targeted test packages after each restore batch to identify actual
  compat regressions instead of carrying workspace-era test updates forward

### 2026-03-12: Main-First `cmd/dagger` Test Cleanup

Applied the same audit rule to the remaining generic `cmd/dagger` test churn.

Concrete follow-up in this pass:

- restored `cmd/dagger/cloud_test.go`,
  `cmd/dagger/shell_completion_test.go`, and
  `cmd/dagger/suite_test.go` to their `main` versions
- kept only the focused new unit coverage that is directly about
  workspace-plumbing behavior, such as workspace-aware function selection and
  workspace load-location handling

Verification after this pass:

- direct `go test ./cmd/dagger ...` on macOS still hits the pre-existing
  `engine/buildkit` platform compile failure
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./cmd/dagger`
  passes

### 2026-03-12: Workspace-Branch Test Transplant Audit

Audited the original `workspace` test delta against the narrower
`workspace-plumbing` scope after restoring the generic suites to a `main`
baseline.

Concrete findings from that audit:

- most workspace-only additions are still out of scope for this branch:
  explicit workspace target parsing, lockfile behavior, `dagger migrate`,
  workspace config/update flows, and module-init behavior that depends on an
  initialized workspace
- the focused plumbing unit coverage already kept on this branch is the right
  core set from the original `workspace` diff:
  workspace-aware function initialization, workspace load-location selection,
  host find-up behavior, no-config workspace detection, legacy pin parsing,
  workspace include matching, and session-side workspace binding/module loading
- one obvious end-to-end transplant candidate remains in the old
  `workspace`-only integration suite:
  `TestBlueprintFunctionsIncludesOtherModules` in
  `core/integration/workspace_test.go`, which exercises sibling workspace
  module entrypoints under existing `dagger functions` / `dagger call`
  commands
- `TestNestedModuleBeneathWorkspace` from the same file is only a partial fit:
  it covers nested standalone-module precedence and multi-module
  `functions`/`call` behavior, but that area is still listed in the deferred
  coverage ledger for the follow-up branch

Current transplant stance:

- keep the existing plumbing unit tests already carried into this branch
- consider porting the sibling-entrypoint integration case if we want explicit
  end-to-end coverage for that retained CLI behavior
- keep the nested-precedence case deferred unless the branch scope is widened

### 2026-03-12: Plumbing Test Harness Build Fix

While rerunning the retained plumbing unit tests, `./core` was blocked by a
stale test mock rather than a runtime compat failure.

Concrete fix in this pass:

- updated `core/telemetry_test.go` so its `mockServer` matches the current
  `core.Server` interface after `CurrentServedDeps` switched from `*ModDeps`
  to `*ServedMods`
- added the no-op `CurrentWorkspace` stub required by the newer interface

Verification after this pass:

- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core`
  passes again
- the original `./core` plumbing test target can now proceed past package
  build instead of failing in `core/telemetry_test.go`

### 2026-03-12: Changeset Git Helper CWD Fix

While sweeping non-integration tests, the Linux-backed `./core` package exposed
one real runtime bug in the new changeset helper tests rather than a workspace
plumbing regression.

Root cause:

- `compareDirectories` and `directoriesAreIdentical` shell out to
  `git diff --no-index`
- those helpers inherited the process cwd
- in the Linux test container, the repo checkout root contains a `.git` file
  for a worktree whose target is not mounted inside the container
- Git therefore tried to resolve the broken worktree first and exited `128`
  before honoring the `--no-index` comparison of the temp directories

Concrete fix in this pass:

- make both helpers set `cmd.Dir` to one of the compared directories so the
  no-index diff no longer depends on the caller's cwd
- tighten `TestCompareDirectories_Integration` so it explicitly runs from a
  fake broken-worktree cwd; this keeps the regression covered instead of
  relying on the repo checkout layout in the test container

Verification after this pass:

- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core --run='^TestCompareDirectories_Integration$' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core`

### 2026-03-12: Non-Integration Sweep Status

After the targeted fixes above, the non-integration sweep is down to one real
remaining package failure plus a separate generation prerequisite bucket.

Green in this sweep:

- host-safe root packages (`auth`, codegen templates, `dagql`, `dagql/dagui`,
  `engine`, `engine/client/drivers`, `engine/client/pathutil`,
  `engine/clientdb`, `engine/filesync`, `engine/session/git`,
  `engine/telemetry`, `engine/vcs`, `internal/buildkit/frontend/gateway/container`,
  `internal/cloud/auth`, `util/*`, and the other previously restored
  root-module test dirs)
- Linux-backed root packages: `cmd/dagger`, `cmd/engine`, `core`,
  `core/schema`, `core/sdk`, `engine/buildkit`, `engine/server`
- nested-module unit packages that do not depend on generated bindings:
  `sdk/go`, `sdk/typescript/runtime/tsutils`, `toolchains/cli-dev/util`

Still failing:

- `dagql/idtui`
  - the failure is not a runtime crash; it is telemetry golden drift
  - the observed diffs show the current CLI emitting workspace-loading steps
    like `load workspace: .`, `load extra module: ...`, and
    `ModuleSource.moduleName: String!` where the existing goldens expected the
    older standalone-module load path
  - several golden cases also now fail under the same telemetry surface shift,
    especially the broken-dependency, Python, TypeScript, and remote-module
    examples

Generation prerequisite bucket, not counted as branch regressions:

- `sdk/python/runtime`
- `sdk/typescript/runtime` root package
- `toolchains/cli-dev` root package

Those module roots import generated `internal/dagger` bindings that are
intentionally untracked until `dagger develop -m <module>` runs.

Verification after this sweep:

- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./cmd/dagger`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/schema`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/sdk`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./engine/server`
- `tar -cf - . | docker run --rm -i --entrypoint sh golang:1.26-alpine -lc 'mkdir /src && tar -xf - -C /src && cd /src && /usr/local/go/bin/go test ./cmd/engine ./engine/buildkit -count=1'`

### 2026-03-12: Base Check Slice First Failure

The first concrete failure in `dagger --progress=logs check test-split:test-base`
is a test-only worktree isolation bug in `util/gitutil`.

Root cause:

- `util/gitutil/glob_test.go` shells out to `git ls-remote file://<tmpDir>` as
  a sanity check against Git's own globbing behavior
- that command inherited the outer repo cwd
- in the containerized check run, the outer cwd contains a worktree `.git`
  file that points to a gitdir path not mounted in the container
- Git failed before touching the temp repo, with:
  `fatal: not a git repository: .../.git/worktrees/dagger_workspace`

Concrete fix in this pass:

- set `cmd.Dir = tmpDir` for the sanity-check `git ls-remote` call so it runs
  from the temporary repo it is validating instead of the outer worktree

Verification target for this pass:

- `env GOCACHE=/tmp/go-build go test ./util/gitutil -count=1`

### 2026-03-12: `test-base` Remaining Failures and Fix Policy

After fixing the initial `util/gitutil` worktree leak, the remaining
`test-base` failures fall into two different buckets:

- restored `main` tests that fail because this branch no longer exposes a
  legacy authoring command or flag (`init --blueprint`, `toolchain install`)
- restored `main` tests that reach the current workspace-plumbing runtime path
  and compute the wrong result

This doc update records the current evidence only. No implementation changes
are made in this pass.

Guardrails for the follow-up fixes:

- keep the restored `main` tests as the source of truth unless they are
  demonstrably flaky or harness-only
- do not add a second runtime compatibility path under
  `ModuleSource.asModule()`
- do not revert `check` / `generate` back to the old standalone-module path
- if legacy authoring commands must be restored for backwards compatibility,
  restore them the same way `main` does: as engine-backed `ModuleSource`
  authoring operations whose generated context writes `dagger.json`
- keep the CLI dumb: no direct `dagger.json` editing and no CLI-owned
  blueprint/toolchain resolution logic
- keep runtime behavior on the existing workspace/session/legacy loader; do
  not add authoring-specific runtime branches to satisfy these tests
- if a failure is in runtime behavior, fix it in the existing
  workspace/session/legacy loading path or workspace aggregation semantics, not
  by rewriting the tests to workspace-only expectations

Evidence baseline for this section:

- the earlier worktree-specific git failure does not reproduce in an ordinary
  clone; that removed the worktree layout as the primary explanation for the
  remaining failures
- some focused reruns in the ordinary clone hit generated-module prerequisites
  in `toolchains/cli-dev`, so the detailed reproductions below were captured in
  the main worktree after the worktree-specific git bug was fixed

Current failure ledger:

- `cmd/codegen/generator/typescript/templates`
  - observed once in the isolated `test-base` slice
  - not reproduced by direct package runs in either the main worktree or the
    ordinary clone:
    - `env GOCACHE=/tmp/go-build go test ./cmd/codegen/generator/typescript/templates -count=1`
    - `env GOCACHE=/tmp/go-build go test ./cmd/codegen/generator/typescript/templates -count=5`
  - current cause: unconfirmed
  - current plan: no code or test change yet; capture the exact failing stack
    if it reappears in another slice run
  - fix stance: `pending`

- `core/integration/TestBlueprint`
  - confirmed failing subtest:
    - `TestBlueprint/TestBlueprintUseLocal/use_local_blueprint`
  - exact failure:
    - `dagger init --blueprint=../hello`
    - `Error: unknown flag: --blueprint`
  - same root cause almost certainly explains
    `TestBlueprint/TestBlueprintInit/init_with_python_blueprint`, which uses
    the same restored `main` command shape
  - known cause: this branch currently removed the legacy `init --blueprint`
    authoring surface that `main` exposes through engine-backed
    `ModuleSource.WithBlueprint(...)`
  - current plan:
    - restore the missing engine-backed authoring path from `main`:
      `ModuleSource.withBlueprint` plus the generated-context export flow
    - re-add the CLI flag only as a thin wrapper over that engine API
    - keep the runtime path on workspace/session compat loading
    - do not reintroduce the old module-scoped runtime path just to satisfy
      these tests
    - do not teach the CLI to write legacy config directly
  - fix stance: `implementation`, engine-backed authoring only

- `core/integration/TestChecks`
  - confirmed failing subtest:
    - `TestChecks/TestChecksAsToolchain/typescript`
  - exact failure:
    - `dagger toolchain install ../hello-with-checks-ts`
    - `Error: unknown command or file "toolchain" for "dagger"`
  - same root cause is expected for the toolchain-setup failures in
    `TestToolchain`
  - the full `test-base` slice also reported
    `TestChecks/TestChecksDirectSDK/java`
  - known cause for the toolchain-backed case: this branch currently removed
    the legacy `toolchain ...` authoring surface that `main` exposes through
    engine-backed `ModuleSource` toolchain mutators and queries
  - current plan:
    - restore the missing engine-backed authoring/query surface from `main`:
      `withToolchains`, `withUpdateToolchains`, `withoutToolchains`, and the
      read-side toolchain query used by `toolchain list`
    - re-add the CLI `toolchain ...` commands only as thin wrappers over that
      engine surface
    - keep all config writing in generated-context export rather than direct
      CLI file edits
    - keep looking for a direct isolated repro of `TestChecksDirectSDK/java`
      before deciding whether there is also a separate runtime regression in
      the direct-SDK path
  - fix stance:
    - `TestChecksAsToolchain/*`: `implementation`, engine-backed authoring only
    - `TestChecksDirectSDK/java`: `pending isolated repro`

- `core/integration/TestToolchain`
  - `test-base` reported at least:
    - `TestToolchain/TestMultipleToolchains/install_multiple_toolchains`
    - `TestToolchain/TestToolchainsWithSDK/use_checks_with_sdk_that_have_a_constructor/go`
  - both restored `main` tests execute `dagger toolchain install ...`
  - known cause: most likely the same missing `toolchain ...` command surface
    confirmed in `TestChecks/TestChecksAsToolchain/typescript`
  - current plan:
    - treat these as the same engine-backed authoring compatibility decision as
      the other toolchain-backed failures
    - only do another isolated repro if the later full-suite results suggest a
      second runtime bug after the command surface is restored
  - fix stance: `implementation`, engine-backed authoring only

### 2026-03-12: Category 1 Implementation Plan

For the missing-command failures (`init --blueprint`, `toolchain ...`), the
implementation strategy is now locked to the same architectural shape used on
`main`.

Concrete plan:

- restore the missing engine-backed `ModuleSource` authoring/query surface in
  `core/schema/modulesource.go` rather than teaching `cmd/dagger` to edit
  `dagger.json`
- restore the corresponding generated Go client methods in `sdk/go`
- re-add the CLI flag and `toolchain` command family in `cmd/dagger` only as
  thin wrappers over those engine operations
- keep `check` / `generate` and all runtime loading on the existing
  workspace/session path

Guardrails for this implementation:

- no direct `dagger.json` edits in the CLI
- no second compat path under `ModuleSource.asModule()`
- no rollback of `check` / `generate` to standalone-module loading
- if a command can be restored by pulling code closer to `main`, prefer that
  over inventing a branch-specific adaptation

Review checkpoint:

- after the engine and CLI batches, re-check `git diff origin/main --` on the
  touched files and confirm the delta shrank rather than grew

### 2026-03-12: Category 1 Execution Result

Category 1 is now implemented without changing the runtime design.

What changed:

- restored engine-backed blueprint/toolchain authoring in
  `core/schema/modulesource.go`
  - re-added `withBlueprint`, `withUpdateBlueprint`, `withoutBlueprint`
  - re-added `withToolchains`, `withUpdateToolchains`,
    `withoutToolchains`
  - restored blueprint/toolchain config round-trip through
    `loadModuleSourceConfig(...)`
  - restored blueprint/toolchain loading for local and git module sources
- restored the corresponding `ModuleSource` data model fields and digest inputs
  in `core/modulesource.go`
- restored the corresponding generated Go client methods in `sdk/go`
- restored the CLI surface in `cmd/dagger` only as thin wrappers over those
  engine operations
  - `dagger init --blueprint`
  - `dagger toolchain install|update|uninstall|list`

Guardrails honored in this batch:

- no direct `dagger.json` edits in the CLI
- no second compat path under `ModuleSource.asModule()`
- no rollback of `check` / `generate` away from workspace traversal
- no new runtime-only legacy shim to satisfy authoring tests

Targeted verification after the restore:

- compile checks:
  - `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./cmd/dagger`
  - `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/schema`
- targeted integration reruns:
  - `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestChecks/TestChecksAsToolchain/typescript' --test-verbose`
    - result: passes
  - `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestBlueprint/TestBlueprintUseLocal/use_local_blueprint' --test-verbose`
    - result: `init --blueprint` succeeds; failure moved to runtime compat
  - `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestToolchain/TestMultipleToolchains/install_multiple_toolchains' --test-verbose`
    - result: `toolchain install` succeeds; failure moved to runtime compat

Failure reclassification after the restore:

- `core/integration/TestChecks/TestChecksAsToolchain/typescript`
  - previous cause: missing `toolchain` command
  - current status: green
- `core/integration/TestBlueprint/TestBlueprintUseLocal/use_local_blueprint`
  - previous cause: missing `--blueprint` flag
  - current cause:
    `load contextual arg "config": load legacy default file "./app-config.txt":
    workspace file "./app-config.txt": path "app" resolves outside root "/"`
  - current stance: runtime compat bug in the existing workspace/session/legacy
    path, not an authoring-surface gap
- `core/integration/TestToolchain/TestMultipleToolchains/install_multiple_toolchains`
  - previous cause: missing `toolchain install` command
  - current cause:
    `load contextual arg "config": load legacy default file "./app-config.txt":
    workspace file "./app-config.txt": path "app" resolves outside root "/"`
  - current stance: same runtime compat family as the blueprint case

Diff-to-main checkpoint for the restored authoring files:

- before this batch:
  - `5 files changed, 327 insertions(+), 1617 deletions(-)`
- after this batch:
  - `5 files changed, 367 insertions(+), 853 deletions(-)`

Conclusion:

- category 1 now converges toward `main` instead of diverging from it
- the remaining blueprint/toolchain failures are no longer missing-command
  failures; they have moved into the runtime-compat bucket below

- `core/integration/TestUserDefaults`
  - the original focused failure,
    `TestUserDefaults/TestLocalBlueprint/inner_envfile`, is now fixed locally;
    see the later ledger update below
  - the currently reproduced user-default failures are now:
    - `TestUserDefaults/TestLocalBlueprint/outer_envfile_outer_workdir`
    - `TestUserDefaults/TestLocalToolchain/outer_envfile_outer_workdir`
  - exact failures:
    - `unknown command "message" for "dagger call"`
    - `unknown command "defaults" for "dagger call"`
  - current classification:
    - these are `dagger -m ./app call ...` command-resolution failures
    - treat them as entrypoint-sensitive hold items pending the upstream
      schema-level entrypoint-module cherry-pick
  - current plan:
    - keep the legacy caller-`.env` compat fix in the existing
      workspace/session/legacy loading path
    - do not add a second compat path under `ModuleSource.asModule()`
    - defer the remaining outer-workdir user-default reruns until after the
      upstream entrypoint change lands here
  - fix stance: `partial implementation`

- `core/integration/TestGenerators`
  - confirmed failing subtest:
    - `TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple`
  - exact failure:
    - expected output should not contain `no changes to apply`
    - actual run returns `no changes to apply`
  - observed runtime signal:
    - the command goes through `currentWorkspace`
    - `Workspace.generators(include: ["generate-*"])` compares the include glob
      against workspace-qualified names such as
      `hello-with-generators-java/generate-files`
    - the trace shows the filter rejecting them:
      - `"hello-with-generators-java/generate-files".Glob("generate-*") -> no match`
      - `[generate-*].Equals([hello-with-generators-java]): "generate" != "helloWithGeneratorsJava" -> NOT EQUAL`
    - the resulting generator set is empty, so the command reaches the
      no-op changeset path
  - `test-base` also reported `TestGenerators/TestGeneratorsAsToolchain/go`
  - known cause:
    - the workspace aggregation/filtering semantics do not currently preserve
      the old single-module include-matching behavior that the restored `main`
      tests expect
  - current plan:
    - keep `generate` on the workspace path
    - change the workspace-side generator matching/naming semantics so
      unchanged `main` patterns such as `generate-*` still match in the
      single-module case
    - do not rewrite the tests to use workspace-qualified generator names
    - do not revert to the standalone-module runtime path
  - update:
    - implemented the compat fix in `core/schema/workspace.go`
    - generator filtering still prefers workspace-qualified matches first
    - if exactly one loaded workspace module contributes generators, retry the
      include match after stripping the leading workspace module segment
    - this preserves old single-module patterns such as `generate-*` without
      broadening true multi-module workspace matching
  - focused verification:
    - earlier targeted rerun:
      - `TestGenerators/TestGeneratorsAsToolchain/go`: passes
    - schema matcher coverage:
      - `dagger --progress=plain call -m ./toolchains/go test --pkgs=./core/schema --run='TestMatchWorkspaceInclude|TestFilterGeneratorsByInclude|TestResolveWorkspacePath|TestWorkspaceAPIPath'`
      - result: passes
    - after restoring runtime-client module cache seeding:
      - `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple' --test-verbose`
      - result: passes
      - `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsAsBlueprint/java/generate' --test-verbose`
      - result: passes
  - fix stance: `implemented + verified`

Verification used to build this ledger:

- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestBlueprint/TestBlueprintUseLocal/use_local_blueprint' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestChecks/TestChecksAsToolchain/typescript' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalBlueprint/inner_envfile' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalBlueprint' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalToolchain/inner_envfile' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalToolchain/outer_envfile_outer_workdir' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsAsToolchain/go' --test-verbose`
- `dagger --progress=plain call -m ./toolchains/go test --pkgs=./core/schema --run='TestMatchWorkspaceInclude|TestFilterGeneratorsByInclude|TestResolveWorkspacePath|TestWorkspaceAPIPath'`
- `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple' --test-verbose`
- `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsAsBlueprint/java/generate' --test-verbose`
- `env GOCACHE=/tmp/go-build go test ./cmd/codegen/generator/typescript/templates -count=1`
- `env GOCACHE=/tmp/go-build go test ./cmd/codegen/generator/typescript/templates -count=5`

### 2026-03-13: Legacy Toolchain Customization Threading

After the root-`/` sandbox fix in `bae60c5b2`, the next remaining targeted
toolchain failure narrowed to:

- `core/integration/TestToolchain/TestToolchainsWithConfiguration/override constructor defaultPath argument`

Known cause:

- legacy compat parsing was preserving primitive constructor defaults via
  `ConfigDefaults`, but not the broader legacy toolchain customizations needed
  to restore `defaultPath`, `defaultAddress`, and `ignore`
- that meant the existing workspace/session loader could load the toolchain
  module, but its already-loaded typedefs no longer reflected the legacy
  contextual argument overrides the tests expect

Cleanup done before touching runtime:

- `legacy.go` now reuses the authoritative `modules.ModuleConfig` schema instead
  of maintaining a private `dagger.json` JSON shape
  - committed in `8126a7a94`

Current implementation for the runtime fix:

- `pendingModule` carries `ArgCustomizations`
- legacy toolchain compat extraction threads those customizations through
  `pendingLegacyModule(...)`
- `resolveModule(...)` applies them onto the already-loaded module typedefs
- the applicator lives in `core/module.go` as
  `ApplyLegacyCustomizationsToTypeDefs(customizations)`
- the customizations are not stored on `core.Module`; they are applied during
  load and discarded

Guardrails preserved in this batch:

- no second loader path under `ModuleSource.asModule()`
- no rollback of `check` / `generate` away from workspace traversal
- no CLI-side `dagger.json` mutation
- no new persistent compat field on `core.Module`

Supporting test-only cleanup:

- `core/integration/workspace_test.go` had stale helper usage from the earlier
  command/path-contract resets
- that helper drift is fixed in `d6a7b60fb` (`test: fix workspace helper drift`)

Focused verification for the in-flight runtime patch:

- `env GOCACHE=/tmp/go-build go test ./core/workspace -run TestParseLegacyPins -count=1`
  - result: passes
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/workspace ./engine/server ./core ./core/integration`
  - result: passes
- added focused unit coverage in `core/module_legacy_test.go` for:
  - constructor `defaultPath` + `ignore`
  - chained function default-value override

Validation gap still open at this checkpoint:

- direct execution of `go test ./core` and `go test ./engine/server` remains
  blocked on this Darwin host by the existing Linux-only `engine/buildkit` and
  overlay snapshotter files
- the `engine-dev` harness remains too opaque/slow to treat as a quick
  validation loop for this one targeted fix

Current stance:

- this is an acceptable narrow compat fix because it only restores metadata that
  the current loader already parsed and attached to the legacy toolchain path
- the next step after landing it is a decisive targeted rerun of the remaining
  toolchain integration repro

### 2026-03-13: Toolchain Repro Green, Entrypoint Hold Rule

The targeted rerun for the legacy toolchain customization patch is now green:

- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestToolchain/TestToolchainsWithConfiguration/override_constructor_defaultPath_argument' --test-verbose`
  - result: passes
- the restored legacy toolchain customizations now reach constructor
  `defaultPath` loading correctly through the existing workspace/session path

Scope consequence after that rerun:

- the legacy toolchain customization thread is no longer an active failure lane
- the next active non-entrypoint runtime bug is now the caller `.env` /
  default-propagation failure in legacy blueprint/toolchain compat loading

Parallel-work coordination rule:

- the `workspace` branch is still landing the upstream schema-level entrypoint
  module change
- until that change is cherry-picked here, defer any remaining failures whose
  primary symptom is root-entrypoint selection or CLI focus behavior:
  `call`, `functions`, shell, and related `defaultModule` expectations
- after that cherry-pick lands, rerun those entrypoint-sensitive buckets before
  deciding whether any local fix is still needed on `workspace-plumbing`
- keep working meanwhile on runtime compatibility bugs that are not
  entrypoint-sensitive, especially:
  - caller `.env` / default propagation through legacy compat loading
  - generator include matching, unless a rerun later proves it is actually
    entrypoint-sensitive

### 2026-03-13: Legacy Blueprint Default-File Repro Green

The previously active blueprint default-file path failure is no longer
reproducing.

Focused rerun:

- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestBlueprint/TestBlueprintUseLocal/use_local_blueprint' --test-verbose`
  - result: passes

What this means:

- the earlier `Workspace.file("./app-config.txt")` failure under the legacy
  blueprint path is no longer an active runtime issue on this branch
- the narrow non-entrypoint runtime focus now shifts to legacy `.env` /
  user-default propagation and generator include matching

### 2026-03-13: Legacy Blueprint Caller `.env` Compat Restored

Implemented a focused compat fix in `engine/server/session.go`:

- when loading a legacy blueprint module, record the caller module directory
- before `ModuleSource.asModule()`, if that caller module has its own `.env`,
  load it as an env file and merge it over the legacy blueprint module-source
  defaults
- recompute the module-source digest after the merge so codegen/runtime cache
  identity follows the effective default set

Focused verification:

- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalBlueprint/inner_envfile' --test-verbose`
  - result: passes
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalBlueprint' --test-verbose`
  - `inner_envfile`: passes
  - `outer_envfile_inner_workdir`: passes
  - `outer_envfile_outer_workdir`: still fails, but now as
    `unknown command "message" for "dagger call"`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalToolchain/inner_envfile' --test-verbose`
  - result: passes
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalToolchain/outer_envfile_outer_workdir' --test-verbose`
  - still fails, but now as `unknown command "defaults" for "dagger call"`

Scope consequence:

- the reproduced non-entrypoint caller-`.env` / user-default propagation gap is
  fixed for the legacy blueprint `inner_envfile` path
- the remaining reproduced user-default failures are both
  `dagger -m ./app call ...` command-resolution failures, so treat them as
  entrypoint-sensitive hold items pending the upstream schema-level
  entrypoint-module cherry-pick
- the next local non-entrypoint follow-up was generator include matching; that
  implementation is recorded in the next entry

### 2026-03-13: Generator Include Matching Compat Restored

Implemented a focused workspace-side compat fix in `core/schema/workspace.go`:

- collect all generator groups first and count how many loaded workspace
  modules actually contribute generators
- keep normal workspace-qualified include matching as the first pass
- when exactly one module contributes generators, retry include matching
  against the generator path without the leading workspace module segment
- keep the fallback scoped to generators only, so `check` and other workspace
  grouping behavior stay unchanged

Focused verification:

- prior targeted rerun:
  - `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsAsToolchain/go' --test-verbose`
  - result: passes
- schema-level matcher coverage:
  - `dagger --progress=plain call -m ./toolchains/go test --pkgs=./core/schema --run='TestMatchWorkspaceInclude|TestFilterGeneratorsByInclude|TestResolveWorkspacePath|TestWorkspaceAPIPath'`
  - result: passes
- fresh direct-SDK reruns after restoring runtime-client module cache seeding:
  - `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple' --test-verbose`
  - result: passes
  - `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsAsBlueprint/java/generate' --test-verbose`
  - result: passes

Scope consequence:

- the workspace generator filter now preserves legacy single-module include
  patterns like `generate-*` without reverting `generate` to the standalone
  module path
- the direct and blueprint Java generator repros now confirm the fallback also
  behaves correctly when only one loaded workspace module actually contributes
  generators
- with caller-`.env` propagation fixed and generator matching implemented, the
  remaining previously reproduced failures on the list are entrypoint-sensitive
  hold items rather than active non-entrypoint runtime regressions

### 2026-03-13: Runtime Client Content-Digest Cache Restored

The generator rerun block turned out not to be a generator-matching problem
after all. It was a runtime client cache gap in `engine/server/session.go`:

- module constructor `+defaultPath` directories resolve through
  `_contextDirectory`
- `_contextDirectory` rehydrates the originating module from the
  content-digest cache before calling `LoadContextDir`
- runtime clients created from `EncodedModuleID` were loading `client.mod`, but
  not seeding that same content-digest cache in the new client server
- that let plain field access like `engineDev.source.entries` work, while later
  nested dependency calls such as `dag.Go(...).Binary("./cmd/codegen")` failed
  trying to reload the same contextual directory

Implemented fix:

- after loading `client.mod` from `EncodedModuleID`, immediately call
  `core.CacheModuleByContentDigest(...)` for the current runtime client

Focused verification:

- `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple' --test-verbose`
  - result: passes
- `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsAsBlueprint/java/generate' --test-verbose`
  - result: passes

Scope consequence:

- the previous `toolchains/engine-dev` self-context failure loading
  `bin/codegen` is gone for the reproduced generator paths
- the existing generator regression tests now serve as the end-to-end coverage
  for both the workspace include fallback and the restored runtime-client
  contextual-dir reload path

### 2026-03-14: Entrypoint Module Cherry-Pick Landed

Cherry-picked the upstream schema-level entrypoint change from `workspace`:

- `bbdfa797e` `workspace: move entrypoints to Query root`

Branch-specific conflict resolution kept the `workspace-plumbing` scope
intact:

- kept PR A's no-new-targeting cut:
  - did not reintroduce explicit workspace target parsing for
    `call` / `functions`
  - did not restore client-side workspace-target plumbing that belongs in the
    deferred follow-up UX
- removed the temporary CLI sibling-entrypoint workaround from
  `cmd/dagger/call.go`, `cmd/dagger/functions.go`, and
  `cmd/dagger/module_inspect.go`
- kept the real schema/runtime change:
  - `core.Module.InstallOpts` now carries `Entrypoint`
  - `core/served_mods.go` preserves entrypoint install policy through module
    dedupe/promotion
  - `core/object.go` installs non-conflicting Query-root proxies for the
    workspace entrypoint module's main-object methods
  - `engine/server/session.go` threads entrypoint install policy through the
    existing workspace/session loader path
- preserved later local compat fixes in the same files:
  - legacy toolchain customization threading
  - legacy blueprint caller `.env` defaults
  - runtime client content-digest cache seeding
- pruned the upstream `workspace_test.go` tail back to PR A scope instead of
  re-importing the deferred initialized-workspace/config/migrate suite
  - kept the retained entrypoint-focused coverage only:
    - `TestBlueprintFunctionsIncludesOtherModules`
    - `TestEntrypointProxySkipsRootFieldConflicts`
    - `TestEntrypointProxySkipsConstructorArgConflicts`
  - kept `TestNestedModuleBeneathWorkspace` deferred per the earlier transplant
    audit

Focused verification for this cherry-pick:

- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./cmd/dagger`
  - result: passes
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core`
  - result: passes
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./engine/server`
  - result: passes
- `env GOCACHE=/tmp/go-build GOOS=linux GOARCH=amd64 go test -c ./core/integration`
  - result: passes

Validation caveats:

- native `env GOCACHE=/tmp/go-build go test ./cmd/dagger -count=1` on this
  Darwin host still fails in `engine/buildkit` on Linux-only `unix.*` symbols;
  this is the same host/build-constraint class seen elsewhere on the branch,
  not a targeted entrypoint regression signal
- targeted `toolchains/engine-dev` rerun for the three retained entrypoint
  integration tests was started, but did not finish in a reasonable local
  validation window; rerun that slice again before calling the entrypoint hold
  bucket fully cleared:
  - `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestWorkspace/(TestBlueprintFunctionsIncludesOtherModules|TestEntrypointProxySkipsRootFieldConflicts|TestEntrypointProxySkipsConstructorArgConflicts)' --test-verbose`

Current stance after this landing:

- the schema-level entrypoint design is now present locally on
  `workspace-plumbing`
- the next follow-up is no longer "cherry-pick the entrypoint change"; it is
  "rerun the entrypoint-sensitive hold bucket on top of this design and keep
  only the fixes that still reproduce here"

### 2026-03-14: Entrypoint Hold-Bucket Rerun Checkpoint

Takeover checkpoint before the next validation pass:

- branch sync state at takeover:
  - `workspace-plumbing` is at `origin/workspace-plumbing`
  - there are no local commits ahead of origin yet
- existing uncommitted worktree state at takeover:
  - `cmd/dagger/module_inspect.go`
  - `cmd/dagger/mcp.go`
  - `core/integration/user_defaults_test.go`
- current classification of that worktree state:
  - `module_inspect.go` and `mcp.go` are an unverified attempt to repair
    explicit `-m` CLI focus after the Query-root entrypoint cherry-pick
  - `user_defaults_test.go` only adds nested stdout/stderr logging for the
    existing user-default repros; it is debugging instrumentation, not a fix
- locked next step:
  - first rerun the retained entrypoint-sensitive hold bucket from the previous
    checkpoint exactly as recorded there
  - only after that rerun should any of the current local CLI WIP be kept,
    rewritten, or discarded
- handoff rule for this pass:
  - keep the ledger updated after each substantive result
  - commit each substantive checkpoint separately so the branch can be handed
    off without relying on worktree-only context

### 2026-03-14: Combined Entrypoint Hold-Bucket Rerun Still Opaque Locally

Reran the exact combined hold-bucket command from the prior checkpoint:

- `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestWorkspace/(TestBlueprintFunctionsIncludesOtherModules|TestEntrypointProxySkipsRootFieldConflicts|TestEntrypointProxySkipsConstructorArgConflicts)' --test-verbose`

Observed local behavior:

- the command again progressed through engine connect, module load, and into:
  - `EngineDev.test(run: "TestWorkspace/(TestBlueprintFunctionsIncludesOtherModules|TestEntrypointProxySkipsRootFieldConflicts|TestEntrypointProxySkipsConstructorArgConflicts)", pkg: "./core/integration", testVerbose: true): Void`
- after entering `EngineDev.test(...)`, no further test output or completion
  signal arrived within a normal local validation window
- this reproduces the earlier "started but did not finish in a reasonable local
  validation window" caveat from the cherry-pick checkpoint rather than
  producing a new pass/fail classification

Worktree hygiene cleanup performed before continuing:

- found and terminated three orphaned local `dagger ... engine-dev test` runs:
  - the combined `TestWorkspace/(...)` rerun above
  - an earlier `TestUserDefaults/TestLocalBlueprint/outer_envfile_outer_workdir`
    rerun from takeover audit
  - an earlier `TestUserDefaults/TestLocalToolchain/outer_envfile_outer_workdir`
    rerun from takeover audit

Locked next step:

- split the retained entrypoint hold bucket into individual `TestWorkspace/*`
  reruns under the same harness so the branch gets attributable results instead
  of another opaque long-running aggregate slice
- keep the current local CLI WIP unverified until those individual reruns show
  whether any explicit `-m` or Query-root focus regression is still active

### 2026-03-14: Individual Hold Reruns Need Explicit Test Timeout

Started the first split rerun under the same `toolchains/engine-dev` harness:

- `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestWorkspace/TestBlueprintFunctionsIncludesOtherModules' --test-verbose`

Observed behavior:

- setup completed quickly compared with the aggregate slice:
  - engine connect/session load: complete
  - `load module: ./toolchains/engine-dev`: complete
  - `EngineDev.test(run: "TestWorkspace/TestBlueprintFunctionsIncludesOtherModules", pkg: "./core/integration", testVerbose: true): Void`
- after entering `EngineDev.test(...)`, there was still no attributable test
  output before the run was stopped

Conclusion from this pass:

- the current blocker is not just "combined slice too broad"; the default
  `toolchains/engine-dev` test path is still too opaque here when left on its
  default `30m` Go test timeout
- continuing to rerun without an explicit shorter timeout would keep consuming
  local validation windows without producing actionable pass/fail output

Locked next step:

- rerun each retained `TestWorkspace/*` case individually with an explicit
  shorter `timeout` argument to the same `EngineDev.test(...)` surface so a
  hang turns into a stack-dumped failure instead of another silent long wait
- keep the local CLI WIP unclassified until those timed individual reruns show
  whether the retained entrypoint tests are actually broken on this branch

### 2026-03-14: Timed `EngineDev.test` Rerun Still Failed To Surface Output

Retried the first retained entrypoint test through the same harness with an
explicit shorter timeout:

- `dagger --progress=plain call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestWorkspace/TestBlueprintFunctionsIncludesOtherModules' --timeout=3m --test-verbose`

Observed behavior:

- the CLI confirmed the timeout was threaded into the function call:
  - `EngineDev.test(run: "TestWorkspace/TestBlueprintFunctionsIncludesOtherModules", pkg: "./core/integration", timeout: "3m", testVerbose: true): Void`
- despite that, the outer `dagger call` stayed alive beyond the expected `3m`
  test window without surfacing a pass, a timeout failure, or any test output
- the run was stopped manually because it was no longer producing new
  information

Conclusion from this pass:

- for this branch state, `toolchains/engine-dev test` is currently too opaque
  to use as the first-line validation surface for the retained entrypoint
  bucket, even after threading a shorter Go test timeout
- the next validation pass needs a more direct harness that surfaces the test
  process output immediately enough to distinguish pass, fail, and hang

Locked next step:

- rerun the retained `TestWorkspace/*` cases with native `go test` from the
  repo worktree, using a short explicit timeout, to get attributable output
  before deciding whether any of the current local CLI WIP is warranted
- only fall back to the opaque `toolchains/engine-dev` surface again if native
  `go test` proves non-viable on this host

### 2026-03-14: Native `go test` Blocked On Darwin Host Build Constraints

Tried the first retained entrypoint test natively from the repo worktree:

- `env GOCACHE=/tmp/go-build go test ./core/integration -run 'TestWorkspace/TestBlueprintFunctionsIncludesOtherModules' -count=1 -v -timeout=3m`

Observed result:

- immediate build failure in `engine/buildkit` on Linux-only `unix.*` symbols:
  - `undefined: unix.OpenTree`
  - `undefined: unix.OPEN_TREE_CLONE`
  - `undefined: unix.OPEN_TREE_CLOEXEC`
  - `undefined: unix.AT_RECURSIVE`
  - `undefined: unix.Unshare`
  - `undefined: unix.CLONE_FS`
  - `undefined: unix.Setns`
- the retained `TestWorkspace/*` slice therefore cannot be validated natively on
  this Darwin host

Scope consequence:

- the fallback to native `go test` is not viable here
- this is the same host/build-constraint class already noted earlier for other
  packages on this branch, but it is now directly confirmed for
  `./core/integration` too

Locked next step:

- keep validation containerized, but move off the opaque `EngineDev.test`
  wrapper onto a more direct local-playground/container execution path that can
  run `go test` against this branch's source while surfacing the inner test
  output directly
- do not classify the existing local CLI WIP until that more observable
  containerized rerun produces attributable results

### 2026-03-14: Local Playground Wrapper Chosen As Next Validation Harness

Exploration after the native-host failure:

- confirmed the repo root exports `engine-dev` as a root module dependency
  (`dagger.json` includes `"name": "engine-dev", "source": "toolchains/engine-dev"`)
- confirmed `engine-dev playground` returns a plain `Container`, with the
  expected container mutation surface available from the CLI:
  - `with-directory`
  - `with-mounted-directory`
  - `with-exec`
  - `stdout` / `stderr` / `combined-output`
- confirmed the sibling `engine-dev-testing` skill still recommends
  `playground` as the manual validation layer, with a wrapper script that adds:
  - heartbeat output during long builds
  - an outer timeout
  - preserved inner command output plus trace tail on failure

What did not work well enough directly:

- raw `dagger call -m ./toolchains/engine-dev playground ...` probes remained
  too silent/long-running to use directly as a reliable handoff-safe validation
  surface
- that made the wrapper behavior, not the underlying `playground` primitive,
  the missing piece

Locked next step:

- run a local-playground wrapper modeled on `engine-dev-testing/with-playground.sh`
  but pointed at this branch checkout for `src/dagger` instead of the remote
  sample repo
- use that wrapper to execute one retained `TestWorkspace/*` case at a time
  with direct `go test` output and an outer watchdog timeout
- only after that wrapper produces attributable test results should the local
  CLI WIP be classified as required or stale

Concrete command selected for the first retained test:

- `dagger --progress=logs call -m ./toolchains/engine-dev playground --shared-cache with-mounted-directory --path=/src/dagger --source=. with-workdir --path=/src/dagger with-exec --args=sh --args=-lc --args='env GOCACHE=/tmp/go-build go test ./core/integration -run "^TestWorkspace/TestBlueprintFunctionsIncludesOtherModules$" -count=1 -v -timeout=3m' combined-output`

Why this is the chosen next harness:

- `toolchains/engine-dev/test.go` runs the inner test with
  `WithExec(args).Sync(ctx)`, so it does not expose the inner `go test`
  stdout/stderr directly
- `playground` returns a plain `Container`, so ending the chain with
  `combined-output` should expose the actual inner `go test` output from the
  same Linux-native dev-engine environment

### 2026-03-14: Raw Local Playground Call Still Needs Outer Watchdog

Ran the first retained test with direct `playground` + `combined-output`:

- `dagger --progress=logs call -m ./toolchains/engine-dev playground --shared-cache with-mounted-directory --path=/src/dagger --source=. with-workdir --path=/src/dagger with-exec --args=sh --args=-lc --args='env GOCACHE=/tmp/go-build go test ./core/integration -run "^TestWorkspace/TestBlueprintFunctionsIncludesOtherModules$" -count=1 -v -timeout=3m' combined-output`

Observed behavior:

- the outer `dagger call` remained alive beyond the inner `go test -timeout=3m`
  window
- no inner `go test` stdout/stderr had been surfaced yet when the process-state
  check was taken
- that means moving from `EngineDev.test(...)` to raw `playground` was not
  sufficient by itself to make the validation pass handoff-safe

Scope consequence:

- the remaining missing piece is now specifically the outer wrapper behavior
  from `engine-dev-testing/with-playground.sh`:
  - heartbeat output during the long build/run window
  - an outer watchdog timeout
  - controlled trace capture on failure/timeout
- the underlying `playground` path still appears to be the right execution
  layer, but not as a naked synchronous `dagger call`

Locked next step:

- run a one-off local wrapper modeled on `engine-dev-testing/with-playground.sh`
  that:
  - uses `engine-dev playground` from this branch
  - mounts this checkout at `src/dagger`
  - writes the inner `go test` command to `/tmp/inner.sh`
  - adds heartbeat output and an outer watchdog timeout
- use that wrapped run as the authoritative next signal for
  `TestWorkspace/TestBlueprintFunctionsIncludesOtherModules`

### 2026-03-14: Wrapped Local Playground Run Failed During Source Resolution

Ran the wrapped local-playground harness for the first retained test. The
wrapper behaved as intended operationally:

- heartbeat messages appeared every 30s
- the run stayed observable/handoff-safe instead of going silent
- the final failure included the trace tail rather than leaving an orphaned
  background process

Concrete result:

- no inner `go test` output was reached
- trace tail showed the failure happened earlier, while evaluating the local
  `source=.` directory argument for the mounted repo:
  - `✘ parsing command line arguments 5m31s ERROR`
  - `failed to get value for argument "source": Post "http://dagger/query": read tcp 172.20.0.217:64869->64.6.38.39:444: read: operation timed out`

Scope consequence:

- the current blocker is no longer "entrypoint test may hang"; it is
  "uploading or resolving the full repo as a `Directory` argument for the local
  playground wrapper times out before the test even starts"
- the wrapper itself is now validated as the right operational shape for this
  branch because it surfaces that failure cleanly

Locked next step:

- retry the same wrapped local-playground harness with a narrower source
  directory payload instead of raw `source=.`
- start from the existing `toolchains/engine-dev` source filter as the baseline
  include/exclude set, then add only the extra repo paths needed for
  `go test ./core/integration`
- keep the local CLI WIP untouched until the validation harness gets past this
  source-resolution blocker and reaches the actual retained tests

### 2026-03-14: Reduced-Source Playground Probe Avoided Fast Parse Failure

Retried the wrapped local-playground harness with a narrowed repo payload and a
trivial inner command (`ls core/integration` plus `source-ok`).

Observed result:

- the run stayed alive for the full outer `420s` watchdog window
- unlike the earlier raw `source=.` mount, it did not fail quickly during
  argument parsing with a `failed to get value for argument "source"` timeout
- the wrapper eventually killed it at the outer timeout:
  - `=== TIMEOUT: killed after 420s ===`
- the trace tail only showed the connection phase plus continued work dots,
  without an early parser failure

Scope consequence:

- narrowing the source payload changed the failure mode in the desired
  direction: the fast source-resolution timeout no longer reproduced
- the remaining problem is now total wall time for the local-playground path,
  not the earlier immediate `Directory`-argument resolution failure
- because the run used `--shared-cache`, the next attempt has a reasonable
  chance of benefiting from the warmed intermediate state even though this
  probe itself timed out

Locked next step:

- keep the reduced-source shape
- rerun the first retained `TestWorkspace/*` case on the same wrapped harness
  with a longer outer watchdog window
- if that longer run still fails to reach inner `go test` output, treat the
  local-playground path itself as too expensive for this handoff window and
  record that explicitly before considering any further validation-path change

### 2026-03-14: Reduced-Source Local Playground Still Too Expensive For Fast Reruns

Reran the first retained test on the reduced-source wrapped harness with a
longer `900s` outer watchdog.

Observed result:

- heartbeat stayed healthy for the entire 15-minute window
- no inner `go test` stdout/stderr was surfaced before the watchdog fired
- the wrapper exited with:
  - `=== TIMEOUT: killed after 900s ===`
- the trace tail still showed only early progress dots rather than the inner
  test output

Scope consequence:

- for this local checkout, even the reduced-source `playground` path is too
  expensive to serve as the primary immediate rerun loop for the retained
  entrypoint bucket
- this is now a validation-harness cost problem, not just a test-failure or
  test-hang problem
- importantly, no new functional code has been committed locally during this
  pass; the local commits since takeover are ledger-only checkpoints, and the
  uncommitted CLI WIP remains intentionally unverified

Locked next step:

- switch the next rerun attempt to a remote branch source that avoids local
  `Directory` upload costs while still matching the current committed branch
  behavior:
  - use `origin/workspace-plumbing` or the equivalent GitHub ref as the source
    payload for `src/dagger`
- only return to local-source validation after a real code fix exists that is
  not already represented by the remote branch contents

### 2026-03-14: Remote Branch Playground Produced The First Actionable Failure

Ran the wrapped playground harness against remote `workspace-plumbing` branch
content for both:

- the module under test (`-m github.com/dagger/dagger@workspace-plumbing`)
- the mounted `src/dagger` source

This changed the validation picture materially:

- remote source avoided the local `Directory` upload bottleneck
- the run completed far enough to build the dev engine, start the playground,
  mount `src/dagger`, and execute the inner script
- the first attributable failure from this path was no longer harness opacity:
  - `env: can't execute 'go': No such file or directory`
  - `withExec sh /tmp/inner.sh` exited `127`

What this means:

- the remote-playground path is now the first validation surface that is both:
  - fast enough to get past harness setup in a reasonable window
  - observable enough to yield a concrete inner-command failure
- the next blocker is simple and local to the harness:
  - the `playground` container does not include the Go toolchain needed to run
    `go test` directly from `src/dagger`

Locked next step:

- keep the remote `workspace-plumbing` playground path
- add Go into the playground container before running the inner test script
- rerun `TestWorkspace/TestBlueprintFunctionsIncludesOtherModules` on that
  adjusted remote harness before changing any branch behavior or classifying
  the uncommitted local CLI WIP

### 2026-03-14: Remote Playground + `toolchains/go` Reached Real Test Execution

Instead of trying to install `go` into the playground image directly, reran the
remote playground path with the inner script delegating to the repo's
`toolchains/go` module:

- inside `/src/dagger`:
  - `dagger --progress=plain call -m ./toolchains/go env with-exec --args=go --args=test --args=-v --args=-timeout=3m --args=-count=1 --args=-run=^TestWorkspace/TestBlueprintFunctionsIncludesOtherModules$ --args=./core/integration combined-output`

This is the closest the rerun has gotten to the actual retained test:

- the remote playground came up successfully
- the remote `src/dagger` mount stayed cached
- the inner `toolchains/go` invocation ran for about `1m43s`
- the trace showed real Go dependency downloads inside that inner run
- the inner step still exited `1`, but now the remaining blocker is narrowed to
  surfacing the actual failure text rather than getting to test execution at
  all

Scope consequence:

- the remote playground + `toolchains/go` path is now the best validation lane
  for the retained entrypoint bucket
- the current blocker is no longer missing toolchains or source transport; it
  is getting the inner failure details out of the nested Dagger call

Locked next step:

- rerun the same remote playground + `toolchains/go` path with inner
  `dagger --progress=logs` (or equivalent stderr capture) so the actual test or
  build failure is visible
- only after that concrete failure is visible should any local CLI code change
  be attempted

### 2026-03-14: Remote Playground + `toolchains/go` Exposed The First Real Test Failure

Reran the same remote playground + `toolchains/go` path with inner
`dagger --progress=logs`, which finally surfaced the actual failing test output.

Concrete test result:

- `TestWorkspace/TestBlueprintFunctionsIncludesOtherModules` still did not reach
  its entrypoint assertions
- the failure happened at test setup time inside `connect(...)`:
  - `failed to read session params: EOF`
  - `start engine: driver for scheme "image" was not available`
- the failing stack in the surfaced stdout pointed to:
  - `core/integration/suite_test.go:71`
  - `core/integration/workspace_test.go:812`

What this means:

- this is the first real `core/integration` failure text obtained during the
  hold-bucket rerun effort
- it is not yet evidence of an entrypoint regression in
  `TestBlueprintFunctionsIncludesOtherModules`
- instead, it shows that running the test through the remote playground plus a
  nested `toolchains/go` container changes the environment enough that the test
  cannot start the engine it expects

Scope consequence:

- the remote playground + `toolchains/go` path is now proven useful for
  observability, but not yet equivalent enough to the intended integration
  environment for this bucket
- the blocker has narrowed again:
  - not source transport
  - not missing Go in the playground
  - specifically the nested engine/runtime environment seen by the test

Locked next step:

- avoid making any local CLI behavior change based on this failure alone
- either:
  - construct a Go-capable `playground` base container so `go test` runs inside
    the actual playground environment, or
  - stop the rerun hunt here and treat the hold-bucket rerun as blocked on
    integration-harness equivalence rather than product behavior

### 2026-03-14: Handoff Checkpoint After Entrypoint Hold-Bucket Investigation

Current committed branch state:

- the upstream entrypoint-module cherry-pick is already landed here:
  - `bbdfa797e` `workspace: move entrypoints to Query root`
- no new functional code has been committed during this investigation
- all new commits since takeover are ledger-only checkpoints

Current uncommitted local WIP:

- `cmd/dagger/module_inspect.go`
- `cmd/dagger/mcp.go`
- `core/integration/user_defaults_test.go`

Current classification of that WIP:

- it appears to be an interrupted attempt to adapt explicit `-m` CLI focus to
  the Query-root entrypoint design
- it is not verified yet and should not be landed as-is
- it does not yet satisfy the design/process bar because:
  - the new filtered-Query helper is still unwired
  - the current logic can still fall back to an unfiltered `Query`
  - the test-file change is still debugging instrumentation only

Current blocker summary:

- the remaining uncertainty is not "did the entrypoint-module cherry-pick land"
  and not "is the upstream schema/runtime design obviously wrong"
- the cherry-pick is landed, and the upstream schema/runtime implementation
  still appears coherent
- preferred test harness guidance for the next session:
  - use system `dagger` to build and test this repo
  - for the integration suite, use `dagger check -l test-split`
  - for targeted reruns, use `dagger call engine-dev test --run=TestSomethingSpecificHere`
  - keep `dagger playground` for ad-hoc QA/manual commands, not for primary
    integration-suite reruns
- the direct rerun through the intended harness now works and supersedes the older
  playground/toolchains investigation:
  - `dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/(TestBlueprintFunctionsIncludesOtherModules|TestEntrypointProxySkipsRootFieldConflicts|TestEntrypointProxySkipsConstructorArgConflicts)'`
  - trace: `https://dagger.cloud/dagger/traces/ba68601dbd7ba2f4fae511f47ac121c7`
- important `engine-dev test` note:
  - the function defaults `pkg` to `./...`
  - for targeted hold-bucket reruns, pass `--pkg=./core/integration`
  - otherwise unrelated package init failures can contaminate the result before the
    targeted integration test verdict is useful
  - this happened during an earlier direct rerun when
    `cmd/codegen/generator/typescript/templates` panicked while loading a legacy
    bare `dagger` dependency version
- current hold-bucket result under the correct harness:
  - `TestWorkspace/TestEntrypointProxySkipsRootFieldConflicts` passes
  - `TestWorkspace/TestBlueprintFunctionsIncludesOtherModules` still fails in:
    - `dagger_functions_shows_all_modules`
    - `dagger_call_blueprint_function`
    - `dagger_call_sibling_module_function`
    - `query_root_exposes_blueprint_entrypoint_methods`
  - `TestWorkspace/TestEntrypointProxySkipsConstructorArgConflicts` still fails in:
    - `namespaced_method_still_accepts_both_args`
- so the previous harness-equivalence blocker is cleared for this bucket; the
  remaining failures are now real branch behavior, not a playground artifact
- constructor-arg conflict diagnosis from the direct rerun:
  - this does not currently look like proof that the schema/runtime
    entrypoint-module design is wrong
  - the engine-side Query-root proxy suppression appears intentional and
    coherent: if a module constructor arg and a main-object method arg share the
    same name, the root proxy is hidden rather than exposing an ambiguous merged
    signature
  - concrete retained example:
    - module path: `ci`
    - constructor: `new(prefix: String! = "ctor")`
    - method: `echo(prefix: String! = "method")`
  - the hidden root proxy is expected here because a root-shaped call like
    `dagger call echo --prefix ... --prefix ...` does not say which `--prefix`
    belongs to the constructor vs the method
  - the namespaced path remains the intended escape hatch and should be
    unambiguous in forms like:
    - `dagger call ci echo`
    - `dagger call ci --prefix ctor echo --prefix method`
  - the currently reproduced blind spot is narrower and appears to live in CLI
    traversal:
    - `dagger call --prefix ctor ci echo --prefix method`
    - this looks human-readable as "constructor flag first, then descend into
      ci", but the current `Query`-root CLI parsing sees the leading
      `--prefix` before it has descended into `ci`, so the flag has no owner
  - likely fix boundary from this diagnosis:
    - repair `cmd/dagger/functions.go` traversal/flag ownership for the
      namespaced constructor path
    - do not treat this as evidence that `core/object.go`'s proxy-skipping
      behavior should be reverted
- local follow-up outcome after reproducing the bucket:
  - `cmd/dagger/module.go` now treats repeated bare `dagger init` as a no-op
    when the module already exists and no init-mutating flags were explicitly
    requested; this removed an earlier workspace bootstrap blocker from the
    direct rerun path
  - `cmd/dagger/functions.go` now rewrites leading constructor flags onto the
    namespaced module constructor path when the workspace CLI root is `Query`
  - the constructor-conflict case now resolves as intended:
    - `dagger call --prefix ctor ci echo --prefix method`
    - effective traversal: `dagger call ci --prefix ctor echo --prefix method`
    - result: `ctor:method`
- verification after the local follow-up:
  - `dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestEntrypointProxySkipsConstructorArgConflicts/namespaced_method_still_accepts_both_args' --test-verbose`
    - trace: `https://dagger.cloud/dagger/traces/fdcce7fff92d111102e4a248f2a4b5f3`
    - result: passes
  - `dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/(TestBlueprintFunctionsIncludesOtherModules|TestEntrypointProxySkipsRootFieldConflicts|TestEntrypointProxySkipsConstructorArgConflicts)' --test-verbose`
    - trace: `https://dagger.cloud/dagger/traces/9bb1f6aa6a8acf126b0639ee675bf16f`
    - result: all retained workspace entrypoint tests pass

Explicit `-m` follow-up outcome under the same direct `engine-dev test` harness:

- the older `cmd/dagger/mcp.go` / `cmd/dagger/module_inspect.go` WIP was not
  sufficient by itself
  - direct reruns still reproduced:
    - `TestUserDefaults/TestLocalBlueprint`
    - `TestUserDefaults/TestLocalToolchain`
  - concrete failures before the landing work:
    - `dagger -m ./app call message`
    - `dagger -m ./app call defaults message`
    - both failed as unknown commands because the explicit `-m` Query root did
      not expose the wrapper app's related blueprint/toolchain entrypoints
- diagnosis after the direct rerun:
  - explicit `-m` still arrives as connect-time module loading via
    `ExtraModules`; that part remains correct
  - the missing piece was not only CLI focus
  - on the explicit `-m` path, `engine/server/session.go` was serving the
    selected module plus type-only deps, but wrapper-app related modules live on
    `ModuleSource.Blueprint` / `ModuleSource.Toolchains`, not in `Deps`
  - so a wrapper app like `./app` could be loaded explicitly while still
    omitting the related modules that actually contribute the visible Query-root
    entrypoints
- landed fix boundary:
  - keep `-m` as connect-time loading via `ExtraModules`; do not reintroduce a
    second client-side targeting model
  - `engine/server/session.go` now resolves and serves the selected module's
    related blueprint/toolchain modules on the explicit `-m` path before
    serving the primary module
  - `cmd/dagger/module_inspect.go` now resolves the selected module name plus
    visible related module names before CLI introspection
  - `cmd/dagger/mcp.go` now presents explicit `-m` as a filtered real
    `Query`-root view based on those visible module names, instead of
    refocusing `MainObject` to the module object or falling back to raw
    unfiltered `Query`
  - the filtered Query-root view still omits core `Query` functions and the
    selected module constructor proxy, while preserving namespaced constructor
    and default behavior for explicit module access
  - the retained blueprint, root-field-conflict, and constructor-conflict
    workspace behavior remains the regression guard for this path
- landing hygiene:
  - the debug-only `user_defaults_test.go` logging was dropped before landing
  - tests were kept unchanged

Verification after the explicit `-m` follow-up:

- `dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='Test(WorkspaceLoadLocation|FocusRootModuleFunctions|RewriteQueryRootConstructorArgs)$' --test-verbose`
  - trace: `https://dagger.cloud/dagger/traces/3da702adcba3a138eec8fea8bb65da7f`
  - result: passes
- `dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestUserDefaults/(TestLocalBlueprint|TestLocalToolchain)' --test-verbose`
  - trace: `https://dagger.cloud/dagger/traces/692e8d3ebb818920a1f72a6b0c7a90fa`
  - result: passes
  - important explicit `-m` cases now succeed:
    - `dagger -m ./app call message`
    - `dagger -m ./app call defaults message`
    - both return the expected outer-defaults output
- `dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/(TestBlueprintFunctionsIncludesOtherModules|TestEntrypointProxySkipsRootFieldConflicts|TestEntrypointProxySkipsConstructorArgConflicts)' --test-verbose`
  - trace: `https://dagger.cloud/dagger/traces/91c8c327c0fbef408564cee8c7521963`
  - result: all retained workspace entrypoint tests still pass after the
    explicit `-m` engine change

## User-Visible Breakage In The Foundation PR

These are the expected user-visible breakages even without the follow-up porcelain.

1. Existing current-directory `call` / `functions` / `check` / `generate` commands keep
   the same names, but they now resolve through workspace loading instead of the old
   standalone-module path.
2. In multi-module or blueprint-shaped repos, the engine's default-module choice can
   change which module the user sees first.
3. `dagger functions` can show sibling workspace module entrypoints in addition to the
   focused module's functions.
4. `dagger check` and `dagger generate` run against `CurrentWorkspace()` groups, so the
   effective check/generator set can become broader than the old single-module view.
5. Legacy `dagger.json` projects stop failing fast for migration and instead run in
   compat mode with warnings.
6. Workspace binding becomes part of runtime identity, so functions that depend on
   workspace context can observe changed resolution behavior and cache boundaries.
7. All of the above arrive under old command names, without the new explicit workspace
   UX for targeting or management.
