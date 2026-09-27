# Git-Native Workspace Commits

This document describes how `GitRef.withCommit` and `Workspace.withCommit` record small edits without leaving Git's own representation: commits are built directly from object databases, history is acquired only on demand, and source trees are updated incrementally.

The source of truth is the code, mainly:

- `core/git_commit_create.go`: native commit construction and eligibility
- `core/git_commit_remote.go`, `core/git_local_history.go`, `core/git_history_native.go`: remote promotion and history hydration
- `core/changeset_native.go`: native workspace reconciliation
- `core/git_local_incremental.go`, `core/git_local.go`: incremental source checkout and checkout provenance
- `core/git_host_history.go`, `engine/session/git/git_pack_commit.go`, `engine/engineutil/git_history.go`: approved host-history donation
- `core/changeset.go`, `core/changeset_delta.go`: changeset scan reuse
- `core/schema/git_commit_create.go`, `core/schema/workspace_commit.go`: resolvers and private recipes

No public API was added. The new schema fields (`__withCommitRepository`, `__withCommitDirectory`, `__nativeCommitBase`, `__hydrateRepository`, `__mergeForWorkspaceCommit`) are internal.

## The Problem

A workspace commit of a one-line edit used to leave Git and rebuild it from a filesystem several times:

```text
Git commit -> Directory -> temporary Git merges -> retained full checkout
           -> git add/commit -> fresh source checkout -> pending filesystem diff
```

Each step scaled with the repository, not the edit:

- **Repeated full checkouts.** The legacy commit path (`GitCommitChangeset`) materializes a complete checkout with `.git`, applies the changeset, and stages it. The next read of the committed tree then does another full source checkout.
- **Refetching cached history.** A remote-backed parent had to be fetched with complete history before it could become a local commit, and history comparisons between the new local commit and its remote parent (ahead/behind, log) fetched again.
- **Full-tree rescans.** Workspace reconciliation merged changesets by reconstructing trees, and the surrounding status/diff machinery walked both full trees several times for the same changeset.

The intrinsic work is much smaller: stage the changed blobs, write the affected trees and one commit object, keep unselected edits pending, and reuse unchanged filesystem content. Each mechanism below removes one class of repeated work. Every one of them is an optimization over an existing path that remains in place as the fallback.

## Native Commit Construction

Entry points: `GitCommitChangesetNative` and `withNativeCommitIndex` in `core/git_commit_create.go`, called from `gitRefWithCommitDirectory` in `core/schema/git_commit_create.go`.

`GitCommitChangesetNativeBase` first proves same-base provenance without evaluating either directory: the changeset's `Before` must be a direct `DirectoryGitTreeLazy` over the same commit SHA, with Git metadata discarded, and its repository must have the same **recipe digest** as the parent. Recipe identity carries authentication and service bindings; equal-looking file contents or a subdirectory/filtered tree are not enough. The parent ref must be a branch (`refs/heads/...`) or a detached commit (a schema-resolved ref whose `Name` equals its SHA).

The transaction then:

1. Obtains owned repository storage (`nativeCommitRepository`): the local repository directly, or a promoted closure for a remote parent (next section).
2. Creates a COW child of that storage snapshot via `withGitMergeWorkspace`, and separately mounts the existing storage **read-only** to borrow its objects.
3. Initializes private scratch metadata, a private index and an empty sparse worktree, with the borrowed object directory as `GIT_ALTERNATE_OBJECT_DIRECTORIES`.
4. `stageNativeChanges`: `read-tree` the parent, check out only `.gitattributes`/`.gitignore` files on the changed paths' ancestor chains, apply the changeset, then `update-index --force-remove` removals and `add -A` additions/modifications. Unchanged blobs are never read.
5. `write-tree`, then `commit-tree` with explicit author/committer identity and dates. Signoff uses `git interpret-trailers --no-divider`, matching `git commit --trailer`.
6. `copyNativeCommitObjects` copies only the transaction's new loose objects into the COW child (`O_EXCL`, never touching existing objects), then `publishNativeCommit` writes `HEAD`/the branch ref and a fresh config with rooted writes, and removes index/logs.

New objects are written to scratch first because Git may freshen the mtime of an existing pack in its primary object store; pointed at the writable child it could copy up a history-sized pack. The borrowed mount must be genuinely read-only for the same reason. The result is self-contained: snapshot ancestry shares all existing objects, and no scratch index or alternates file is persisted.

## Remote Promotion and Lazy History

Entry points: `GitRemoteCommitBase` and `packRemoteCommitBaseDepth` in `core/git_commit_remote.go`; `fullHistory`, `HydrateGitRepository` and `mountHistory` in `core/git_local_history.go`; `mountOwnedShallowParentHistory` and `nativeParentHistoryRefs` in `core/git_history_native.go`.

A remote parent is promoted through the private `GitRef.__nativeCommitBase(depth)` field, selected on the ref pinned to its resolved SHA so that reconciliation and commit construction share one promotion. Its cache key (`gitRefNativeCommitBaseKey`) is the parent's exact recipe digest, not its content digest, which may alias equivalent refs across authorization scopes.

Promotion goes through the normal authenticated mirror fetch, then, holding the mirror lock and a read-only mount, runs `pack-objects --revs` for the pinned commit into a private bare repository. The shared mirror is keyed by URL and may hold objects and refs from other authorization scopes, so it is never copied wholesale and its refs are never inherited. Ordinary commits request **depth one**: only the commit, its tree and blobs, with an explicit `shallow` file naming that SHA, even if the mirror already holds full history.

The committed repository records the pinned remote ref as `LocalGitRepository.HistorySource`: one exact remote anchor, inherited unchanged by descendant commits. Raw storage operations (native staging, reconciliation, source-only checkout, short logs) work against the shallow storage. Consumers that need deeper history (`mountHistory` with a depth reaching the boundary, full retained checkouts, bundles) call `fullHistory`, which selects `HistorySource.__hydrateRepository(directory)`: the complete authorized closure (`__nativeCommitBase(depth: 0)`) is merged with the local storage in a new snapshot, and the shallow boundary is removed only after the complete pack succeeded. That field's key combines the source recipe and the directory's structural input digest, so content-equivalent aliases never share owned provenance.

History comparisons between a committed child and its exact parent (e.g. ahead/behind against the remote branch) are answered from the child's own objects when the child's raw commit headers name that parent as the single parent and the repository recipes match (`nativeParentHistoryCandidate`, `validateNativeParentHistory`). A direct-parent comparison is complete even with unknown older ancestry. The substitution lasts only for that read; no remote-to-local alias is published.

## Native Workspace Reconciliation

Entry points: `TryNativeWorkspaceMerge` and `nativeWorkspaceMerge` in `core/changeset_native.go`, called from `changesetMergeForWorkspaceCommit` (`Changeset.__mergeForWorkspaceCommit`) in `core/schema/workspace_commit.go`.

`Workspace.withCommit` merges the committed changeset into the approved working tree as well as into HEAD, so incoming and unselected edits both survive. When both the workspace's uncommitted changeset and the incoming one pass `GitCommitChangesetNativeBase` against HEAD, the uncommitted changeset is reused directly instead of being re-derived by applying it to HEAD; empty or off-baseline inputs keep the complete-tree path.

The native merge builds two temporary commits (working and incoming) with the same sparse private-index staging as commit construction, then runs `git merge-tree --write-tree --merge-base=<parent>`. It does **not** return the merged Git tree as a Directory. Instead, on a COW child of the working tree's `Before`, it replays the filesystem transitions the legacy checkout sequence performs:

```text
Before
  + raw working delta,  then checkout: working tree  -> parent
  + raw incoming delta, then checkout: incoming tree -> working tree
  + checkout: working tree -> merged tree
```

`nativeWorkspaceCheckout` rewrites only entries that differ between two trees. That matters because the legacy path's filesystem result is not a pure function of the Git tree: entries Git skips keep their incoming raw bytes (including CRLF), permissions, ownership and xattrs, while rewritten entries are normalized. Returning the raw merged tree, or normalizing everything, each change metadata that existing callers observe. Only declared changed paths and the delta's directory metadata are inspected (`validateNativeWorkspaceContent`, `validateNativeWorkspaceBase`); the baseline is never walked or hashed. Temporary commits and indexes are discarded.

## Incremental Canonical Source Checkout

Entry points: `GitCheckoutBase` and `validateTree` in `core/git_local.go`; `incrementalTree`, `planIncrementalGitCheckout` and `applyIncrementalGitCheckout` in `core/git_local_incremental.go`; `gitRefWithCommitRepository` in `core/schema/git_commit_create.go`.

`GitRef.withCommit` selects the private `__withCommitRepository` recipe, which records checkout provenance on the resulting `LocalGitRepository`:

```go
type GitCheckoutBase struct {
	Parent    dagql.ObjectResult[*GitRef]    // exact parent recipe (remote refs pinned to SHA)
	CommitSHA string                         // resolved HEAD of the new commit
	Tree      dagql.ObjectResult[*Directory] // remote parents only: evaluated tree(discardGitDir: true)
}
```

These are owned DAG dependencies, not mount paths or guesses from a dirty worktree, and public `withContents` never infers them. A remote parent's tree is pinned and evaluated up front so reuse never needs another fetch after mirror eviction; `validateTree` checks that it was produced by exactly `Parent.tree(discardGitDir: true)`.

When `LocalGitRef.Tree(discardGitDir: true)` is requested for exactly `CommitSHA`, `incrementalTree`:

1. Validates the storage layout, that the commit's raw headers name `Parent` as its single parent (bypassing replace refs and grafts), and the checkout controls, before evaluating anything else.
2. Takes the parent's canonical tree (pinned for remote parents, `Parent.tree(discardGitDir: true)` via DagQL for local ones) and creates a COW child of its snapshot.
3. Uses a private index and the same clean checkout config as a full checkout to remove old leaves and `checkout-index` only the `diff-tree` delta.
4. Normalizes the rewritten paths, their ancestors and the root to mtime 1, as a full checkout would; untouched inodes are left alone.

Other refs of the same repository never inherit the annotated tip's materialization, and retained `.git` checkouts keep the full path. A cold parent may still need one full materialization; the point is that each subsequent commit costs its delta.

## Approved Host-History Donation

Entry points: `RegisterCapturedHostHistory`, `approvedHostCommitPack` and `importHostCommitPack` in `core/git_host_history.go`; `GitAttachable.PackCommit` in `engine/session/git/git_pack_commit.go`; `ReceiveGitCommitPack` in `engine/engineutil/git_history.go`; registration in `core/schema/workspace_checkpoint.go`.

For interactive workspaces the client's checkout usually already holds the history that complete-history hydration would download. After a clean, owning-client, remote-backed capture, the checkpoint registers a donor on the session's `Query`, keyed by the exact repository recipe digest and captured remote anchor SHA, recording the owner client ID, checkout path and captured state digest. The registry is session-local and never persisted; the remote recipe remains the reconstruction path for replay and other clients.

Donation is consulted only for depth-zero promotion (complete-history demand); capture and ordinary depth-one commits never request it. The engine calls the separate `PackCommit` RPC on the owning client only, if its attachable is available. `PackCommit` is deliberately distinct from `PackCheckout`, which sends all branches and tags, so an older client can never interpret a scoped request as permission to send more. The client verifies the checkout state digest before and after packing, borrows only the object database into scratch metadata (no refs, config, replace refs or lazy fetching), rejects shallow donors and incomplete closures, and streams a non-thin pack of exactly the requested commit's closure.

The engine imports the pack into an isolated repository with `index-pack --strict`, runs `fsck --strict`, and requires the object inventory to equal the commit's closure exactly before publishing an owned snapshot. Remotes are written from the engine's recipe, never from donor config. A missing, moved, shallow or incomplete donor reports `HISTORY_UNAVAILABLE`; that and any other donor failure (host pack errors and timeouts, size limits, lost transports, or a pack the import rejects, which is discarded uncommitted) makes the engine fetch from the authorized remote instead, unless the caller's own context is done.

## Changeset Scan Reuse

Entry points: `changesetFilter` in `core/schema/directory.go`; `DiffStats`, `NewChangeset` and `appliedOntoOwnBaseline` in `core/changeset.go`; `changesetLineStats` in `core/changeset_delta.go`.

The surrounding status/diff/commit loop walked the same full trees repeatedly. Three changes remove that:

- `Changeset.filter()` with no include or exclude patterns returns its receiver instead of copying both trees to rebuild an identical changeset.
- `DiffStats` goes through the memoized `ComputePaths` scan and then reads only changed files for line counts; equal-digest sides skip mounting entirely.
- `NewChangeset` recognizes `base.withChanges(C).changes(base)` where `base` is also `C.Before` (by content-preferred digest) and delegates path queries to `C`. It delegates rather than sharing `C`'s memo: computing the new changeset directly evaluates `base.withChanges(C)`, which needs `C`'s paths first, so a shared `sync.Once` would wait on itself.

## Invariants

- **Authorization and identity.** Promotion, hydration, parent-history reuse and host donation are all scoped by exact recipe digests, never by URL, SHA or content equality. The shared mirror is read, never published from.
- **Borrowed objects are read-only.** Every borrowed object database is an actual read-only mount for the whole operation. Outputs are COW children holding only new objects.
- **No persisted alternates.** Alternates exist only in scratch metadata during an operation. Every published snapshot owns (through ancestry) all objects it references.
- **Provenance is owned and persisted.** `CheckoutBase` and `HistorySource` are attached as dependencies in `LocalGitRepository.attachDependencyResults`, encoded in `GitRepository.EncodePersistedObject` (`persistedGitCheckoutBase`) and walked in `core/persisted_visitors.go`, so they survive cache persistence with their storage.
- **Engine-side repositories are pinned** to `--object-format=sha1 --ref-format=files`. Publication writes loose refs and copies the scratch config; if Git defaulted to reftable (Git 3.0, `init.defaultRefFormat`) that config would declare reftable over loose refs.
- **Ref/tag retention differs intentionally.** Native publication in local storage keeps the parent storage's unrelated refs and tags, whereas a fresh fetch would prune them. Remote promotion carries no refs beyond `HEAD`. Tag-listing semantics on committed repositories can therefore differ from the legacy path.

## Fallback Policy

Every native path is an optimization; the legacy path is always correct. The policy is uniform:

- **Unsupported configurations take the legacy path up front**, recorded as a fixed reason code on the span (never paths or refs). From the code: non-branch/non-detached refs; `.git` gitfiles or symlinks; linked worktrees; foreign shallow boundaries; object alternates; partial clones/promisors; non-SHA-1 object formats; reftable ref storage in the source repository; `.gitmodules` changes and changes touching gitlinks (for incremental checkout, gitlinks anywhere in either tree); merge commits or annotated parents that are not the actual single parent; `.gitattributes` changes (checkout) or `.gitattributes`/`.gitignore` changes (reconciliation); empty directory additions, ignored paths, non-canonical base blobs, unreported filesystem changes and directory metadata/xattr changes (reconciliation).
- **Any other error in a native path also falls back**: commit construction, reconciliation, incremental checkout and host donation all retry through the legacy commit, the general `__mergeWithChangeset`, a full checkout, or the authorized remote respectively, recording the error as the fallback reason in telemetry. The single exception is cancellation of the caller's context, which is returned.
- **Checkout-base provenance that fails validation is dropped**, not fatal: the repository simply behaves as one without a `CheckoutBase` and takes the full checkout path.
- **Chains are bounded.** Each incremental checkout and native commit adds a snapshot layer over its parent. Beyond a fixed chain depth the full path is taken instead, resetting the chain and avoiding unbounded overlay layer depth.

## Limitations and Non-Goals

- No durability beyond the existing [cache persistence model](cache_persistence.md): graceful persistence is best-effort, and nothing here survives an engine crash that the cache would not.
- Physical storage growth (retained layers, promoted closures, hydrated history) has not been measured. The `dagger.git.native.new_object_bytes` span attribute counts compressed loose-object bytes, not disk allocation.
- A cold remote-backed first commit still pays one depth-one promotion and one full source materialization; genuinely old history still pays hydration, from the host when donated and from the remote otherwise.

**Direction B, Git trees as a general Directory backend**, would represent Directories as Git trees so diff and merge become tree operations everywhere. It is deferred because it changes Directory's representation across lazy evaluation, persistence and every consumer, and Git trees cannot express the ownership, xattrs and timestamps Directory must preserve; the reconciliation section shows how subtle that gap is even in one place.

**Direction C, an engine-owned Git object store**, would keep objects in a shared per-engine store instead of per-snapshot ownership. It is deferred because it needs its own reachability/GC, durability, authorization-scope and concurrency/repacking contracts. The current design keeps every object owned by a snapshot scoped to an exact recipe.

## Testing

Unit tests (`go test -race ./core ./core/schema ./engine/session/git ./engine/engineutil -count=1`):

- `core/git_commit_test.go`: `TestGitNativeCommitMatchesCheckout` (native vs legacy commit objects), `TestGitNativeCommitPublicationIsRooted`, `TestGitNativeCommitRejectsGitlinks`, `TestGitNativeCommitRefStorageEligibility`, `TestGitNativeCommitStorageEligibility`, `TestGitNativeCommitObjectMetrics`; `core/git_commit_create_test.go`: `TestNativeCommitFallback`.
- `core/git_commit_remote_test.go`: promotion provenance, isolation from unrelated mirror objects/refs, shallow promotion, fallbacks. `core/schema/git_lazy_test.go`: `TestNativeCommitBaseCacheScope`.
- `core/git_history_native_test.go`, `core/git_history_test.go`: parent-history provenance and raw headers, cached/shallow history joins.
- `core/changeset_native_test.go`: `TestNativeWorkspaceMergeMatchesCheckout` (reconciliation vs legacy checkout sequence, including metadata), fallback and base-evidence tests.
- `core/git_local_incremental_test.go`: incremental checkout vs full checkout, gates, actual-parent and provenance checks. `core/git_persistence_test.go`: checkout-base and remote tree persistence.
- `core/git_host_history_test.go`, `engine/session/git/git_pack_commit_test.go`, `engine/engineutil/git_history_test.go`: donor scoping, exact-closure packs, unavailable donors, import validation.

Integration tests run against a from-source engine, e.g. `dagger call engine-dev test --pkg ./core/integration --run 'TestGit/TestGitRefWithCommitNative'`:

- `core/integration/git_commit_test.go` (`TestGit`): `TestGitRefWithCommitNative`, `TestGitRefWithCommitReftable`, `TestGitRefNativeCommitHistory` (56 sequential and concurrent commits with fsck and checkout equivalence), `TestGitRefIncrementalCheckoutOracle`, `TestGitRefIncrementalCheckoutTrace`, `TestGitRefRetainedCheckoutSurvivesSourceScope`.
- `core/integration/workspace_commit_test.go` (`TestWorkspace`): `TestWorkspaceWithCommitReconciliationOracle`, `TestWorkspaceWithCommitNativeReconciliationTrace`, `TestWorkspaceRemoteFirstNativeCommit`, `TestWorkspaceWithCommitScopedHistory`, `TestWorkspaceWithCommitSignoff`.
- `core/integration/workspace_remote_history_test.go` (`TestWorkspace`): `TestWorkspaceRemoteParentHistoryDoesNotFetch`, `TestWorkspaceRemoteLazyHistory{Ordinary,Demand,Unavailable}`, `TestWorkspaceApprovedHostHistory{Ordinary,Offline,Fallback}`.
- `core/integration/changeset_test.go` (`TestChangeset`): `TestFilterWithoutPatternsSelectsAll`, `TestChangesOfAppliedChangeset`.

The "trace" tests assert on span names and attributes that the native path actually ran (no silent fallback, no repeated full materialization or fetch); the "oracle" tests compare native results with the legacy path.

## Performance

Two opt-in harnesses measure the loop: `TestWorkspaceScopedCommitPerformance` (synthetic 12,000-file packed repository, no remote) and `TestWorkspaceRealRepositoryPerformance` and `TestWorkspaceRealRepositoryPerformanceControlledOrigin` in `core/integration/workspace_realbench_test.go` (a full-history dagger/dagger checkout, the latter served through a local origin with a frozen ref advertisement; both run only when named explicitly with `-run`). Representative medians, **indicative only, not a benchmark**: small samples on shared hosts.

| Scenario | Before | After |
| --- | ---: | ---: |
| Synthetic 12k-file fixture, scoped `withCommit` | ~11.1s | ~0.74s |
| dagger/dagger, later scoped `withCommit` | ~19.0s | ~1.5s |
| dagger/dagger, interactive agent commit tool call | ~6.8s | ~3.1s |
