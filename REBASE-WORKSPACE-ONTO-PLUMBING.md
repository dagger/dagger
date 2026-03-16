# Rebase `workspace` Onto `workspace-plumbing`

## Status

Draft operational plan.

This file is not canonical branch policy. The canonical plumbing ledger remains `workspace-plumbing:hack/designs/workspace-foundation-compat.md`

This file exists to make the later rebase tractable.

## Goal

Move the remaining `workspace` work onto `workspace-plumbing` without regressing the
plumbing branch's verified compatibility/runtime fixes or reintroducing deferred
porcelain UX by accident.

The target outcome is not a literal preservation of `workspace` commit topology.
The target outcome is:

- preserve the verified `workspace-plumbing` behavior
- replay the still-wanted `workspace` work on top
- drop or rewrite obsolete historical merge noise

## Inputs

- Current merge-base:
  - `dcb1b99c7898c4ca61ce4858a13697d9713bc1be`
- Branch distance at time of planning:
  - `workspace-plumbing`: 58 commits ahead of merge-base
  - `workspace`: 311 commits ahead of merge-base
- Canonical ledger:
  - `workspace-plumbing:hack/designs/workspace-foundation-compat.md`

## Key Ledger Rules

From `workspace-foundation-compat.md`:

- Preserve verified behavior, not current patch shape or commit topology.
- Every `workspace` change must land in exactly one bucket:
  - lands in PR A
  - lands in PR B
  - intentionally dropped
- Do not preserve docs-only checkpoint commits for their own sake.
- Do not spend effort preserving temporary client-side entrypoint workarounds that
  the upstream schema-level entrypoint design supersedes.

## Dry-Run Results

Three scratch experiments were run.

### 1. Full `--rebase-merges` from `workspace`

Command shape:

```sh
git rebase --rebase-merges --onto workspace-plumbing <merge-base>
```

Result:

- Rebasing progressed through 313 of 314 steps.
- The first stop was the top merge commit:
  - `eeea90802` `Merge branch 'main' into workspace`
- Conflict count at that stop: 40 files.
- Conflict cluster was concentrated in:
  - `cmd/dagger/*`
  - `core/schema/workspace.go`
  - `core/workspace/*`
  - `engine/server/session.go`
  - generated docs/SDK outputs
- Skipping that merge commit let the rebase finish.

Interpretation:

- Historical merge-from-`main` commits are replay noise.
- They should not be treated as meaningful preservation points.

### 2. `--rebase-merges` from pre-merge `workspace` tip

Base used:

- `bbdfa797e` `workspace: move entrypoints to Query root`

Result:

- Rebasing progressed through 302 of 313 steps.
- The first stop was again a historical merge-from-`main` commit:
  - `e50a37ca0` `Merge branch 'main' into workspace`
- Skipping that merge let the rebase finish.

Interpretation:

- Same result as above.
- If merge commits are preserved mechanically, the rebase mostly burns time
  replaying old `main` merges.

### 3. Plain linear rebase from pre-merge `workspace` tip

Command shape:

```sh
git rebase --onto workspace-plumbing <merge-base>
```

Result:

- Real semantic conflict arrived immediately at:
  - `c8fc9d728` `workspace: add config parser and detection logic`
  - conflict file: `core/workspace/detect.go`
- After skipping that, the next real conflict arrived immediately at:
  - `bd6d55956` `engine: load workspace modules at connect time`
  - conflict file: `engine/server/session.go`

Interpretation:

- A literal chronological replay of early `workspace` foundation commits is wrong.
- `workspace-plumbing` intentionally rolled back initialized-workspace/config
  parsing and old connect-time loading shape.
- The eventual integration should be bucketed and rewritten, not replayed blindly.

## Working Conclusion

Do not run a naive commit-for-commit rebase of `workspace` onto
`workspace-plumbing`.

Instead:

1. start from `workspace-plumbing`
2. use the plumbing ledger as the source of truth
3. replay `workspace` in curated buckets
4. drop historical merge-from-`main` commits
5. merge or rebase fresh `main` later, once the plumbing-based replay is stable

## Same-Intent Work Already Present On Both Branches

These should be reconciled by intent, not replayed blindly:

- `workspace: adopt path contract`
  - `workspace`: `5e0b1e4a7`
  - `workspace-plumbing`: `df14b8416`
- `workspace: move entrypoints to Query root`
  - `workspace`: `bbdfa797e`
  - `workspace-plumbing`: `033c92644`

Near-duplicate intent also exists around:

- path-contract rollout follow-ups
- foundation/compat plumbing
- engine-backed authoring compatibility

## Plumbing Fix Threads That Must Survive

These come directly from `workspace-foundation-compat.md` and should be treated as
non-negotiable until proven superseded by equivalent `workspace` behavior.

### Required To Preserve Or Re-prove

- `cd4c63ff1` `engine: preserve legacy toolchain customizations`
  - proof:
    - `TestToolchain/TestToolchainsWithConfiguration/override_constructor_defaultPath_argument`
- `fec23805b` `core: restore legacy blueprint caller env defaults`
  - proof:
    - `TestUserDefaults/TestLocalBlueprint/inner_envfile`
- `6c93ddba9` `core/schema: restore legacy generator include matching`
  - proof:
    - `TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple`
    - `TestGenerators/TestGeneratorsAsBlueprint/java/generate`
- `1bd6065c7` `engine: seed runtime module content cache`
  - broader than generators; protects runtime contextual dir/file reloads

### Also Treat As Must-Keep Plumbing Behavior

- `13f2abf15` `engine: fix sandboxing under root slash`
- `1adb147a4` `cli: make bare init idempotent`
- `eefd243f0` `cli: route query-root ctor flags to modules`
- `ce32ecb46` `workspace: repair explicit -m entrypoints`
- `31a0f6dad` `test(workspace): fix tests`

These are late plumbing fixes validated against the entrypoint hold bucket and
explicit `-m` behavior. They are exactly the kind of “easy to lose in a replay”
fixes that should be preserved first and then revalidated.

## Proposed Replay Strategy

### Phase 0: Prepare The Integration Branch

Create a fresh branch from `workspace-plumbing`.

Goals:

- preserve plumbing ledger history untouched
- keep the replay branch disposable
- keep fresh notes in this file or a sibling scratch log

Suggested name:

- `tmp/workspace-on-plumbing`

### Phase 1: Lock In Plumbing Truth

Before replaying any `workspace` work:

- verify the plumbing must-keep fixes still exist in the integration base
- mark them explicitly in the replay checklist as protected
- do not start by resolving generated files

This is the point to confirm:

- path contract behavior
- root-`/` sandbox fix
- compat restoration threads
- explicit `-m` entrypoint fixes

### Phase 2: Drop Historical Merge Noise

Do not replay historical `Merge branch 'main' into workspace` commits.

Policy:

- skip all old merge-from-`main` commits during replay
- once the plumbing-based replay is stable, re-merge or rebase against fresh
  `main`

Reason:

- both `--rebase-merges` dry runs only stopped on those merges
- they are not the semantic content we care about preserving

### Phase 3: Replay By Buckets, Not Original Order

#### Bucket A: Drop Or Rewrite Early Foundation Commits That Contradict Plumbing

These are the first commits a naive linear rebase trips over and should be
treated as “rewrite or drop” candidates:

- `c8fc9d728` `workspace: add config parser and detection logic`
- `bd6d55956` `engine: load workspace modules at connect time`

Likely broader set in the same category:

- early config-owned workspace detection/loading
- early initialized-workspace parsing
- old connect-time module loading assumptions

Reason:

- plumbing intentionally narrows PR A away from initialized-workspace/config UX
- replaying these raw commits would reintroduce that older architecture

Action:

- do not cherry-pick these directly
- if any behavior is still needed, port only the minimal surviving logic into
  the plumbing model

#### Bucket B: Drop Same-Intent Duplicates Already Landed On Plumbing

Do not replay these as historical commits:

- `5e0b1e4a7` `workspace: adopt path contract`
- `bbdfa797e` `workspace: move entrypoints to Query root`

Action:

- keep plumbing’s versions
- port only later follow-up behavior that is still missing

#### Bucket C: Preserve Plumbing Compat/Runtime Fixes As The Base Truth

These should be considered “already solved here” unless `workspace` has a better,
verified replacement:

- compat parsing and legacy restoration
- blueprint/toolchain caller env defaults
- generator include matching
- runtime module content cache seeding
- root-`/` sandbox handling
- explicit `-m` entrypoint fixes

Action:

- do not overwrite these accidentally with older `workspace` history
- during replay, resolve conflicts in favor of plumbing semantics unless a newer
  `workspace` change has explicit proof and subsumes the plumbing fix

#### Bucket D: Replay Wanted `workspace` Porcelain Work On Top

This is the big bucket, but it should be replayed after the plumbing truth is
stabilized.

Likely subclusters:

- command UX and authoring flows
- module list
- check/generate/functions targeting semantics
- lockfile/update work
- docs/design cleanup

This work should be replayed top-down by user story, not by old commit date.

#### Bucket E: Regenerate Public Surface Last

Leave these until behavior is settled:

- `docs/docs-graphql/schema.graphqls`
- `docs/static/api/reference/index.html`
- SDK generated files
- PHP static docs

Reason:

- generated fallout is a consequence, not a decision
- conflict noise here is high and low-signal

## Expected Conflict Zones

Highest risk:

- `engine/server/session.go`
- `core/schema/workspace.go`
- `core/workspace/detect.go`
- `core/workspace/legacy.go`
- `core/module.go`
- `core/modulesource.go`
- `core/schema/modulesource.go`
- `cmd/dagger/module.go`
- `cmd/dagger/functions.go`
- `cmd/dagger/call.go`
- `cmd/dagger/checks.go`
- `cmd/dagger/generators.go`
- `cmd/dagger/mcp.go`
- `cmd/dagger/module_inspect.go`

Expected generated fallout:

- `sdk/go/dagger.gen.go`
- `sdk/php/generated/Workspace.php`
- `sdk/python/src/dagger/client/gen.py`
- `sdk/rust/crates/dagger-sdk/src/gen.rs`
- `sdk/typescript/src/api/client.gen.ts`
- `docs/docs-graphql/schema.graphqls`
- `docs/static/api/reference/index.html`

Test fallout likely:

- `core/integration/workspace_test.go`
- `core/schema/workspace_test.go`
- `cmd/dagger/*_test.go`

## Replay Order Recommendation

Recommended high-level order:

1. create integration branch from `workspace-plumbing`
2. record protected plumbing fixes
3. port only still-missing `workspace` semantic deltas after:
   - path contract
   - entrypoint root exposure
   - compat/runtime restorations
4. replay CLI/porcelain work in coherent feature batches
5. replay lockfile/update work
6. regenerate outputs
7. merge fresh `main`
8. run required validation suite

## Required Validation

From the plumbing ledger, these are mandatory:

- `TestToolchain/TestToolchainsWithConfiguration/override_constructor_defaultPath_argument`
- `TestUserDefaults/TestLocalBlueprint/inner_envfile`
- `TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple`
- `TestGenerators/TestGeneratorsAsBlueprint/java/generate`

From the entrypoint hold-bucket notes, also mandatory:

- `TestWorkspace/TestBlueprintFunctionsIncludesOtherModules`
- `TestWorkspace/TestEntrypointProxySkipsRootFieldConflicts`
- `TestWorkspace/TestEntrypointProxySkipsConstructorArgConflicts`
- `TestUserDefaults/TestLocalBlueprint`
- `TestUserDefaults/TestLocalToolchain`

Recommended targeted command style from the ledger:

```sh
dagger --progress=plain call engine-dev test --pkg=./core/integration --run='TestSomething'
```

And for cmd-side checks:

```sh
dagger --progress=plain call engine-dev test --pkg=./cmd/dagger --run='TestSomething'
```

## Open Questions To Resolve Before The Real Move

1. Which `workspace` porcelain features are still intended to land on top of
   plumbing, versus intentionally abandoned by the spin-out?
2. Should lockfile/update work be replayed at all before the plumbing-based
   workspace integration settles?
3. Is there any early `workspace` foundation logic worth porting manually from
   `c8fc9d728` / `bd6d55956`, or should those commits be treated as fully obsolete?
4. After replay, should the branch be linearized first and only then merged with
   fresh `main`, or should a fresh `main` merge happen earlier to reduce drift?

## Initial Commit Buckets To Build Next

When it is time to prepare the actual move, the next useful artifact is a
commit-bucket checklist with three columns:

- replay
- preserve plumbing instead
- drop

Seed entries for that checklist:

### Preserve Plumbing Instead

- `cd4c63ff1`
- `fec23805b`
- `6c93ddba9`
- `1bd6065c7`
- `13f2abf15`
- `1adb147a4`
- `eefd243f0`
- `ce32ecb46`
- `31a0f6dad`
- plumbing’s versions of path-contract and Query-root entrypoint commits

### Drop Historical Replay

- all historical merge-from-`main` commits on `workspace`
- raw replay of `c8fc9d728`
- raw replay of `bd6d55956`

### Replay Later

- `workspace` porcelain features
- lockfile/update features
- non-duplicate docs/design updates
- generated outputs
