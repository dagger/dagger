# Git-native workspace commits: design and continuation

**Status:** implemented Direction A prototype in [PR #14314](https://github.com/dagger/dagger/pull/14314), still draft. No new public GraphQL API. The remote-native implementation and matched benchmark checkpoint is `756122ed24d8bc351c568ba87a1d3d88d7c71262`; the earlier incremental-local checkpoint was `460380658e5b95975ab084db71f706676a432bad`. Use the PR's current head for continuation, not a historical hash.

This is the starting document for a fresh session asked to **"continue https://github.com/dagger/dagger/pull/14314"**. It supersedes the implementation status and next steps in the [earlier investigation handoff](https://gist.github.com/vito/aead3f59d5aec76d824797c4c00f7f3a). The code is authoritative when it differs from this document.

## Resume: approved host-history reuse (2026-09-25)

**Implemented and locally validated through `daaae3060dead329ba5a1cdb5c500bcd105e4863`.** The earlier lazy-history checkpoint and measurements remain below. The user-requested push published through `64e2136`; the host-history commits described here have not been pushed.

- `30296e6` implements optional host donation only for **complete-history demand after a clean, owning-client, remote-backed capture**. The client's `Query` retains owner/path/captured-state plus exact remote repository recipe and anchor. This registry is session-local, not persisted or engine-global; remote provenance remains the reconstruction recipe.
- A separate internal `PackCommit` RPC sends only the requested non-thin object closure. Scratch Git metadata excludes host refs, config, replacement refs and promisor fetching. The engine imports into an isolated object database, validates completeness and exact inventory, then publishes an owned immutable snapshot without live alternates.
- Capture and ordinary depth-one commits never request donor packs. Missing, moved, shallow, incomplete or expired donors fall back to the existing authorized remote; malformed packs, corrupt objects and cancellation remain errors. Dirty/unpushed bundle captures and pure remote inputs keep their existing behavior. There are no public SDL changes.
- `e1a1ff3` and `cb73359` add independent host integration tests and the explicit service-bound origin fixture. `bbeae50` covers missing versus corrupt ancestral commits and skipped bundle registration. `daaae30` resolves final lint findings without suppressions.

**Validation:** full serial `go test -race ./engine/session/git ./engine/engineutil ./core ./core/schema -count=1 -p=1` passed, root golangci-lint v2.11.4 reports zero issues, and the combined from-source host/lazy/native/reconciliation/history/reftable matrix passed (27 tool-summary entries; call span `d2c45afc4def3741`). The offline-host baseline failed at deep history after a successful ordinary commit, attempting the removed origin (`f6a17bc11906e3cf`); the implementation passes. Tests cover exact donor owner/recipe/anchor scope, unrelated-object exclusion, offline deep history, independent retained-checkout fsck after donor removal, and remote fallback for missing/replaced/moved/shallow donors. Unit tests additionally cover incomplete blobs/ancestral commits, corrupt objects, old-client transport, cancellation and malformed imports. This is not a real engine restart/GC/eviction test or proof of green remote CI.

**Final ordinary-workflow benchmark:** the unchanged controlled-origin workload passed twice serially on `daaae306`, with fresh endpoints `tcp://i8t5hmrshc0gs:1234` then `tcp://87s3j61572f02:1234`; no other builds/tests overlapped. First-commit/full-loop medians were **1.634 / 9.273s**, later medians **1.636 / 7.705s**. Compare to the lazy-only measurements below: **1.444 / 9.790s** first and **1.530 / 6.847s** later. These samples do **not** establish unchanged latency: preserve the slower capture and repeated later-cycle outliers rather than labeling them unrelated noise.

All samples, **commit / full loop seconds**:

| Step | Host-reuse run 1 | Host-reuse run 2 |
| --- | ---: | ---: |
| README edit, remote base | 1.563 / 8.937 | 1.704 / 9.608 |
| Nested existing-file edit | 1.542 / 6.782 | 1.832 / 7.377 |
| Nested addition | 1.636 / 7.107 | 1.627 / 7.494 |
| Nested rename | 1.411 / 8.195 | 1.542 / 9.316 |
| README edit again | 5.306 / 10.721 | 4.353 / 10.353 |
| Nested deletion | 1.637 / 6.979 | 1.668 / 7.916 |

Capture was **7.590 / 3.783s**, capture plus first loop **16.527 / 13.391s**. Cycle 4's next-edit phase took **2.226 / 3.294s**. Cycle 5's native merge took **2.976 / 1.733s**; run 1 also had an **839ms** native transaction, while run 2's preceding incremental checkout took **1.454s**. The underlying slowdown remains unestablished. Both runs retain six native history writes/reconciliations, eleven incremental checkouts, one shallow promotion, no legacy commit/general merge, and only the same three actual depth-one fetch processes. Content hashes match the lazy-only runs. This workload does not demand old history, so it is not a benchmark of host-versus-remote complete-history transfer.

Raw own-log spans in trace `e9d38bac0c0c96f68949905266104683`: run 1 **`e3fb0a87eb966387`**, run 2 **`f1c480823a014a7f`**, each lines 1–86. Use the logged endpoint/timestamps to identify runs; outer test span associations can be confusing because of the private re-exec's duplicate running entry. Engine-test calls were `76678f73aecc0eb1` and `cac3926e69347c28`.

**Remaining:** investigate the final timing outliers with detailed telemetry and a fresh, resource-quiescent session before claiming latency parity; measure full-history donor transfer on representative repositories; real engine restart/eviction/GC and physical storage accounting; review/rerun remote CI. Do not broaden to dirty/bundle captures or Direction C without separate work. No worker-only implementation remains; all host changes are harvested. Refresh the stale PR description only when requested.

## Earlier checkpoint: lazy hydration validated (2026-09-25)

Continuation from [the pause handoff](https://github.com/dagger/dagger/pull/14314#issuecomment-5825385014) checked out actual head `49d15eb`. Production behavior is unchanged from `b895022`; `4123245c3763c6fae0b2ce30228528b33e47814b` fixes four final lint findings by extracting dependency attachment/diff parsing helpers and simplifying two conditions. No lint suppressions or public SDL changes.

**Completed on `4123245`:**

- Full `go test -race ./core ./core/schema -count=1` passed.
- Root golangci-lint **v2.11.4** (`run --timeout=10m ./...`) reports **0 issues**.
- Combined from-source lazy-history and native/reconciliation/history/reftable regressions passed (tool summary: 21 passing entries, including suite entries). Includes the follow-up independent full-bundle clone/fsck, retained depth-one checkout, hydration reuse after origin deletion, remote-parent no-fetch, 56-commit sequential/concurrent history, retained checkout lifetime, incremental checkout oracles, scoped history and identity replay. Combined test call span: `4a739c1f36636bb8`.
- The separately selected `TestWorkspaceCommittedHistoryDoesNotFetch` still **skips** under its inherited-session guard; it is not counted as passing coverage.
- GitHub checks remain red at merge SHA `363dd6c82b9b0f623ab84581daf445ffeaf0ff53`. Loading the lint trace yielded only a small outer trace, without the reported origin span or lint diagnostic output; local success does not establish why those remote checks failed.

**Lazy-hydration controlled-origin measurements:** unchanged `TestWorkspaceRealRepositoryPerformanceControlledOrigin` at `4123245`, same pinned `06eca99` fixture and six-edit workload, two serial invocations using the two distinct equivalent selectors below. Explicit from-source CLI and engine both identify `4123245c`; distinct endpoints were `tcp://osif87dmigtis:1234` and `tcp://4qa5bhjgh07ii:1234`. No builds/tests overlapped the timed workloads. Every source/workspace content hash matched between these runs. The harness and fixture were not modified.

Medians in seconds (main and pre-lazy are the recorded earlier controlled-origin runs, not new reruns):

| Population | True-main commit | Pre-lazy commit | Lazy commit | True-main full loop | Pre-lazy full loop | Lazy full loop |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| First remote-backed commit (2) | 25.956 | 9.771 | **1.444** | 41.526 | 17.141 | **9.790** |
| Subsequent varied commits (10) | 19.018 | 1.590 | **1.530** | 35.820 | 7.000 | **6.847** |

All samples retained, **commit / full loop seconds**:

| Step | Lazy run 1 | Lazy run 2 |
| --- | ---: | ---: |
| README edit, remote base | 1.508 / 9.607 | 1.379 / 9.973 |
| Nested existing-file edit | 1.545 / 6.728 | 1.510 / 6.978 |
| Nested addition | 1.827 / 7.091 | 1.502 / 6.800 |
| Nested rename | 1.516 / 6.886 | 1.629 / 6.965 |
| README edit again | 1.544 / 6.800 | 1.482 / 6.807 |
| Nested deletion | 1.465 / 6.689 | 1.904 / 7.169 |

Capture was **3.402 / 3.523s**, capture plus first loop **13.009 / 13.496s**. Consumer image setup is separate; actual full-file consumption is included in each loop and took **5.896 / 6.451s** on the first cycle and approximately **3.16–3.47s** thereafter. History checks were **34–47ms**. Cold shallow promotion was **222 / 215ms**, including **214 / 209ms** isolated packing, instead of complete-history hydration.

Each run recorded six native history writes, six native reconciliations, eleven incremental checkouts, one shallow promotion/pack, no legacy commit/general merge, and no full-history/unshallow fetch. Three actual Git fetch process spans remained: initial remote depth-one capture plus two shallow local source materializations. The broad `fetches=4` counter additionally includes one enclosing `fetching` wrapper. This workload deliberately uses recent/direct-parent history; genuinely older history still pays demand-driven hydration. These are small observational samples with shared host/build caches, not universal speedup or physical-storage claims.

Full-precision logs are in trace `e9d38bac0c0c96f68949905266104683`, own-log spans **`e308f4a4b8e036be`** and **`1cf8b945a4a2a347`**, lines 1–86. Engine-test calls: `2551b49726c6c61d` and `61885e682c2ff472`. The private re-exec leaves a duplicate running test entry in outer telemetry; each actual child completed successfully and logged six validated commits with unchanged host checkout.

**Next:** approved host-history reuse is now being implemented separately; no host-history implementation has been integrated at this checkpoint. `PackCheckout` currently includes all branches/tags and must not be reused unchanged for exact-commit-only imports. Preserve capture identity, owning-client authorization, self-contained ownership and remote reconstruction/fallback; do not eagerly move full-history cost into ordinary capture. Direction C remains deferred. Full CI, real engine restart/GC/eviction and physical allocation remain unvalidated. Nothing from this continuation has been pushed yet; refresh the PR description only when requested.

## Earlier pause: lazy hydration checkpoint (2026-09-25)

**Paused at the user's request due to host memory pressure.** Production implementation is committed at `b8950222b150056e77260837c129be979269ef65` (worker original `ac989cc`). Finish validating lazy hydration **before** starting host-checkout history synchronization. Direction C remains deferred. The sections below describe the earlier complete-history implementation and measurements unless explicitly stated otherwise.

- Ordinary remote commits now promote only the pinned commit/tree/blob closure at depth one, including when the shared mirror already has full history. Owned `HistorySource` retains the exact authorized remote anchor across descendants and persistence.
- Raw storage operations stay separate from history demand. Native staging/reconciliation, source-only checkout, short logs, and proven direct-parent ranges can use shallow storage. Deeper history, historical refs, full retained checkouts and bundle/export consumers hydrate through `__hydrateRepository` into owned immutable snapshots.
- Hydration merges complete authorized source objects with local storage, preserves local metadata, and removes the shallow boundary only after complete packing succeeds. Exact source recipe and structural Directory recipe partition the cache; runtime content-equivalent IDs alone are insufficient. No host sync or global object store was added.
- Main implementation: `core/git_local_history.go`, `core/git_commit_remote.go`, `core/git_history_native.go`, native commit/reconciliation/incremental checkout call sites, schema resolvers, persistence codecs and visitors. Independent regressions: `core/integration/workspace_remote_history_test.go`.

**Validation actually completed:** targeted core/schema Git/native/bundle/history/persistence tests; shallow object-inventory and donor-removal fsck tests; two codec cache restarts; exact-recipe/content-alias isolation; from-source remote-first-commit oracle; initial lazy-history matrix plus existing parent-history no-fetch test (worker reports eight passing cases). Baseline Ordinary failed specifically on eager `git fetch --unshallow`, establishing the regression. Test-only lint passed before the final additions.

**Not yet validated:** follow-up tests in `d754196` (full bundle independent clone/fsck, retained depth-one boundary, hydrated history reuse after origin deletion with exactly one hydration execution) compile but have not run against production. Full `go test -race ./core ./core/schema -count=1`, broad existing native integration rerun, and final root lint were interrupted/not started when the user paused. Do not report these as passed for this checkpoint. No lazy-hydration benchmark has run; the 9.771s/1.590s figures below predate it. Real engine restart/GC/eviction remains untested; codec restart tests use a test snapshot manager.

**Resume sequence:** run the expanded integration matrix `^TestWorkspace$/^TestWorkspaceRemoteLazyHistory(Ordinary|Demand|Unavailable)$`, then race tests, existing native/reconciliation/history/reftable regressions and CI-matching golangci-lint v2.11.4. Fix failures before claiming completion. Then run the unchanged controlled-origin real-repository benchmark twice, serially, using distinct equivalent selectors and checking distinct engine endpoints. Compare first commit and full loop with the recorded true-main and pre-lazy baselines; retain outliers. Refresh the stale PR description only when requested. No public SDL change: the new fields are internal.

All implementation/test commits have been harvested; no worker-only fix remains. At pause, `remote-native` was deliberately PAUSED and other workers IDLE. Resume commands sent in an interrupted parallel call were not confirmed delivered. Many engine-dev services remained listed in this long session; close the session before further resource-intensive validation.

## Earlier compaction notes (superseded above)

**Latest user decision:** pursue options **1 and 2 below**, within Direction A. Engines are single-tenant, but the user explicitly deferred Direction C; do not relax authorization scope or introduce engine-wide object storage on the basis of tenancy. No implementation of these next options has started.

1. **Avoid requiring complete history for an ordinary commit.** Explore owned shallow commits with demand-driven history hydration. Parent commit/tree objects suffice to construct a child, but unbounded/path-filtered history, older comparisons, retained `.git` exports, and persistence must retain correct semantics. Do not simply remove the complete-history gate and let queries silently return truncated results. Local repository mounting currently assumes already-owned storage; any lazy hydration needs explicit remote provenance, ownership and error handling.
2. **Use history already present in the approved host checkout.** For interactive workspaces, avoid downloading history the owning client already has. Import the requested commit's authorized object closure using captured checkout identity and approved host access; retain self-contained ownership after the client disappears and preserve remote reconstructibility. Host import must not copy unrelated refs/objects or depend on an expired client mount. Missing local history and pure remote API inputs need a correct fallback. Relevant entry points include `engine/session/git/git_capture.go`, `core/schema/workspace_checkpoint.go`, `host.__gitDir`, and `core/git_commit_remote.go`.

**Checkpoint:** `5220bea90e3095d64e7ab0de120585a7927e44f3` was pushed to `vito/dagger:fix-agent-git-history-no-fetch`; it includes all benchmark harnesses, remote-native code and results below. The chief was in a clean detached checkout before these notes. Do not discard local notes/commits by checking out again after compaction. Verify current status/log and GitHub head first. The PR description predates the real-repository/remote-native results and is stale; the code and this guide are authoritative. No publishing is needed merely to compact.

**Current result:** controlled-origin medians are first commit **9.771s**, first full loop **17.141s**, later commit **1.590s**, later full loop **7.000s**. Cold complete-history hydration costs **6.3–7.0s**, isolated packing **1.7–1.8s**. Capture stayed **2.9–3.7s**. All six commits now use native construction/reconciliation, and first-parent history comparisons do not refetch. Do not promise cold-first parity with warmed local commits. Physical engine allocation and full engine restart/GC stress remain unmeasured.

**Validation:** full `go test -race ./core ./core/schema -count=1`, seven from-source integration regressions and root golangci-lint v2.11.4 passed. Internal `__nativeCommitBase` is excluded from public SDL. Key implementation commits: `dee33de` (remote promotion/owned canonical tree), `756122e` (exact-parent history reuse); `3345369` is benchmark-only lint extraction.

**Benchmark rules:** use the controlled-origin real-repository harness and the matched main/pre-fix/final data below, not the older partially optimized synthetic baseline. Pin both source and advertised refs: moving live GitHub refs silently caused bundle import and a local base in one rejected run. Preserve all timings, including capture/first loop and outliers. Private SDK sessions must remove inherited routing variables and use explicitly built CLI/engine. Separate equivalent test selectors bypass engine-test session memoization; check distinct runner IDs. Run timed workloads serially, without competing builds/tests. The benchmark is opt-in; broad integration selectors skip it.

**Workers:** `remote-native` implemented and committed the remote path and is harvested; `realbench` holds pre-fix PR benchmark state; `mainbench` holds true-main plus test-only harness (never pull its baseline commit into the PR); `ci` handled lint. All completed their assigned turns, with no known pending implementation. They can be resumed if useful. The earlier TUI/engine smoke services were stopped; smoke-only commits were neither pushed nor exported. Full raw log span IDs, fixture hashes and test selectors are below.

## Start here

1. Read this document and the current PR description/checks.
2. Query the PR's actual head repository, branch and SHA. At this checkpoint the repository is **`vito/dagger`**, branch **`fix-agent-git-history-no-fetch`**, targeting `dagger/dagger`.
3. Work in that checkout, preserving unrelated host edits and local continuation notes. With the workspace checkout tool, use `ref` for the branch name, then verify HEAD against GitHub. Do not replace an already-correct workspace unnecessarily. A previous session encountered stale branch resolution, so do not skip verification.
4. Reproduce with an explicitly selected **from-source engine and CLI**. Inherited `DAGGER_SESSION_PORT` can send SDK tests to another engine; see [Validation and reproduction](#validation-and-reproduction).
5. Follow [Continuation priorities](#continuation-priorities). Do not restart the rejected experiments or replace Directory with a new backend by default.

Useful GitHub commands:

```sh
gh pr view 14314 --repo dagger/dagger \
  --json headRefOid,headRefName,headRepository
gh pr checks 14314 --repo dagger/dagger
```

## Direction and scope

The user's preference is explicit:

| Direction | Decision |
| --- | --- |
| **A: Git-native commit transactions** | Active approach. Substantial incremental gains are worthwhile without requiring every workflow to meet a one-second threshold. |
| **B: Git trees as a general Directory backend** | Deferred. Do not introduce a new general Directory representation merely to continue this work. |
| **C: Engine-owned Git object storage** | Explicitly deferred by the user, including after discussing single-tenant engines. Focus next on lazy history acquisition and approved host-history reuse, not engine-wide object storage or relaxed authorization boundaries. |

The implemented slices are plausibly independently mergeable after review and sufficient validation. That is not a claim of production readiness, exhaustive compatibility, or crash durability.

## Problem

Committing a small edit used to repeatedly leave Git's immutable representation and reconstruct it from a filesystem:

```text
Git commit -> Directory -> temporary Git merges -> retained full checkout
           -> Git commit -> fresh source checkout -> pending filesystem diff
```

The earlier live-agent run measured a 17.182s commit tool call, including two general changeset merges and a retained checkout. Git history comparison had already become cheap after redundant local fetches were removed. The actual Git commit subprocess in another detailed run took about 0.4s.

The desired work for a small same-base commit is to stage changed blobs, update trees, write a commit, preserve remaining edits, and reuse unchanged filesystem content. Fetching or rehashing an entire cached repository is not intrinsic to that operation.

## Implemented design

The PR also retains the earlier foundation: read-only cached local history joins, bounded remote-log fetching with correctness fallbacks, fetch-free local source-only checkouts, and the nested interactive CLI attachable fix needed for snapshot approval. Those changes are not replaced by the three slices below; the original handoff contains their detailed investigation.

### 1. Native commit construction

`GitRef.withCommit` first attempts `GitCommitChangesetNative`:

1. Prove a direct discard-Git tree baseline with the same repository recipe identity and commit SHA as the parent. Recipe identity retains authorization scope; equal-looking filesystem contents are insufficient.
2. For a remote parent, hydrate its pinned history through the existing authenticated/locked mirror path and pack only its reachable closure into private immutable storage. The shared mutable mirror may contain unrelated authorization scopes, so copying all its packs or refs is forbidden. An engine-computed exact-recipe cache input prevents content aliases from sharing promotion across authorization recipes; reconciliation and construction share one promotion.
3. Hold the resulting local repository's read-only mount.
4. Initialize private Git metadata, an operation-local index, and a sparse staging worktree.
5. Use `read-tree` for the parent, hydrate ancestor attribute/ignore controls, and stage only the selected delta.
6. Use `write-tree` and `commit-tree` with explicit identity, message, dates and parentage.
7. Write new objects into scratch storage, then copy only new loose objects into a copy-on-write child of the existing repository snapshot.
8. Return its self-contained Git storage. Persist no scratch index or mount-path alternate.

Git may freshen the timestamp of an existing pack in either a primary or alternate object database. Pointing it at a writable inherited pack risks a history-sized copy-up. The borrowed parent mount must actually be **read-only**, not merely chmod-protected.

A schema resolver can represent a detached ref as `Name == SHA`, not only an empty name. The native transaction recognizes that exact form without mutating the shared ref. An early benchmark silently fell back until this was fixed; telemetry now identifies support and fixed-code fallback reasons.

### 2. Native workspace reconciliation

`Workspace.withCommit` still freezes the receiver and resolves author identity before constructing replayable results. It uses the private `Changeset.__mergeForWorkspaceCommit` field for reconciliation; generic Directory/Changeset merge APIs are unchanged.

For verified same-base inputs with owned or promotable remote storage, the already-approved changeset is reused instead of applying it to HEAD and computing the same delta again. Empty or off-baseline inputs retain the complete-tree reconstruction path.

`TryNativeWorkspaceMerge` uses private sparse indexes and cached objects to build temporary working/incoming trees, then runs `merge-tree` with an explicit base. It does **not** return an apparently identical raw Directory. Instead it replays the filesystem transitions the legacy checkout sequence would perform:

```text
COW Before
  + raw working delta
  + checkout differences: working tree -> parent
  + raw incoming delta
  + checkout differences: incoming tree -> working tree
  + checkout differences: working tree -> merged tree
```

This distinction matters: Git can skip rewriting identical entries, retaining incoming raw bytes, ownership, permissions or xattrs. Other transitions recreate files and normalize that metadata. Blanket normalization and blanket raw-tree reuse are both wrong for some existing cases.

Only declared changed files and the sparse delta's directory metadata are checked. The helper does not stage, hash, or walk the whole baseline filesystem. Temporary merge commits, objects and indexes are discarded rather than retained in output layers.

### 3. Incremental canonical source materialization

The private `GitRef.__withCommitRepository` recipe records the exact parent and the resulting resolved HEAD. `LocalGitRepository.CheckoutBase` contains:

```go
type GitCheckoutBase struct {
    Parent    dagql.ObjectResult[*GitRef]
    CommitSHA string
    Tree      dagql.ObjectResult[*Directory] // exact evaluated tree for a remote parent
}
```

These are owned DAG dependencies, with persistence codecs and reference visitors. They are not expired mount paths, guesses based on a dirty worktree, or public `withContents` metadata supplied by a caller. A retained remote parent tree is validated against its exact canonical source-only producer recipe on attachment, persistence and use.

For a source-only checkout of exactly the annotated SHA:

1. Validate the current local storage, actual single-parent commit headers, and checkout controls before evaluating the parent tree.
2. Reuse the pinned remote parent's evaluated canonical tree, or select a local parent's `tree(discardGitDir: true)` through DagQL.
3. Create a COW child of that canonical snapshot.
4. Borrow current repository objects read-only; use private Git metadata/index to check out only the Git tree delta.
5. Normalize rewritten paths, touched ancestors and the root to Unix second 1. Leave untouched files/inodes alone.

Other refs must not inherit the annotated tip's materialization. Retained `.git` checkouts remain on the existing self-contained path. Changed attributes or submodules trigger a full source checkout because unchanged blobs can acquire different checkout bytes.

A cold parent may still need materialization. This design removes repeated full materialization; it does not promise that no filesystem is ever constructed.

### 4. Reuse of the exact owned remote parent for history

Mixed remote/local history comparisons may borrow the committed child's complete owned objects when its current tip, actual single-parent headers, retained parent SHA and remote repository recipe all match. Substitution is operation-local; public refs and shared mirrors are not changed. This removes first-commit ahead/behind refetches without a global URL/SHA alias. Other comparisons retain the general join, and real object/cancellation/cleanup errors remain errors.

## Semantics, fallbacks and safety

| Area | Current boundary |
| --- | --- |
| Commit transaction | Direct same-base SHA-1 provenance; complete supported owned storage, including exact-scope promoted remote history. Unsupported refs/storage/submodule edits use the existing path. |
| Workspace reconciliation | Same-base Git inputs with owned or promotable storage. Changed controls, ignored paths, empty directories, unsupported delta metadata and storage layouts can fall back. |
| Incremental checkout | Source-only, exact annotated SHA, supported complete local SHA-1 storage, actual single parent; remote parents require the pinned canonical tree. Changed `.gitattributes`/`.gitmodules` and any gitlinks fall back. |
| Public APIs | Existing methods and signatures remain. New schema fields are internal-only. |
| Rich filesystem metadata | Match existing checkout behavior, including checkout-skip retention. Do not equate Git tree equality with filesystem equality. |
| Ref namespace | Native local-source storage deliberately retains unrelated source refs/tags rather than reproducing a fresh fetch's pruning; review this difference. Remote promotion retains only the requested history closure, never unrelated mirror objects or refs. |
| Errors | Fallback only when every error leaf is explicitly unsupported. Missing objects, cancellation, deadlines and cleanup errors remain errors. |

Rooted filesystem operations prevent writes or metadata inspection through replaced symlink ancestors. Produced snapshots are released if cancellation or source-unmount failure arrives after their creation. Cleanup uses an uncancelled context and preserves release failures.

### Storage lifetime is not crash durability

Existing snapshot ancestry shares and pins inherited object storage and canonical source trees. Explicit DagQL dependency attachment, encoding/decoding, and reference visitation retain checkout provenance across valid cache persistence.

This uses the existing [cache persistence model](cache_persistence.md): graceful persistence is best-effort, and an unclean or invalid cache can be discarded. **No new guarantee of surviving an engine crash or ungraceful shutdown is claimed.** A future Direction C must decide its own durability, reachability, authorization and GC contracts.

Do not confuse counters for new compressed loose-object files with physical disk allocation. Full allocation, retained parent layers, long-chain compaction and GC behavior still need broader measurement.

## Measurements

### Synthetic optimization checkpoints

The baseline below is **not unmodified `dagger/dagger@main`**: it already includes earlier history/checkout optimizations. Use the real-repository comparison below for the true-main baseline.

The deterministic harness uses a packed local repository with **12,002 files and 98,304,010 source bytes**, no remote, and three sequential scoped commits. It preserves an unselected edit and separately measures pre-status, commit, post-status, history, source consumption and the next edit. Engine/CLI build, fixture creation, connection and capture are outside commit timing.

These are observations from separate engine runs, not controlled universal speedup ratios or tail-percentile estimates:

| Production checkpoint | Commit median | Full-cycle median | Sample |
| --- | ---: | ---: | --- |
| `ba72c9d` plus harness `04c8bbd` | 11.07s | 11.77s | Three sequential commits |
| Native commits, `411b67d` | 4.59s | 5.38s | Three sequential commits |
| Native reconciliation, `ef87790` | 2.28s | 3.04s | Final three-commit run |
| Incremental source trees, `4603806` | **0.74s** | **1.93s** | Two runs, six commits |

For the last row, commit times ranged **0.56–1.66s** and complete cycles **1.47–2.45s**. The latest run's commits were **0.558s, 0.736s, 0.741s**, with cycles **2.261s, 1.591s, 1.590s**. The first cycle can include an additional baseline consumption cost; these are changing HEADs/recipes, not uniformly warmed identical queries.

In that latest run:

- Three actual native history commits, three native reconciliations, six temporary merge commits.
- Five incremental source materializations, each updating one Git path, typically **24–30ms**.
- **Zero general merge reconstructions, commit-time fetches, or full source checkouts inside commits.** The one fetch was initial capture.
- New loose-object file data was only hundreds of bytes per commit for this top-level, tiny-file edit fixture. This is not a representative byte count for every repository or a physical allocation measurement.

Do not discard outliers. A previous reconciliation-stage run had a **5.14s commit / 8.14s cycle** with slow Git index/history operations. The first incremental run included a **~916ms incremental checkout**. Their underlying latency causes remain unresolved.

The original 17.182s live-agent result was a different workload/environment. The synthetic numbers measure core API work, **not LLM latency or the complete committer module/TUI tool call**. The fresh smoke test below measures the latter separately.

### Controlled-origin comparison after remote-native commits

**Latest matched measurements:** remote promotion and parent-history reuse at `756122ed24d8bc351c568ba87a1d3d88d7c71262`, compared with pre-fix PR production `71bfa41` and true-main production `06eca99`. Test-only baseline builds were `f2eccf3d3793caca6317200f8f80998685f84213` and `86befe1120fb435ec2f407e0e6618ed376bf6443`, respectively.

Pinning the checkout alone proved insufficient: after live GitHub refs moved, capture discarded advertised tips not present in the pinned checkout, selected older known ancestors, and imported a bundle. That silently made the base local. The controlled variant instead serves the same real-repository history through smart HTTP plus a session-owned tunnel with a frozen advertisement. A per-origin host HTTP proxy reaches that same server without rewriting the captured origin URL. Host and engine both verify HEAD `06eca99`; telemetry must show snapshot reconstruction and **zero bundle imports**. Origin transport setup is timed separately. These measurements are **not directly matched to the historical live-GitHub transport runs below**.

All six runs (two per variant) used identical measured harness blob `591ed9351287f5a3fad1edd43dc84ab9b5aaa513`, SHA256 `5cd7ec38ec68b98f5e70f14c333658d712ca938e3c0b2dbfd8a52ff60b6ec726`. The same six-edit workload, real repository, pending edit and full-file consumers were retained; every source/workspace hash matched across variants. Runs were serial with fresh engine state, explicit from-source targets and private SDK sessions. Host/build caches and compressed pack representations were not identical; this remains a small observational comparison. A subsequent lint-only helper extraction does not change the measured loop.

Medians, seconds:

| Population | Main commit | Pre-fix PR commit | Remote-native commit | Main full loop | Pre-fix PR full loop | Remote-native full loop |
| --- | ---: | ---: | ---: | ---: | ---: | ---: |
| First remote-backed commit (2 per variant) | 25.956 | 23.990 | **9.771** | 41.526 | 39.297 | **17.141** |
| Subsequent varied commits (10 per variant) | 19.018 | 5.943 | **1.590** | 35.820 | 11.130 | **7.000** |

Both final first-commit observations: **9.933 / 9.608s**, full loops **17.701 / 16.581s**. Capture was **3.706 / 2.919s**, and capture plus first loop **21.407 / 19.500s**; the improvement was not deferred into capture. Pre-fix PR capture was 3.380 / 3.064s; main capture was 3.213 / 7.949s. The cold first loop is about 2.29x faster than the pre-fix PR and 2.42x faster than true main in this sample, **not yet steady-state local-path latency**.

Final post-fix samples, **commit / full loop seconds**:

| Step | Run 1 | Run 2 |
| --- | ---: | ---: |
| README edit, remote base | 9.933 / 17.701 | 9.608 / 16.581 |
| Nested existing-file edit | 1.486 / 6.709 | 1.867 / 6.886 |
| Nested addition | 3.138 / 8.411 | 1.421 / 9.245 |
| Nested rename | 1.450 / 6.481 | 1.490 / 7.177 |
| README edit again | 1.810 / 6.953 | 1.691 / 7.048 |
| Nested deletion | 1.831 / 7.074 | 1.404 / 6.422 |

Each final run executed **one remote promotion/pack, six native history writes, six native reconciliations, eleven incremental checkouts, zero legacy commits/general merges, and zero history-join fetches**. Four actual Git fetch process spans remain: initial remote capture and complete-history hydration, plus two shallow local source materializations. The broad `fetches=6` trace counter includes two enclosing `fetching` wrappers. Pre-fix PR had five native writes and three directory-metadata reconciliation fallbacks; those nested cases now pass natively without weakening the metadata gate.

**Remaining cost:** promotion took 8.754 / 8.117s, including 7.003 / 6.323s complete-history hydration and 1.745 / 1.788s isolated packing. Fetching the complete cold history dominates; no zero-copy or zero-cold-fetch claim is justified. Full-file consumers still cost roughly 3s per later loop. Retain run 2's 2.410s history outlier on cycle 3 (no fetch), and the early pre-history-reuse exploratory run's 18.726s capture / 21.680s first commit / 52.648s first loop (`07c687b752a59a4e`); it is not included in final medians. A separate live-origin run that silently imported a bundle was rejected as evidence for remote-native first-commit performance.

Reproduce using separate invocations to avoid memoizing the engine-test call:

```text
^TestWorkspaceRealRepositoryPerformanceControlledOrigin$
^(TestWorkspaceRealRepositoryPerformanceControlledOrigin)$
```

Raw logs in trace `0ad63cdfeee7172c2fa61c05bd5dbabd`, `ReadLogs(scope: "own", fromLine: 1)`:

- True main: `61556b72c65f8390`, `335e215be0529413`.
- Pre-fix PR: `f8f13d5d58e48bb9`, `cdde0d78e534930c`.
- Remote-native: `c9714d49532860ac`, `0b6c2a99b4d25c49` (fresh runners `b0tbluif32tl8`, `jtm068do636ri`).

Validation after integration: full `go test -race ./core ./core/schema -count=1`, seven from-source remote/local commit/history/reconciliation regressions, and CI-matching root lint pass. Tests cover authorization/content-alias separation, exact packed object inventory with unrelated shared-mirror objects and replacement refs, concurrent promotion, donor removal, canonical tree producer validation and ownership across two cache restarts. This is **not** a full engine restart/GC stress test or physical storage-allocation measurement. The existing inherited-session local-history test still skips under its guard; it was not counted as a passing remote regression.

### Historical live-origin comparison against true main

Production sources were pinned to **main `06eca9957aa99cdf55855110846e8ee2546ef306`** and **PR `71bfa41e8bb442a9ca57246575de54cf979a63b0`**. Both received the identical standalone test-only harness, with no production changes: main measured build `4c91b5e183b1be041c190ed7d8c0b9077db855b2`, PR measured build `99f0a93b9352e0422a82dc12ae2f5c63c1040ec1`. The measured harness Git blob is `af56c317205b7721e88e4fed2d5b006cec4a501d` (SHA256 `01bedeef1899852b900b600a328747f58e2ca49204f55809ef80c305d4842d72`). The subsequently retained harness adds an explicit opt-in gate so normal CI does not fetch full history.

Fixture: a full-ancestry checkout of that pinned main SHA, **13,935 commits, 183,548 objects, 16,201 tracked files and 176,838,006 regular source bytes**, retaining `origin=https://github.com/dagger/dagger.git` and branch tracking. Removing the remote would change snapshot provenance and hide the first-commit fallback. Git 2.55.0 and clone/config/single-thread repack procedures matched; pack representations were not byte-identical (Git-file sizes varied by about 14 KB out of 210 MB).

Two fresh engines per variant, run serially (PR then main), each executing six changing-HEAD scoped commits while preserving an unselected `go.mod` edit. Each cycle measures status, actual patch review, `withCommit`, post-status, ahead/behind and latest-two history, complete source/workspace file hashing in a container, and evaluation of the next edit. Engine/CLI builds, fixture setup, connection, capture and consumer image setup are outside cycle timing. All four runs passed path/content/ancestry and source-isolation assertions; source and workspace content hashes matched across variants at every step.

| Samples per variant | Main commit median | PR commit median | Main full-cycle median | PR full-cycle median |
| --- | ---: | ---: | ---: | ---: |
| First remote-backed commit (2) | 27.918s | 29.545s | 42.327s | 47.864s |
| Subsequent varied commits (10) | 21.418s | 6.093s | 38.182s | 11.182s |

Later median commits/full cycles were approximately **3.52x / 3.41x faster** in this sample. **The first commit did not improve and was slower in both observations.** This is a small observational comparison, not a universal speedup or a causal attribution of the first-commit difference. Engines had fresh state volumes, but shared host/build caches and run order were not randomized.

Every sample below is **commit / full cycle in seconds**; do not discard the slower runs:

| Step | Main run 1 | Main run 2 | PR run 1 | PR run 2 |
| --- | ---: | ---: | ---: | ---: |
| README edit, remote base | 28.192 / 42.960 | 27.643 / 41.694 | 29.550 / 50.151 | 29.540 / 45.577 |
| Nested existing-file edit | 28.403 / 39.464 | 20.451 / 32.052 | 6.564 / 11.938 | 6.983 / 11.939 |
| Nested addition | 21.518 / 40.383 | 19.212 / 34.865 | 6.026 / 11.152 | 7.975 / 13.075 |
| Nested rename | 23.538 / 35.138 | 20.422 / 35.886 | 6.160 / 11.211 | 6.188 / 11.220 |
| README edit again | 30.484 / 45.945 | 22.163 / 42.223 | 1.557 / 6.487 | 1.500 / 6.623 |
| Nested deletion | 21.318 / 36.901 | 21.110 / 41.463 | 1.404 / 6.447 | 1.595 / 6.933 |

Cold capture/status was 5.495/6.005s on main and 5.878/6.770s on the PR, separate from fixture setup (27–30s). Full-file consumption alone cost roughly 3s in later cycles. Subsequent history reads cost 6.4–15.9s on main versus 29–57ms on the PR; the loop gain is not just work deferred beyond `withCommit`.

**Trace findings, per run:** main executed six legacy commits, twelve general merges and 45 actual Git fetch process spans. The PR executed one legacy commit, five native history writes, five general merges and nine actual Git fetch process spans. Broad harness fetch counters also include two `fetching` wrapper spans; they are not extra Git processes.

Two concrete remaining eligibility limits surfaced:

- A clean remotely reproducible snapshot retains a `RemoteGitRef`; native commit/reconciliation eligibility requires local storage. The first commit therefore uses the legacy path. Its output is local, enabling subsequent native construction. See `checkpointCapturedGitCompositionWithBase` and `GitCommitChangesetNativeBase`.
- Nested edit/add/rename cycles 2–4 use native commit construction but fall back from native reconciliation with `directory-metadata`. Only cycles 5–6 use both native construction and reconciliation. Do not label all subsequent commits fully native or relax metadata preservation just to improve these timings.

**Storage limitation:** fixture file sizes, host `du` and pack hashes were recorded; engine physical allocation, retained layer growth and GC effects were not measured. No storage-amplification claim follows from these timings or new-object counters.

Reproduce with the opt-in `core/integration/workspace_realbench_test.go`, package `./core/integration`, verbose, in two separate engine-test invocations:

```text
^TestWorkspaceRealRepositoryPerformance$
^(TestWorkspaceRealRepositoryPerformance)$
```

Distinct equivalent selectors avoid memoizing the entire engine-dev test invocation; each invocation allocates fresh random engine-state and `/run` volumes. Private children remove inherited SDK session variables and log explicit CLI/runner/engine versions. Observed PR runners: `78g223kdsab2a`, `6n2i4lnv2itn6`; main runners: `19fa708ui5ek4`, `b1ju0gjloiacq`.

Raw full-precision JSON, per-step timings, patches, hashes and trace counters are available in session trace `0ad63cdfeee7172c2fa61c05bd5dbabd`, via `ReadLogs(scope: "own", fromLine: 1)`: PR spans `14adc50e0b29c707`, `488e350a8dd8d73c`; main spans `0fdf2213c3757294`, `9edd0f81cf5fb257`. The private child re-exec can leave a duplicate running test in outer telemetry; both parent and child output reported PASS.

### Fresh interactive-agent smoke test

An isolated copy of PR checkout `71bfa41` ran `dagger agent editor committer` with the from-source CLI against an explicitly supplied from-source engine. CLI identity reported `71bfa41e`; engine-lab built from that same workspace and served `v1.0.0-beta.15+d0bffb21`. LLM authentication worked without switching to the stock engine. The agent reviewed status/diff/log, changed one comment, committed only that file, read the latest two commits and confirmed no pending changes. A second scoped commit restored the comment. Neither commit was pushed or exported, and both test services were stopped.

| Operation | First, remote-backed | Second, local-backed |
| --- | ---: | ---: |
| `Workspace.withCommit` span | 19.236s | 4.491s |
| Committer module span | 20.211s | 5.813s |
| Complete agent commit-tool span | 26.893s | 7.647s |

The first operation visibly used general merges, retained checkout and fetch; the second had native merge/commit and incremental-checkout spans. These two observations are **not the matched main benchmark or complete LLM conversation latency**. Snapshot capture succeeded; the recorded author was the commit tool default `Dagger <dagger@localhost>`, so this was not validation of a configured human identity. Smoke-only commits: `80b9760b12d9140fafabdb11beb72c5a6b864eb9` and `127ab14030c00e6f87da79d050792256985fb54e`.

### Investigation evidence

Trace/span IDs are breadcrumbs, not a replacement for rerunning tests. Some private-session engine spans exist only in the harness's captured telemetry, so the outer session cannot necessarily inspect them directly.

- Session trace: `00cd6583ac232e50455972f138f9e931`.
- Baseline PERF logs: `12081996215567a3`.
- First-slice PERF logs: `29cc66727634ccac`.
- Final reconciliation PERF logs: `9d40b5d4b925398e`; earlier outlier: `ca553d97e3783c7c`.
- Incremental PERF logs: `e75c012eb9095890`, `470ad2d319e233d5`.
- Incremental trace gate: `1f9e974da084275e`.

## Validation and reproduction

### Critical test-targeting correction

When `engine-dev` supplies `DAGGER_SESSION_PORT`, an SDK connection can use that inherited session instead of the explicitly configured from-source runner. This produced extremely fast semantic-test passes against the wrong target, while private-session trace tests correctly exposed failures.

Relevant workflow/oracle tests now use `runWithPrivateTraceSession` in `core/integration/tracesink_test.go`. It re-execs only the selected test, removes inherited session port/token variables without changing the parallel parent's environment, and retains `_EXPERIMENTAL_DAGGER_RUNNER_HOST` and `_EXPERIMENTAL_DAGGER_CLI_BIN`. Inspect the connected engine version when in doubt.

**Correction to earlier handoff reports:** the `LLM.portableID`/`portable-id` scoped-history and identity-replay failures were not reliable evidence of a PR-baseline defect. Those tests pass against the correctly selected from-source engine. Do not carry forward the earlier "pre-existing schema mismatch" diagnosis.

### Passed checks

```sh
go test ./core ./core/schema -count=1

go test -race ./core \
  -run 'TestIncrementalGitCheckout|TestGitCheckoutBasePersistence|TestNativeWorkspace|TestGitNativeCommit' \
  -count=1
```

Use the from-source engine test tool with `pkg: ./core/integration`, verbose output, and these selectors. Run the performance selector alone:

```text
^TestWorkspace$/^TestWorkspaceScopedCommitPerformance$

^TestGit$/(TestGitRefWithCommitReftable|TestGitRefNativeCommitHistory)$

^TestGit$/(TestGitRefIncrementalCheckoutOracle|TestGitRefIncrementalCheckoutTrace|TestGitRefRetainedCheckoutSurvivesSourceScope)$

^TestWorkspace$/(TestWorkspaceWithCommitReconciliationOracle|TestWorkspaceWithCommitNativeReconciliationTrace)$

^(TestGit|TestWorkspace)$/(TestGitRefWithCommit|TestGitRefWithCommitNative|TestWorkspaceWithCommitScopedHistory|TestWorkspaceWithCommitResolvedIdentityReplay)$
```

Coverage includes exact commit objects, pending path sets, conflicts, metadata, attributes, source immutability, concurrent detached commits, retained repository lifetime, and incremental/full-checkout equivalence. Backend tests assert canonical root timestamps; container inspection wrappers can replace root mtime, so consumer tests compare root metadata but assert timestamp 1 only for descendants.

Persistence coverage verifies exact provenance dependency retention across **two cache restarts**. It is not a full engine restart/GC stress test. Long-history coverage now exercises 32 sequential commits followed by three concurrent eight-commit branches (**56 commits**), varying additions, renames, executable edits and removals. It checks complete ancestry, `git fsck`, retained ancestor/source isolation and incremental/full-checkout equivalence. Trace gates require actual native writes and delta checkouts, allow one cold canonical parent checkout, and reject repeated full materialization or native fetches.

### Continuation validation after `424cb01`

The storage review found that native publication accepted reftable repositories despite writing loose refs and replacing their configuration. They now explicitly fall back (`ref-storage`); bare/worktree eligibility tests and a private-session public API test cover this. The intentional retention of unrelated source refs/tags remains unchanged and still needs review.

Validation on the combined continuation tree:

- `go test -race ./core ./core/schema -count=1` passed.
- Seven explicitly from-source integration tests passed: `TestGitRefWithCommitReftable`, `TestGitRefNativeCommitHistory`, `TestGitRefWithCommitNative`, `TestGitRefIncrementalCheckoutOracle`, `TestGitRefIncrementalCheckoutTrace`, `TestWorkspaceWithCommitReconciliationOracle`, and `TestWorkspaceWithCommitNativeReconciliationTrace`.
- The history test observed 56 native writes, 56 incremental materializations, 56 delta checkouts and one cold full parent checkout. This is bounded lifecycle validation, not an engine restart/GC stress test.
- The synthetic performance harness passed after lint refactoring: commit times **0.613s, 0.738s, 0.749s** and full cycles **2.285s, 1.638s, 1.539s**, with three native commits/reconciliations, five incremental checkouts and no general merges or retained-checkout commits. This does not add real-repository or interactive-agent measurements.

Remote checks observed at merge SHA `699c5755d61a5de5b7992aa2410cc210d649cf66` had five lint violations, addressed through helper extraction and staticcheck simplifications. The seven other failing checks showed HTTP 502/connection resets, session removal/closure, or a client-caller deadline, with passing inner checks or recorded test passes. These are transport/session failures, **not proven PR test regressions or proven unrelated infrastructure defects**; their underlying cause remains unestablished. Re-read and rerun remote checks before claiming green CI.

Full CI, engine restart/eviction and GC stress, longer-lived/varied repository measurements and physical storage accounting remain release gates. The remote-native follow-up addresses the first-commit and measured nested-reconciliation fallbacks, but cold complete-history acquisition remains expensive. The earlier interactive smoke used default rather than configured human identity and predates that follow-up.

## Source map

| Concern | Entry points |
| --- | --- |
| Approval, identity, pending overlay composition | [`core/schema/workspace_commit.go`](../core/schema/workspace_commit.go), [`workspace_checkpoint.go`](../core/schema/workspace_checkpoint.go) |
| Commit repository/storage recipes | [`core/schema/git_commit_create.go`](../core/schema/git_commit_create.go), [`core/schema/git.go`](../core/schema/git.go) |
| Private index, object insertion, eligibility, cleanup | [`core/git_commit_create.go`](../core/git_commit_create.go) |
| Remote closure promotion and exact-parent history reuse | [`core/git_commit_remote.go`](../core/git_commit_remote.go), [`core/git_history_native.go`](../core/git_history_native.go) |
| Reconciliation and checkout-transition replay | [`core/changeset_native.go`](../core/changeset_native.go) |
| Incremental source materialization | [`core/git_local_incremental.go`](../core/git_local_incremental.go), [`core/git_local.go`](../core/git_local.go) |
| Provenance ownership and persistence | [`core/git.go`](../core/git.go), [`core/persisted_visitors.go`](../core/persisted_visitors.go), [`core/git_persistence_test.go`](../core/git_persistence_test.go) |
| Legacy merge oracle / filesystem delta | [`core/changeset.go`](../core/changeset.go), [`core/changeset_delta.go`](../core/changeset_delta.go) |
| Benchmark and engine oracles | [`core/integration/workspace_commit_test.go`](../core/integration/workspace_commit_test.go), [`git_commit_test.go`](../core/integration/git_commit_test.go), [`tracesink_test.go`](../core/integration/tracesink_test.go) |

Related architecture: [lazy evaluation](lazy_evaluation.md), [cache persistence](cache_persistence.md), [cache pruning](cache_pruning.md), [version gating](version-gating.md).

## Continuation priorities

1. **Establish the current PR state and address CI.** Verify the actual head, inspect failures, and keep unrelated host changes out of the branch. No broad rebase/history rewrite or force push is implied by "continue".
2. **Prepare the existing slices for independent review.** Review fallback coverage and the intentional native ref/tag retention difference. Test long sequential and concurrent histories, graceful restart/eviction, cancellation around publication, and retained snapshots during GC. Respect existing cache durability semantics rather than promising crash recovery it does not provide.
3. **Follow the controlled-origin evidence.** Keep the stable advertised ref as well as the source SHA pinned, and prove remote input provenance; live-ref movement otherwise invalidates comparisons. The remote-native first commit is now faster but still dominated by complete-history hydration, with a smaller isolated-packing cost. Measure cold versus cached-mirror behavior before choosing another optimization; never shift cost into capture or weaken object isolation. Add physical allocation and longer-lived growth measurements, which remain absent.
4. **Broaden the interactive smoke.** The isolated two-commit run passed against verified from-source binaries and exposed the cold/remote versus local distinction. Repeat with configured human identity, pending unselected edits and representative agent use. Do not push test commits or silently switch to another engine for authentication.
5. **Continue A only where measurements justify it.** Remaining loop cost includes status/diff preparation, checkpoint/overlay composition, initial materialization and metadata walks. Reducing the duration of `withCommit` alone by deferring work to the next tool is not success. Do not widen eligibility by guessing filesystem equivalence.
6. **Explore C separately when requested.** Compare per-repository/session stores and immutable pack sharing before proposing a global store. Specify reachability/pinning, graceful persistence versus crash durability, GC, authorization boundaries, shallow/partial/object-format support, concurrency/repacking, and logical versus physical byte accounting. Existing snapshot-owned objects are the current foundation, not an implemented object service.

Direction B remains deferred. Reaching a universal one-second bar is not a prerequisite for reviewing these conservative gains.

## Do not repeat these dead ends

- Returning a raw Directory for an apparently trivial merge changed permissions, ownership and attribute conversion. The current reconciliation deliberately replays Git's changed-path transitions instead.
- Caching an initialized whole-tree temporary merge repository risked retaining repository-sized hidden `.git` layers after deletion.
- Merely borrowing objects while still running the old full-tree `git add` reduced writes but did not improve the real benchmark. Current native staging avoids that baseline reconstruction.
- `Directory.digest` differences can include `trusted.overlay.origin`; earlier legacy-versus-legacy experiments proved this. Compare explained consumer manifests, not unexplained digest equality, and do not broadly ignore arbitrary xattrs.
- A reported `dagger agent --trace` host-read issue through `/src` remains outside this work. Do not imply it was fixed.
