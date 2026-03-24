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

- `origin/workspace-plumbing`:
  - `4e9d517dc` `fix(test): use constructor then method call in no-sdk shell test`
- `origin/lockfile`:
  - `b68344fda` `generate: refresh generated lockfile APIs`
- `lockfile` is 7 commits ahead of `origin/workspace-plumbing`
- `lockfile` changes 40 files, `+3631/-80`
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

## Process Lessons From The Plumbing Replay

These lessons were learned while replaying onto `workspace-plumbing`, but they
still apply on top of `lockfile`.

### Preserve Behavior, Not Commit Topology

The most important practical lesson was that chronological replay is the wrong
tool.

- old merge-from-`main` commits were replay noise
- early foundation commits were often stale by shape even when their intent was
  still valid
- replaying by semantic bucket was much faster than resolving history-shaped
  conflicts

Rule:

- if a commit conflicts because the base evolved the same area differently, stop
  and reslice by intent instead of forcing the old patch through

### Keep The Ledger On Disk

The replay only stayed tractable because progress was recorded after each bucket.

For the new replay, keep an on-disk ledger that records:

- current branch name and base commit
- completed buckets
- dropped buckets
- rewritten buckets
- exact verifier commands run
- known verifier failures that are environmental rather than branch regressions

Do not rely on chat history as the handoff medium.

### Keep Commits Small And Reviewable

The successful replay buckets were the ones that produced legible review units.

Prefer:

- one semantic code bucket per commit
- a separate doc/ledger commit when the notes are substantial
- generated output in late commits, after the underlying API is settled

Avoid:

- giant mixed commits that combine engine, CLI, tests, generated code, and docs
  unless they are inseparable

### Generate Late

This held on plumbing and still holds on `lockfile`.

- generated SDK/docs commits create noise during conflict resolution
- they should come after the API shape is stable
- they should not drive the replay order

### Expect Hot Conflict Files

Even with bucketed replay, some files are repeat conflict magnets:

- `engine/server/session.go`
- `core/schema/workspace.go`
- `core/workspace/lock.go`
- `cmd/dagger/main.go`
- `cmd/dagger/module.go`

Treat edits in those files as design work, not routine cherry-picks.

### Verify Narrowly After Each Bucket

Broad end-to-end verification is too expensive to use as the first signal.

What worked during the plumbing replay:

- run the smallest repo-native test that proves the bucket
- use local package tests only where they are actually valid
- defer broader sweeps until a group of related buckets is stable

This is especially important on this Darwin host, where some local Go test
paths are not meaningful because Linux-only engine/buildkit code is pulled in.

### Treat Verifier Cache Problems As A Separate Class Of Failure

One replay got derailed by local verifier contamination rather than a code
regression. That can happen again.

Rule:

- if a broad verifier fails in a way that references the wrong worktree or a
  stale engine-dev source path, record it as an environmental blocker until it
  is reproduced cleanly

Do not let a bad local cache force unnecessary code surgery.

### Preserve Base Truth Before Replaying New Work

The safest pattern was:

1. understand what the base branch already proved
2. replay only the missing intent
3. revalidate the touched behavior

That remains the right pattern on `lockfile`.

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

## New Upstream Principle From `lockfile`

Since the earlier handoff draft, the `lockfile` design doc added an explicit
implementation principle that matters for the remaining workspace replay.

Principle:

- new lockfile consumers should attach to existing lookup resolution flows
- do not add new engine hooks whose only purpose is lock integration

Practical consequence for the workspace replay:

- when replaying `modules.resolve`, wire lock read/write behavior into the
  existing module resolution path
- do not treat `modules.resolve` as a reason to invent a parallel lock-specific
  resolution API

This is important enough to treat as base-branch guidance, not an optional
refactor preference.

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

Additional rule from the current `lockfile` branch:

- integrate `modules.resolve` into the existing module resolution flow rather
  than creating new lock-specific engine plumbing

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
