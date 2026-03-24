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

### Pending Safe Pre-Lock Buckets

- sibling workspace-module traversal/listing for `dagger functions`
- `dagger workspace info`
- initialized-workspace detection
- `Workspace.init` plus `dagger workspace init`
- workspace config model
- `dagger workspace config` read/write
- top-level install split and workspace install base
- `dagger workspace list`
- engine-routed `dagger module init`
- module install/update command split
- local `dagger migrate`

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

- workspace install should populate `.dagger/lock` by using the generic
  `ModuleSource` lookup path
- engine/session loading of config-owned workspace modules should consume the
  persisted generic module-source lookups
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
