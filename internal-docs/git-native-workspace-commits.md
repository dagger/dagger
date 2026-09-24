# Git-native workspace commits: design and continuation

**Status:** implemented Direction A prototype in [PR #14314](https://github.com/dagger/dagger/pull/14314), still draft. No new public GraphQL API. The implementation checkpoint described here is `460380658e5b95975ab084db71f706676a432bad`; use the PR's current head for continuation, not this historical hash.

This is the starting document for a fresh session asked to **"continue https://github.com/dagger/dagger/pull/14314"**. It supersedes the implementation status and next steps in the [earlier investigation handoff](https://gist.github.com/vito/aead3f59d5aec76d824797c4c00f7f3a). The code is authoritative when it differs from this document.

## Start here

1. Read this document and the current PR description/checks.
2. Query the PR's actual head repository, branch and SHA. At this checkpoint the repository is **`vito/dagger`**, branch **`fix-agent-git-history-no-fetch`**, targeting `dagger/dagger`.
3. Work in that checkout, preserving unrelated host edits. With the workspace checkout tool, use the branch name, then verify HEAD against GitHub. That tool's `branch` argument does not accept a commit SHA. A previous session encountered stale branch resolution, so do not skip verification.
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
| **C: Engine-owned Git object storage** | Interesting future exploration, but a separate design. This PR does not introduce a global mutable bare repository or object service. |

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
2. Hold the local repository's read-only mount.
3. Initialize private Git metadata, an operation-local index, and a sparse staging worktree.
4. Use `read-tree` for the parent, hydrate ancestor attribute/ignore controls, and stage only the selected delta.
5. Use `write-tree` and `commit-tree` with explicit identity, message, dates and parentage.
6. Write new objects into scratch storage, then copy only new loose objects into a copy-on-write child of the existing repository snapshot.
7. Return its self-contained Git storage. Persist no scratch index or mount-path alternate.

Git may freshen the timestamp of an existing pack in either a primary or alternate object database. Pointing it at a writable inherited pack risks a history-sized copy-up. The borrowed parent mount must actually be **read-only**, not merely chmod-protected.

A schema resolver can represent a detached ref as `Name == SHA`, not only an empty name. The native transaction recognizes that exact form without mutating the shared ref. An early benchmark silently fell back until this was fixed; telemetry now identifies support and fixed-code fallback reasons.

### 2. Native workspace reconciliation

`Workspace.withCommit` still freezes the receiver and resolves author identity before constructing replayable results. It uses the private `Changeset.__mergeForWorkspaceCommit` field for reconciliation; generic Directory/Changeset merge APIs are unchanged.

For verified same-base local inputs, the already-approved changeset is reused instead of applying it to HEAD and computing the same delta again. Empty or off-baseline inputs retain the complete-tree reconstruction path.

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

The private `GitRef.__withCommitRepository` recipe records the exact local parent and the resulting resolved HEAD. `LocalGitRepository.CheckoutBase` contains:

```go
type GitCheckoutBase struct {
    Parent    dagql.ObjectResult[*GitRef]
    CommitSHA string
}
```

This is an owned DAG dependency, with a persistence codec and reference visitor. It is not an expired mount path, a guess based on a dirty worktree, or public `withContents` metadata supplied by a caller.

For a source-only checkout of exactly the annotated SHA:

1. Validate the current local storage, actual single-parent commit headers, and checkout controls before evaluating the parent tree.
2. Select the parent's canonical `tree(discardGitDir: true)` through DagQL, reusing its cached materialization when available.
3. Create a COW child of that canonical snapshot.
4. Borrow current repository objects read-only; use private Git metadata/index to check out only the Git tree delta.
5. Normalize rewritten paths, touched ancestors and the root to Unix second 1. Leave untouched files/inodes alone.

Other refs must not inherit the annotated tip's materialization. Retained `.git` checkouts remain on the existing self-contained path. Changed attributes or submodules trigger a full source checkout because unchanged blobs can acquire different checkout bytes.

A cold parent may still need materialization. This design removes repeated full materialization; it does not promise that no filesystem is ever constructed.

## Semantics, fallbacks and safety

| Area | Current boundary |
| --- | --- |
| Commit transaction | Direct same-base local SHA-1 provenance; complete supported local storage. Unsupported refs/storage/submodule edits use the existing path. |
| Workspace reconciliation | Same-base local Git inputs. Changed controls, ignored paths, empty directories, unsupported delta metadata and storage layouts can fall back. |
| Incremental checkout | Source-only, exact annotated SHA, supported complete local SHA-1 storage, local parent, actual single parent. Changed `.gitattributes`/`.gitmodules` and any gitlinks fall back. |
| Public APIs | Existing methods and signatures remain. New schema fields are internal-only. |
| Rich filesystem metadata | Match existing checkout behavior, including checkout-skip retention. Do not equate Git tree equality with filesystem equality. |
| Ref namespace | Native commit storage deliberately retains unrelated source refs/tags rather than reproducing a fresh fetch's pruning. Review this intentional difference before merging. |
| Errors | Fallback only when every error leaf is explicitly unsupported. Missing objects, cancellation, deadlines and cleanup errors remain errors. |

Rooted filesystem operations prevent writes or metadata inspection through replaced symlink ancestors. Produced snapshots are released if cancellation or source-unmount failure arrives after their creation. Cleanup uses an uncancelled context and preserves release failures.

### Storage lifetime is not crash durability

Existing snapshot ancestry shares and pins inherited object storage and canonical source trees. Explicit DagQL dependency attachment, encoding/decoding, and reference visitation retain checkout provenance across valid cache persistence.

This uses the existing [cache persistence model](cache_persistence.md): graceful persistence is best-effort, and an unclean or invalid cache can be discarded. **No new guarantee of surviving an engine crash or ungraceful shutdown is claimed.** A future Direction C must decide its own durability, reachability, authorization and GC contracts.

Do not confuse counters for new compressed loose-object files with physical disk allocation. Full allocation, retained parent layers, long-chain compaction and GC behavior still need broader measurement.

## Measurements

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

The original 17.182s live-agent result was a different workload/environment. The new numbers measure core API work, **not LLM latency or the complete committer module/TUI tool call**. No fresh interactive-agent smoke test was completed for these native slices.

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

^TestGit$/(TestGitRefIncrementalCheckoutOracle|TestGitRefIncrementalCheckoutTrace|TestGitRefRetainedCheckoutSurvivesSourceScope)$

^TestWorkspace$/(TestWorkspaceWithCommitReconciliationOracle|TestWorkspaceWithCommitNativeReconciliationTrace)$

^(TestGit|TestWorkspace)$/(TestGitRefWithCommit|TestGitRefWithCommitNative|TestWorkspaceWithCommitScopedHistory|TestWorkspaceWithCommitResolvedIdentityReplay)$
```

Coverage includes exact commit objects, pending path sets, conflicts, metadata, attributes, source immutability, concurrent detached commits, retained repository lifetime, and incremental/full-checkout equivalence. Backend tests assert canonical root timestamps; container inspection wrappers can replace root mtime, so consumer tests compare root metadata but assert timestamp 1 only for descendants.

Persistence coverage verifies exact provenance dependency retention across **two cache restarts**. It is not a full engine restart/GC stress test. Full CI, larger/varied repositories, long commit chains and actual agent workflows remain release gates.

The latest observed remote CI before this document's publication had `golangci-lint:lint-all` in `ERROR` at merge SHA `5c3546bb7d3e2dd740ea083daa9944f50c88d727`, for the previously published code. It was not investigated. **Re-read checks for the new PR head; do not assume the old error is unrelated or that these local results imply green CI.**

## Source map

| Concern | Entry points |
| --- | --- |
| Approval, identity, pending overlay composition | [`core/schema/workspace_commit.go`](../core/schema/workspace_commit.go), [`workspace_checkpoint.go`](../core/schema/workspace_checkpoint.go) |
| Commit repository/storage recipes | [`core/schema/git_commit_create.go`](../core/schema/git_commit_create.go), [`core/schema/git.go`](../core/schema/git.go) |
| Private index, object insertion, eligibility, cleanup | [`core/git_commit_create.go`](../core/git_commit_create.go) |
| Reconciliation and checkout-transition replay | [`core/changeset_native.go`](../core/changeset_native.go) |
| Incremental source materialization | [`core/git_local_incremental.go`](../core/git_local_incremental.go), [`core/git_local.go`](../core/git_local.go) |
| Provenance ownership and persistence | [`core/git.go`](../core/git.go), [`core/persisted_visitors.go`](../core/persisted_visitors.go), [`core/git_persistence_test.go`](../core/git_persistence_test.go) |
| Legacy merge oracle / filesystem delta | [`core/changeset.go`](../core/changeset.go), [`core/changeset_delta.go`](../core/changeset_delta.go) |
| Benchmark and engine oracles | [`core/integration/workspace_commit_test.go`](../core/integration/workspace_commit_test.go), [`git_commit_test.go`](../core/integration/git_commit_test.go), [`tracesink_test.go`](../core/integration/tracesink_test.go) |

Related architecture: [lazy evaluation](lazy_evaluation.md), [cache persistence](cache_persistence.md), [cache pruning](cache_pruning.md), [version gating](version-gating.md).

## Continuation priorities

1. **Establish the current PR state and address CI.** Verify the actual head, inspect failures, and keep unrelated host changes out of the branch. No broad rebase/history rewrite or force push is implied by "continue".
2. **Prepare the existing slices for independent review.** Review fallback coverage and the intentional native ref/tag retention difference. Test long sequential and concurrent histories, graceful restart/eviction, cancellation around publication, and retained snapshots during GC. Respect existing cache durability semantics rather than promising crash recovery it does not provide.
3. **Broaden end-to-end measurements.** Use the actual Dagger repository as well as the synthetic fixture. Vary edited paths, add/remove/rename files, consume trees in containers, and measure source/object storage amplification. Preserve outliers and distinguish cold capture, changing-HEAD reuse, and genuinely warm work.
4. **Run a fresh interactive-agent smoke test against verified from-source binaries.** In an isolated workspace, ask it to make a one-line comment edit, review status/diff/log, commit only that file, then read the latest two commits. Do not push benchmark edits. Verify engine targeting and model authentication; do not silently fall back to another engine for convenience.
5. **Continue A only where measurements justify it.** Remaining loop cost includes status/diff preparation, checkpoint/overlay composition, initial materialization and metadata walks. Reducing the duration of `withCommit` alone by deferring work to the next tool is not success. Do not widen eligibility by guessing filesystem equivalence.
6. **Explore C separately when requested.** Compare per-repository/session stores and immutable pack sharing before proposing a global store. Specify reachability/pinning, graceful persistence versus crash durability, GC, authorization boundaries, shallow/partial/object-format support, concurrency/repacking, and logical versus physical byte accounting. Existing snapshot-owned objects are the current foundation, not an implemented object service.

Direction B remains deferred. Reaching a universal one-second bar is not a prerequisite for reviewing these conservative gains.

## Do not repeat these dead ends

- Returning a raw Directory for an apparently trivial merge changed permissions, ownership and attribute conversion. The current reconciliation deliberately replays Git's changed-path transitions instead.
- Caching an initialized whole-tree temporary merge repository risked retaining repository-sized hidden `.git` layers after deletion.
- Merely borrowing objects while still running the old full-tree `git add` reduced writes but did not improve the real benchmark. Current native staging avoids that baseline reconstruction.
- `Directory.digest` differences can include `trusted.overlay.origin`; earlier legacy-versus-legacy experiments proved this. Compare explained consumer manifests, not unexplained digest equality, and do not broadly ignore arbitrary xattrs.
- A reported `dagger agent --trace` host-read issue through `/src` remains outside this work. Do not imply it was fixed.
