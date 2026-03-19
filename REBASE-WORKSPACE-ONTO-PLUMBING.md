# Rebase `workspace` Onto `workspace-plumbing`

## Status

Draft operational plan.

Snapshot refreshed on 2026-03-18 against the latest fetched plumbing tip:

- `workspace`: `b45355fe0` `engine: keep rename backport behavior-neutral`
- `origin/workspace-plumbing`: `570a25dc3` `feat(core): hide entrypoint constructor from outer schema`

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
  - `origin/workspace-plumbing`: 96 commits ahead of merge-base
  - `workspace`: 314 commits ahead of merge-base
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
git rebase --rebase-merges --onto origin/workspace-plumbing <merge-base>
```

Result:

- Rebasing progressed through 313 of 317 steps.
- The first stop was the top merge commit:
  - `eeea90802` `Merge branch 'main' into workspace`
- Conflict count at that stop: 43 files.
- Conflict cluster was concentrated in:
  - `cmd/dagger/*`
  - `core/object.go`
  - `core/served_mods.go`
  - `core/schema/coremod.go`
  - `core/schema/workspace.go`
  - `core/workspace/*`
  - `engine/client/client.go`
  - `engine/server/client_resources.go`
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
git rebase --onto origin/workspace-plumbing <merge-base>
```

Result:

- Real semantic conflict arrived immediately at:
  - `c8fc9d728` `workspace: add config parser and detection logic`
  - conflict file: `core/workspace/detect.go`
- After skipping that, the next real conflict arrived immediately at:
  - `bd6d55956` `engine: load workspace modules at connect time`
  - conflict file: `engine/server/session.go`
- Skipping those exposes the next stale-foundation cluster immediately:
  - `4e6a886d1` `cli: replace initializeDefaultModule with workspace-aware loading`
  - `c31ac7c35` `cli: remove dead initializeDefaultModule function`
  - `1d09d0b5a` `fix: use dagql.ObjectResult instead of non-existent dagql.Instance`
  - conflict files stay concentrated in old CLI default-module code and `session.go`

Interpretation:

- A literal chronological replay of early `workspace` foundation commits is wrong.
- `workspace-plumbing` intentionally rolled back initialized-workspace/config
  parsing and old connect-time loading shape.
- The broader rewrite bucket also includes the older CLI-side
  `initializeDefaultModule` / focused-Query assumptions that plumbing has since
  replaced with the current entrypoint-proxy architecture.
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
  - `origin/workspace-plumbing`: `3db01c79c`
- `workspace: move entrypoints to Query root`
  - `workspace`: `bbdfa797e`
  - `origin/workspace-plumbing`: `ae45fb66c`
- `engine: rename extra-module blueprint to entrypoint`
  - `workspace`: `bfced037f`
  - `workspace`: `b45355fe0`
  - `origin/workspace-plumbing`: `99c0047b9`

Near-duplicate intent also exists around:

- path-contract rollout follow-ups
- foundation/compat plumbing
- engine-backed authoring compatibility
- entrypoint proxy / outer-schema follow-ups

## Plumbing Fix Threads That Must Survive

These are the current `origin/workspace-plumbing` equivalents of the ledger's
must-keep threads, and should be treated as non-negotiable until proven
superseded by equivalent `workspace` behavior.

### Required To Preserve Or Re-prove

- `2af637736` `engine: preserve legacy toolchain customizations`
  - proof:
    - `TestToolchain/TestToolchainsWithConfiguration/override_constructor_defaultPath_argument`
- `392947385` `core: restore legacy blueprint caller env defaults`
  - proof:
    - `TestUserDefaults/TestLocalBlueprint/inner_envfile`
- `30ed7c5f6` `core/schema: restore legacy generator include matching`
  - proof:
    - `TestGenerators/TestGeneratorsDirectSDK/java/generate_multiple`
    - `TestGenerators/TestGeneratorsAsBlueprint/java/generate`
- `843bca331` `engine: seed runtime module content cache`
  - broader than generators; protects runtime contextual dir/file reloads

### Also Treat As Must-Keep Plumbing Behavior

- `db34d9710` `engine: fix sandboxing under root slash`
- `c21f25ef5` `cli: make bare init idempotent`
- `a4f551bd0` `cli: route query-root ctor flags to modules`
- `e7f30322e` `workspace: repair explicit -m entrypoints`
- `9f1336827` `test(workspace): fix tests`
- `99c0047b9` `engine: rename extra-module blueprint to entrypoint`
- `54c097019` `cli: drop local explicit -m query projection shims`
- `1635ccd5a` `cli: stop hoisting query-root constructor flags`
- `225467934` `restore(cli): use local flags for constructor args on root command`
- `dc1b8dffc` `feat(dagql): two-server architecture for entrypoint proxies`
- `abfd53e57` `fix(dagql): proxy resolvers through inner server`
- `ac4a6d2fc` `fix(core): respect directives core currentTypeDefs`
- `627273e27` `fix(core): use inner server for runtime plumbing in ContainerRuntime.Call`
- `32e99dd61` `feat(core): use 'with' field for entrypoint constructor args`
- `570a25dc3` `feat(core): hide entrypoint constructor from outer schema`
- `6f4da0159` `test(workspace): update entrypoint proxy tests for with/shadow`
- `2e6939dda` `test(workspace): cover entrypoint proxy corner cases`
- `e341496df` `test(workspace): add entrypoint test using Go SDK`

These are late plumbing fixes validated against the current entrypoint-proxy
contract and explicit `-m` behavior. They define the present plumbing-side
truth:

- the outer schema exposes entrypoint proxies and `with`
- the entrypoint constructor is hidden from the outer schema
- proxies may shadow core fields on the outer server
- ID loading and engine-internal runtime plumbing must stay on the inner server

They are exactly the kind of “easy to lose in a replay” fixes that should be
preserved first and then revalidated.

## Proposed Replay Strategy

### Phase 0: Prepare The Integration Branch

Create a fresh branch from an up-to-date `workspace-plumbing`.

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
- `with`-field constructor flow on Query root
- entrypoint constructor hidden from the outer schema
- outer/inner server split for proxy resolution and ID/runtime loading

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
- `bfced037f` `engine: rename extra-module blueprint to entrypoint`
- `b45355fe0` `engine: keep rename backport behavior-neutral`

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
- current entrypoint proxy / `with` / outer-schema architecture

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
- `engine/server/client_resources.go`
- `engine/client/client.go`
- `core/schema/workspace.go`
- `core/schema/coremod.go`
- `core/workspace/detect.go`
- `core/workspace/legacy.go`
- `core/module.go`
- `core/modulesource.go`
- `core/object.go`
- `core/served_mods.go`
- `core/sdk.go`
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
- `TestWorkspace/TestEntrypointProxyShadowsCoreFields`
- `TestWorkspace/TestEntrypointProxyConstructorArgOverlap`
- `TestWorkspace/TestEntrypointProxyCoreAPIShadow`
- `TestWorkspace/TestEntrypointProxySelfNamedMethod`
- `TestWorkspace/TestEntrypointProxyCoreAPIShadowWithCoreReturnTypes`
- `TestWorkspace/TestEntrypointProxyDirectoryField`
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
4. Should the plumbing ledger be updated to replace the older
   “skip conflicting entrypoint proxies” wording with the current
   outer-schema / `with`-field design before the real move starts?
5. After replay, should the branch be linearized first and only then merged with
   fresh `main`, or should a fresh `main` merge happen earlier to reduce drift?

## Initial Commit Buckets To Build Next

When it is time to prepare the actual move, the next useful artifact is a
commit-bucket checklist with three columns:

- replay
- preserve plumbing instead
- drop

Seed entries for that checklist:

### Preserve Plumbing Instead

- `2af637736`
- `392947385`
- `30ed7c5f6`
- `843bca331`
- `db34d9710`
- `c21f25ef5`
- `a4f551bd0`
- `e7f30322e`
- `9f1336827`
- `99c0047b9`
- `54c097019`
- `1635ccd5a`
- `225467934`
- `dc1b8dffc`
- `abfd53e57`
- `ac4a6d2fc`
- `627273e27`
- `32e99dd61`
- `570a25dc3`
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
