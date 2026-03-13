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
  - legacy `defaultPath` failures are currently blocked by the root-`/`
    sandbox bug in
    [pathutil.go](/Users/shykes/git/github.com/dagger/dagger_workspace/engine/client/pathutil/pathutil.go),
    not by a mismatch in the new Workspace path semantics

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
  - confirmed failing subtest:
    - `TestUserDefaults/TestLocalBlueprint/inner_envfile`
  - exact failure:
    - expected: `salut-inner, monde-inner!`
    - actual: `hello, world!`
  - observed runtime signal:
    - the engine warns `module "defaults" uses legacy-default-path; port to
      workspace API and remove this flag`
    - that means the legacy blueprint module is loading, but the caller's
      `.env` defaults are not being applied to it
  - `test-base` also reported
    `TestUserDefaults/TestLocalToolchain/outer_envfile_outer_workdir`; based on
    the test shape, that likely belongs to the same dependency-default
    propagation bug family
  - known cause:
    - precise code location still needs root-cause analysis
    - behaviorally, user defaults from the caller/module scope are not being
      propagated through the legacy blueprint/toolchain compatibility path
  - current plan:
    - fix this in the existing workspace/session/legacy loading path
    - do not rewrite the test to accept workspace-specific output
    - do not add a second compat path under `ModuleSource.asModule()`
  - fix stance: `implementation`

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
  - fix stance: `implementation`

Verification used to build this ledger:

- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestBlueprint/TestBlueprintUseLocal/use_local_blueprint' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestChecks/TestChecksAsToolchain/typescript' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestUserDefaults/TestLocalBlueprint/inner_envfile' --test-verbose`
- `dagger --progress=logs call -m ./toolchains/engine-dev test --pkg=./core/integration --run='TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple' --test-verbose`
- `env GOCACHE=/tmp/go-build go test ./cmd/codegen/generator/typescript/templates -count=1`
- `env GOCACHE=/tmp/go-build go test ./cmd/codegen/generator/typescript/templates -count=5`

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
