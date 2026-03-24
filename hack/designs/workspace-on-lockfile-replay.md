# Workspace Replay Onto Lockfile

## Status

Active replay ledger for the fresh curated `workspace` replay on top of
`lockfile`.

This file is operational. Update it after every substantive bucket so the replay
can be resumed from disk alone.

## Branch Contract

Current integration branch:

- `tmp/workspace-on-lockfile`
- worktree:
  `/Users/shykes/git/github.com/dagger/dagger_workspace-on-lockfile`
- base:
  - branch: `origin/lockfile`
  - commit: `b68344fda` `generate: refresh generated lockfile APIs`

Reference oracles:

- brief:
  [REBASE-WORKSPACE-ONTO-LOCKFILE.md](/Users/shykes/git/github.com/dagger/dagger_workspace/REBASE-WORKSPACE-ONTO-LOCKFILE.md)
- older replay ledger:
  [workspace-foundation-compat.md](/private/tmp/workspace-on-plumbing/hack/designs/workspace-foundation-compat.md)
- branch oracle:
  `origin/workspace-plumbing`

Important frame:

- this is a fresh curated replay from `lockfile`
- this is not a mechanical rebase of `workspace`
- this is not a replay of the old plumbing branch history
- reuse the old replay's bucket discipline, not its stale lockfile patch shapes

## Base Truth

`lockfile` now owns the generic lock substrate and generic persisted module
source lookup primitive.

That includes:

- `util/lockfile`
- lock modes:
  - `disabled`
  - `live`
  - `pinned`
  - `frozen`
- compatibility aliases for `auto` / `strict` / `update`
- generic lookup locking for:
  - `container.from`
  - git lookup locking
  - module source resolution via `dag.ModuleSource()` / `modules.resolve`
- `currentWorkspace.update(): Changeset!`
- `dagger lock update`

Replay rule:

- workspace must consume the existing generic module-source lookup path
- do not rebuild a workspace-specific `modules.resolve` substrate
- do not restore the old `Workspace.update(modules: ...)` API as-is

## First Execution Pass

Planned first pass:

1. replay safe pre-lock buckets only
2. stop at the first bucket that used old workspace-owned `modules.resolve`
3. redesign that bucket against the `lockfile` base before continuing

First bucket to land:

- CLI workspace-target parsing and inference for:
  - `dagger check`
  - `dagger generate`
  - `dagger call`
  - `dagger functions`

Reason:

- pre-lock
- reviewable
- low conflict risk
- easy to verify in isolation

## Bucket Ledger

### Completed Buckets

- CLI workspace-target parsing and inference for:
  - `dagger check`
  - `dagger generate`
  - `dagger call`
  - `dagger functions`
- sibling workspace-module traversal/listing for `dagger functions`
- `dagger workspace info`
- initialized-workspace detection via `.dagger/config.toml`
- `Workspace.init` plus `dagger workspace init`
- workspace config model
- `dagger workspace config` read/write
- `Workspace.install` base mutation
- workspace-aware top-level `dagger install` routing
- `dagger workspace list`
- engine-routed `dagger module init`
- module install/update command split
- local `dagger migrate`
- engine/session loading of config-owned workspace modules on top of the
  generic `ModuleSource` / `modules.resolve` path
- workspace install populating `.dagger/lock` through the generic
  `ModuleSource` / `modules.resolve` lookup path

### Pending Safe Pre-Lock Buckets

- none

### Explicitly Dropped Buckets

These were replayed onto `workspace-plumbing`, but now belong to `lockfile` or
are superseded by it:

- `workspace: add lockfile model and workspace wrappers`
- `lockfile: reject unordered map inputs`
- `engine: plumb workspace lock mode through clients`
- `core/schema: add workspace lock update mutation`
- `cmd/dagger: add workspace update command`
- `cmd/dagger: route update through workspace mode`
- `core/workspace: share generic lookup tuples`
- `core/schema: lock container.from lookups`
- `cmd/dagger: add global --lock flag`
- generated/docs refreshes whose only purpose was the generic lockfile API

### Rewrite Buckets

These intents still belong to workspace, but must be rewritten on top of the
`lockfile` base:

- migration should emit or refresh the same generic module-source lookups
- any selective refresh UX must layer on top of `currentWorkspace.update()` /
  `dagger lock update`, not replace them

## Stale Oracle Notes

The old replay remains useful for bucketing and verifier history, but it is
stale in these places:

- `core/workspace/lock.go`
  - old replay still owns `modules.resolve` locally
  - old replay still thinks in `strict|auto|update`
- `core/schema/workspace.go`
  - old replay manually persists module lock entries
  - old replay exposes `Workspace.update(modules: ...)`
- `cmd/dagger/workspace.go`
  - old replay reintroduces workspace-specific update UX
- `engine/server/session.go`
  - old replay applies workspace lock pins directly instead of consuming the
    generic `ModuleSource` lookup flow

Authoritative base files now live in the `lockfile` worktree:

- `/Users/shykes/git/github.com/dagger/dagger_lockfile/core/workspace/lock.go`
- `/Users/shykes/git/github.com/dagger/dagger_lockfile/core/schema/modulesource.go`
- `/Users/shykes/git/github.com/dagger/dagger_lockfile/core/schema/workspace.go`
- `/Users/shykes/git/github.com/dagger/dagger_lockfile/cmd/dagger/lock.go`

## Verification Guidance

Always run with:

- `env -u DAGGER_CLOUD_ENGINE`

Useful first verifiers for the initial CLI-targeting bucket:

```sh
env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='Test(ParseChecksTargetArgs|ParseGenerateTargetArgs|ParseCallTargetArgs|ParseFunctionsTargetArgs|StripHelpArgs)$'
env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test
```

Known host caveats:

- local `go test ./cmd/dagger` is not a meaningful verifier on this Darwin host
- local `go test ./core/schema` is also not a meaningful verifier once Linux-only
  engine/buildkit code is pulled in

## Progress Log

- 2026-03-24:
  - created worktree
    `/Users/shykes/git/github.com/dagger/dagger_workspace-on-lockfile`
  - created branch `tmp/workspace-on-lockfile` from `origin/lockfile`
  - recorded initial branch contract, bucket plan, drops, rewrites, and first
    verifiers
  - replayed CLI workspace-target parsing and inference for `check`,
    `generate`, `call`, and `functions`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='Test(ParseChecksTargetArgs|ParseGenerateTargetArgs|ParseCallTargetArgs|ParseFunctionsTargetArgs|StripHelpArgs)$'`
  - trace:
    `https://dagger.cloud/dagger/traces/47a4050626111ef45c047231eb8c348f`
  - replayed sibling workspace-module traversal/listing for `dagger functions`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='Test(ParseChecksTargetArgs|ParseGenerateTargetArgs|ParseCallTargetArgs|ParseFunctionsTargetArgs|StripHelpArgs|FindSiblingEntrypoint|FunctionListRunIncludesSiblingEntrypoints)$'`
  - trace:
    `https://dagger.cloud/dagger/traces/a714146e2d617b0bf30a15e1c36ba377`
  - replayed `dagger workspace info`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='TestWriteWorkspaceInfo$'`
  - trace:
    `https://dagger.cloud/dagger/traces/1da4d95be7485f1b14e907d0e6c53612`
  - replayed initialized-workspace detection via `.dagger/config.toml`
  - rewrite note:
    do not reuse the old `.dagger` boundary heuristic from the plumbing replay;
    `lockfile` now owns `.dagger/lock`, so initialized-workspace detection must
    look for `.dagger/config.toml`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE go test ./core/workspace -run 'TestDetect'`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./engine/server -o /tmp/.tmp-engine-server.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./engine/server --run='Test(EnsureWorkspaceLoadedInheritsParentWorkspace|EnsureWorkspaceLoadedKeepsExistingWorkspaceBinding|WorkspaceBindingMode|BuildCoreWorkspaceIncludesConfigState)$'`
  - trace:
    `https://dagger.cloud/dagger/traces/31880ef073ba622125d9b8dab2a59d8f`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestCurrentWorkspaceInit$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/1bea4dc0f532e20d49dfe0557caf36d1`
  - replayed `Workspace.init` plus `dagger workspace init`
  - rewrite note:
    keep the bucket narrow: add the local-only workspace init mutation and the
    thin CLI wrapper, but do not pull in the later config model or old
    workspace-owned lock plumbing
  - codegen note:
    `go generate` wanted to refresh unrelated Go SDK surface, so keep the
    bucket legible by patching only the minimal `Workspace.Init` client method
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./core/schema -o /tmp/.tmp-core-schema.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/Test(CurrentWorkspaceInit|WorkspaceInitCommand)$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/ce6a8ec60e4250a5d944abed759f495e`
  - replayed the pure workspace config model in `core/workspace`
  - rewrite note:
    keep this bucket schema-free and CLI-free; land only deterministic
    parse/serialize/read/write utilities and tests so the later
    `workspace config` UX can stay thin
  - verifier passed:
    `go test ./core/workspace -run 'Test(ParseConfig|SerializeConfig|ReadConfigValue|WriteConfigValue)$' -count=1`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./core/workspace -o /tmp/.tmp-core-workspace.test`
  - replayed `dagger workspace config` read/write
  - design note:
    require an initialized workspace; `workspace config` does not implicitly
    create `.dagger/config.toml`
  - codegen note:
    keep the bucket legible by patching only the minimal Go SDK
    `Workspace.ConfigRead` / `Workspace.ConfigWrite` surface instead of running
    full generation
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./core/schema -o /tmp/.tmp-core-schema.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestWorkspaceConfig(Read|Write|RequiresInit)$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/49bba36802eca6773f6b5e8c93944553`
  - replayed the base `Workspace.install` mutation in the engine/schema layer
  - design note:
    keep this bucket narrow: initialize `.dagger/config.toml` if missing,
    resolve module identity through the existing generic `moduleSource` path,
    rewrite local refs relative to `.dagger`, and write config through the
    shared workspace config helpers
  - rewrite note:
    do not pull lock persistence, selective refresh, or workspace-specific
    `modules.resolve` plumbing into this bucket; `lockfile` owns that base
  - codegen note:
    keep the bucket legible by patching only the minimal Go SDK
    `Workspace.Install` surface instead of running full generation
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./core/schema -o /tmp/.tmp-core-schema.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestCurrentWorkspaceInstall$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/ca515babe72ef7bd2246969984da7df3`
  - replayed workspace-aware top-level `dagger install` routing
  - design note:
    keep the command unified for now, but only as a thin router: preserve
    normal module dependency installs when the current or explicit `--mod`
    target is a module, and fall back to `CurrentWorkspace().Install(...)`
    only when no module target exists
  - design note:
    reject `--compat` in workspace-install mode instead of silently ignoring a
    module-only flag
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/Test(CurrentWorkspaceInstall|WorkspaceInstallCommand)$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/4f8e174d71cda956bc68f8743ef4f564`
  - replayed `dagger workspace list`
  - design note:
    keep the bucket read-only and narrow: expose a simple `Workspace.moduleList`
    field over the shared workspace config model, convert local sources back to
    workspace-root-relative paths for display, and keep the CLI as a thin
    formatter
  - codegen note:
    keep the bucket legible by patching only the minimal Go SDK
    `Workspace.ModuleList` surface instead of reviving the old generated
    load-by-ID object plumbing
  - verifier note:
    the first focused engine test failed because `WorkspaceModule` was not yet
    installed in the GraphQL schema; fix that registration and rerun the same
    verifier
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./core/schema -o /tmp/.tmp-core-schema.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestWorkspaceList$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/bd48931245ea07ee4a3a3ce011000527`
  - replayed engine-routed `dagger module init` for initialized workspaces
  - design note:
    keep the current CLI shape and make it a thin router: with no explicit path
    and an initialized workspace, create the module under
    `.dagger/modules/<name>` and auto-install it; with an explicit path, keep
    standalone module init behavior
  - design note:
    keep license creation in the CLI wrapper instead of widening the workspace
    GraphQL mutation with host-specific license search semantics
  - rewrite note:
    this bucket only covers workspace-owned module creation and installation;
    it does not claim the later config-owned module loading/session behavior
  - verifier note:
    the first focused engine test failed because `requireKind` was passed to
    `moduleSource` as a bare enum instead of `dagql.Opt(...)`; fix the selector
    input shape and rerun the same verifier
  - verifier note:
    the first version of the integration test also overreached into
    config-owned module loading by calling `dagger call mymod greet`; narrow it
    back to init-specific assertions and leave config-owned loading for the
    later rewrite bucket
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./core/schema -o /tmp/.tmp-core-schema.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestWorkspaceModuleInitCommand$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/a70af3c4899e62edfc4945a319f4c18d`
  - replayed explicit `dagger module install` / `dagger module update`
    commands without changing the workspace-aware top-level `dagger install`
    path
  - design note:
    keep the new nested commands thin and explicit: `dagger module install`
    always mutates a module's `dagger.json`, while top-level `dagger install`
    remains the workspace-or-module router from the earlier bucket
  - design note:
    share local-module mutation helpers across top-level and nested commands so
    the replay adds command shape, not a second implementation
  - verifier note:
    the first focused workspace verifier failed because the earlier
    workspace-aware `dagger init` bucket changed setup semantics inside an
    initialized workspace; fix the tests to pass an explicit `.` path when
    creating standalone modules under workspace subdirectories
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/(TestWorkspaceInstallCommand|TestModuleInstallCommand|TestModuleUpdateCommand)$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/e512f8e8bc3ccf7382522b5988f82199`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='TestSpanName$'`
  - trace:
    `https://dagger.cloud/dagger/traces/9b596ae2d4097764ebb12dd661906bc2`
  - replayed local `dagger migrate`
  - design note:
    keep `migrate` as its own top-level CLI file (`cmd/dagger/migrate.go`)
    instead of burying a non-workspace subcommand inside `workspace.go`
  - rewrite note:
    the old plumbing replay silently dropped standalone sdk-backed root modules
    during migration unless toolchains were also present; this replay always
    materializes the project module into `.dagger/modules/<name>` when a
    legacy sdk-backed module is migrated, even if its source remains at the
    project root
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `go test ./core/workspace -run 'TestMigrate' -count=1`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger-migrate.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='Test(DetectMigrationTarget|FindMigratableModuleConfigs)$'`
  - trace:
    `https://dagger.cloud/dagger/traces/adb63fc6bac207b8a7244fa1ab0c658c`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestMigrate$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/19ce8adb4010457919b09890f58b959e`
  - replayed config-owned workspace module loading in `engine/server/session.go`
  - design note:
    session startup now parses `.dagger/config.toml`, turns config entries into
    pending modules, and lets `dag.moduleSource()` own remote pinning and lock
    writes; `engine/server` does not reintroduce workspace-owned lock
    resolution logic
  - design note:
    initialized workspace config now suppresses legacy `dagger.json`
    toolchain/blueprint enrichment, while nested standalone modules outside the
    managed `.dagger/modules` tree still override inherited workspace modules
  - rewrite note:
    keep workspace config refs raw for remote modules and pass them through
    `moduleSource(refString:, disableFindUp: true)` so the generic
    `modules.resolve` path applies the current lock mode instead of a
    workspace-specific pre-resolution step
  - rewrite note:
    preserve `defaults_from_dotenv` even when a config-owned module has no
    explicit `[modules.<name>.config]` table by applying empty workspace config
    metadata at load time
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./engine/server --run='Test(PendingLegacyModule|WorkspaceConfigPendingModules|ApplyPendingModuleMetadata)$'`
  - trace:
    `https://dagger.cloud/dagger/traces/d10268482f7d37277c3821543da049e8`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestWorkspaceModuleInitCommand$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/9bc3714fac40271d6d70729038b60897`
  - replayed workspace install lock persistence through
    `dag.ModuleSource()` / `modules.resolve`
  - design note:
    workspace install now defaults its own lookup context to `pinned` only
    when the caller did not specify a lock mode, so default installs populate
    `.dagger/lock` without overriding explicit `--lock` behavior
  - rewrite note:
    do not manually write install lock entries in workspace code; use the
    existing generic `moduleSource` lookup persistence path
  - verifier passed:
    `git diff --check`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./core/schema -o /tmp/.tmp-core-schema-workspace-install.test`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/schema --run='Test(CurrentLookupLockMode|WorkspaceInstallLookupContext)$'`
  - trace:
    `https://dagger.cloud/dagger/traces/7bcf5ac25c96736b168175f396518f7d`
  - verifier passed:
    `env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/Test(CurrentWorkspaceInstall|WorkspaceInstallCommand)$' --test-verbose`
  - trace:
    `https://dagger.cloud/dagger/traces/75a668ee9ce9eceb08642d6734aaffca`
