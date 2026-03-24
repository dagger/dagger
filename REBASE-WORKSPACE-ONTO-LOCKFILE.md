# Rebase `workspace` Onto `lockfile`

## Status

Draft handoff plan, derived from the completed `workspace-plumbing` replay and
adapted for the newer `lockfile` base.

This file is intentionally operational, not canonical product policy.

Primary source material now lives in:

- [REBASE-WORKSPACE-ONTO-LOCKFILE.md](/Users/shykes/git/github.com/dagger/dagger_workspace/REBASE-WORKSPACE-ONTO-LOCKFILE.md)
- [REBASE-WORKSPACE-ONTO-PLUMBING.md](/Users/shykes/git/github.com/dagger/dagger_workspace/REBASE-WORKSPACE-ONTO-PLUMBING.md)
- `/Users/shykes/git/github.com/dagger/dagger_lockfile/hack/designs/lockfile.md`

Historical detailed replay notes also exist in the old scratch replay branch:

- `/private/tmp/workspace-on-plumbing/hack/designs/workspace-foundation-compat.md`

That scratch file is useful, but this document is intended to be sufficient even
if that worktree disappears.

## Why This Exists

`lockfile` was spun out after the `workspace-plumbing` replay work had already
classified and replayed a large subset of `workspace`.

That changes the integration target, but it does not invalidate the replay
work. The right move now is:

- reuse the proven replay bucketing work
- treat `lockfile` as the new base branch
- drop the generic lockfile buckets that `lockfile` now owns
- reslice the remaining workspace-authored lock behavior on top

This file exists to make that handoff explicit.

## Starting Facts

Snapshot taken on 2026-03-24:

- `workspace` worktree:
  - `/Users/shykes/git/github.com/dagger/dagger_workspace`
- `lockfile` worktree:
  - `/Users/shykes/git/github.com/dagger/dagger_lockfile`
- existing replay worktree:
  - `/private/tmp/workspace-on-plumbing`

Branch facts at time of analysis:

- `lockfile` is 7 commits ahead of `origin/workspace-plumbing`
- `lockfile` changes 54 files, `+6187/-295`
- current replay branch `tmp/workspace-on-plumbing` changes 87 files over
  `origin/workspace-plumbing`
- overlap between `lockfile` and the plumbing replay:
  - 43 changed files overlap
  - only 9 overlapping files are byte-identical
  - 34 overlapping files changed the same areas differently

Interpretation:

- do not attempt a literal `git rebase workspace onto lockfile`
- do not attempt a literal `git rebase tmp/workspace-on-plumbing onto lockfile`
- perform a fresh curated replay from `lockfile`

## Core Conclusion

The `workspace-plumbing` replay remains the best integration guide, but not as a
commit-for-commit script.

Use it as:

- the classification ledger
- the bucket order
- the verifier matrix
- the record of what was already proven reviewable

Do not use it as:

- a history-preservation target
- a lockfile API source of truth
- a reason to replay stale `strict|auto|update` semantics

## What `lockfile` Already Owns

The new base already contains the generic lockfile foundation:

- flat lock tuple substrate in `util/lockfile`
- authoritative lock modes:
  - `disabled`
  - `live`
  - `pinned`
  - `frozen`
- compatibility aliases for old experimental names
- CLI/global `--lock` plumbing
- nested runtime/client lock-mode propagation
- generic lookup consumers:
  - `container.from`
  - Git lookup locking
- low-level workspace lock refresh:
  - `currentWorkspace.update(): Changeset!`
  - `dagger lock update`
- generated SDK/docs for that surface

Examples in the new base:

- `/Users/shykes/git/github.com/dagger/dagger_lockfile/core/workspace/lock.go`
- `/Users/shykes/git/github.com/dagger/dagger_lockfile/core/schema/lockfile.go`
- `/Users/shykes/git/github.com/dagger/dagger_lockfile/core/schema/workspace.go`
- `/Users/shykes/git/github.com/dagger/dagger_lockfile/cmd/dagger/lock.go`
- `/Users/shykes/git/github.com/dagger/dagger_lockfile/hack/designs/lockfile.md`

## What Was Not Spun Out

Per the `lockfile` design doc and branch contents, the remaining workspace-owned
lock story is still out of tree.

That includes:

- `modules.resolve` entries in `.dagger/lock`
- workspace-config-backed module loading using those pins
- migration emitting workspace-owned lock entries
- any higher-level workspace UX layered on top of the generic lock substrate

This is the remaining lock-related scope that still belongs to the workspace
replay.

## Main Trap To Avoid

The current plumbing replay contains stale lockfile shape in several places.

Do not replay these old semantics on top of `lockfile`:

- old lock mode names:
  - `strict`
  - `auto`
  - `update`
- old result-envelope lock entry shape
- old reuse of `currentWorkspace.update` for selective workspace module updates
- old CLI shape that removed `dagger lock update`

Concrete collisions:

- replay branch:
  - `/private/tmp/workspace-on-plumbing/core/workspace/lock.go`
- new base:
  - `/Users/shykes/git/github.com/dagger/dagger_lockfile/core/workspace/lock.go`

- replay branch:
  - `/private/tmp/workspace-on-plumbing/core/schema/workspace.go`
- new base:
  - `/Users/shykes/git/github.com/dagger/dagger_lockfile/core/schema/workspace.go`

- replay branch:
  - `/private/tmp/workspace-on-plumbing/cmd/dagger/workspace.go`
- new base:
  - `/Users/shykes/git/github.com/dagger/dagger_lockfile/cmd/dagger/lock.go`

The new base is authoritative. Adapt workspace behavior to it.

## Best Handoff Shape

Hand the next engineer a fresh disposable branch from `lockfile`, not an
already-conflicted rebase.

Suggested setup:

```sh
git fetch origin
git worktree add /private/tmp/workspace-on-lockfile -b tmp/workspace-on-lockfile lockfile
cd /private/tmp/workspace-on-lockfile
env -u DAGGER_CLOUD_ENGINE git status --short --branch
```

Then replay in buckets.

## Replay Strategy

### 1. Reuse The Existing Replay Classification

The plumbing replay already paid the expensive classification cost. Reuse:

- bucket boundaries
- which changes were reviewable
- which changes were intentionally deferred
- which verification commands were meaningful

The old replay ledger is still a useful cross-check, but the minimum reusable
classification is copied below so a new engineer is not blocked on that scratch
worktree.

### 1a. Safe Buckets Already Proven On `workspace-plumbing`

These buckets were already replayed successfully and are still good candidates
to bring over early:

- CLI workspace-target parsing and inference for:
  - `dagger check`
  - `dagger generate`
  - `dagger call`
  - `dagger functions`
- sibling workspace-module traversal/listing for `dagger functions`
- `dagger workspace info`
- workspace initialized/config detection
- `Workspace.init` and `dagger workspace init`
- workspace config model
- `dagger workspace config` read/write surface
- `Workspace.install` and top-level install split
- `dagger workspace list`
- engine-routed `dagger module init`
- module install/update command split
- local `dagger migrate`

### 1b. Old Replay Buckets That Now Belong To `lockfile`

These were replayed successfully before, but should now be treated as base
behavior rather than replay targets:

- lockfile substrate and wrappers
- generic lookup tuple helpers
- generic lock mode propagation
- `container.from` generic lookup locking
- global `--lock` plumbing
- `currentWorkspace.update(): Changeset!`
- `dagger lock update`
- generated/docs refreshes whose only purpose was to surface the generic
  lockfile API

### 1c. Workspace-Owned Lock Buckets Still Needed

These are the still-live lock-related buckets after the spinout:

- persist `modules.resolve` pins during workspace install
- share `modules.resolve` lookup rules in `core/workspace`
- load config-owned workspace modules with those pins in engine sessions
- emit `modules.resolve` entries during migration
- decide the higher-level selective refresh UX, if still wanted

### 2. Replay Safe Non-Lock Buckets First

These buckets were already carved cleanly and should be replayable with little
or no adaptation:

- CLI workspace-target parsing and inference
- sibling workspace-module traversal for `dagger functions`
- `dagger workspace info`
- initialized-workspace detection
- `Workspace.init` plus `dagger workspace init`
- workspace config model and config read/write
- top-level install split and workspace install base
- module init/license work
- workspace list
- migrate helpers and `dagger migrate`

Practical breakpoint:

- replay through the install split
- stop before the first old lockfile bucket

On the plumbing replay branch, that breakpoint was immediately before:

- `e3145127c` `workspace: add lockfile model and workspace wrappers`

### 3. Drop Generic Lockfile Buckets Entirely

Do not cherry-pick these ideas from the plumbing replay. They are now base
material or stale:

- `workspace: add lockfile model and workspace wrappers`
- `lockfile: reject unordered map inputs`
- `engine: plumb workspace lock mode through clients`
- `core/schema: add workspace lock update mutation`
- `cmd/dagger: add workspace update command`
- `cmd/dagger: route update through workspace mode`
- `core/workspace: share generic lookup tuples`
- `core/schema: lock container.from lookups`
- `cmd/dagger: add global --lock flag`
- any generated/doc commits whose purpose was only to reflect the generic
  lockfile API

Reason:

- `lockfile` already owns these behaviors or supersedes them with newer
  semantics

### 4. Rewrite The Remaining Workspace-Owned Lock Buckets

These still matter, but must be rewritten on top of `lockfile` instead of
cherry-picked:

- workspace install persisting `modules.resolve` pins
- shared `modules.resolve` resolution rules in `core/workspace`
- engine/session loading workspace config modules with remote lock pins
- migration emitting `modules.resolve` entries

These were represented in the plumbing replay by commits such as:

- `8d1adcba7` `core/schema: persist workspace install lock entries`
- `9f41d8365` `core/workspace: share module lookup lock rules`
- `2675ed8ee` `engine/server: load workspace config modules with lock pins`
- `5cae712ce` `core/workspace: add local migration helpers`

Carry the intent, not the patch shape.

### 5. Re-Decide Selective Workspace Update UX

The old replay used `Workspace.update(modules: ...)` and routed top-level
`dagger update` through it.

That exact shape now conflicts with the new base, because `lockfile` already
uses:

- `currentWorkspace.update(): Changeset!`
- `dagger lock update`

So the selective workspace-module refresh story must be re-decided before code
replay.

The important handoff instruction is simple:

- do not reintroduce the old `Workspace.update(modules: ...)` API as-is

If the higher-level UX is still wanted, give it a new shape that layers on top
of the `lockfile` base instead of overwriting it.

### 6. Regenerate Late

Do not cherry-pick generated SDK/docs commits from the plumbing replay.

After the new lockfile-based replay stabilizes:

- regenerate schema-derived SDKs
- regenerate docs
- verify against the final branch state

This avoids carrying stale lockfile surface churn back into the new branch.

## Concrete Reuse From The Plumbing Replay

The following things should be copied directly into the new handoff branch or
notes as needed:

- the bucket ordering
- the known-good verifier commands
- the known invalid verifier paths on Darwin
- the edge cases already found during replay
- the list of intentionally deferred suspicious areas outside the workspace
  foundation slice

Most useful concrete reuse:

- the commit list in the replay ledger
- the per-bucket test commands in the replay ledger
- the fact that local `go test ./cmd/dagger` and some local `go test ./core/schema`
  paths are not meaningful on this Darwin host
- the rule to always unset `DAGGER_CLOUD_ENGINE` in verification commands

## Verifier Guidance

Reuse the replay verifier discipline:

- prefer targeted repo-native `engine-dev` test runs
- use local package tests only where they are actually valid
- avoid treating Darwin-local `cmd/dagger` and `core/schema` failures as branch
  regressions when they are really Linux-only build issues
- run with `env -u DAGGER_CLOUD_ENGINE`

Useful commands copied forward from the earlier replay:

```sh
env -u DAGGER_CLOUD_ENGINE go test ./core/workspace
env -u DAGGER_CLOUD_ENGINE go test ./util/lockfile
env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./core/schema -o /tmp/.tmp-core-schema.test
env -u DAGGER_CLOUD_ENGINE GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./cmd/dagger -o /tmp/.tmp-cmd-dagger.test
env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='Test(ParseChecksTargetArgs|ParseGenerateTargetArgs|ParseCallTargetArgs|ParseFunctionsTargetArgs|StripHelpArgs|FindSiblingEntrypoint|FunctionListRunIncludesSiblingEntrypoints|WorkspaceLoadLocation|WriteWorkspaceInfo)$'
env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./engine/server --run='Test(WorkspaceBindingMode|EnsureWorkspaceLoadedInheritsParentWorkspace|EnsureWorkspaceLoadedKeepsExistingWorkspaceBinding|PendingLegacyModule)$'
env -u DAGGER_CLOUD_ENGINE dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestWorkspace/TestMigrate$'
```

Still true from the earlier replay:

- local `go test ./cmd/dagger` is not a meaningful verifier on this Darwin host
- local `go test ./core/schema` is also not a meaningful verifier here once
  Linux-only engine/buildkit code is pulled in
- prefer targeted `engine-dev` runs for behavior changes

## Suggested First Execution Pass

The next engineer should make the first pass intentionally small:

1. create `tmp/workspace-on-lockfile` from `lockfile`
2. copy in a short replay ledger for the new branch
3. replay the safe pre-lock buckets only
4. stop at the first `modules.resolve` bucket
5. redesign that bucket against the new lockfile API before proceeding

This keeps the first review legible and avoids burning time in a large
multi-conflict rebase.

## Minimal Success Criteria For The Handoff

The handoff is good enough if the next engineer can start from disk alone and
answer these questions quickly:

- what branch should I start from
- what prior replay work is reusable
- which lockfile buckets must be dropped
- which lockfile buckets must be rewritten
- where is the API collision
- what should I verify first

If any of those answers still requires reconstructing chat history, this
handoff failed.
