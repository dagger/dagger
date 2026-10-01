# Git-Native Workspace Commits

This document describes how `GitRef.withCommit` and `Workspace.withCommit` record small edits by working directly on Git object databases, rather than rebuilding repositories from filesystems.

An agent workspace commit of a one-line edit used to materialize a complete checkout with `.git`, apply the changeset, stage and commit it, and then check the result out again, re-fetching history the engine already had cached along the way. The Git commit itself was the cheap part. The intrinsic work is to stage the changed blobs, write the affected trees and one commit object, and reuse everything else. The direction here is to do that Git work natively, over cached objects, with each mechanism an optimization over an existing path that remains in place as the fallback.

The source of truth is the code, mainly:

- `core/git_commit_create.go`: native commit construction, eligibility and the fallback policy
- `core/changeset_native.go`: native workspace reconciliation
- `core/schema/git_commit_create.go`, `core/schema/workspace_commit.go`: the `GitRef.withCommit` and `Workspace.withCommit` resolvers

## Native Commit Construction

Entry points: `GitCommitChangesetNative` and `withNativeCommitIndex` in `core/git_commit_create.go`, called from `gitRefWithCommitDirectory` in `core/schema/git_commit_create.go` before the general three-way reconciliation.

`GitCommitChangesetNativeBase` first proves same-base provenance without evaluating or hashing either directory. The parent must be a local ref resolved to a full SHA-1, and the changeset's `Before` must be a direct `DirectoryGitTreeLazy` over that same commit, with Git metadata discarded, from a repository with the same **recipe digest** as the parent's. Recipe identity carries authorization scope; equal-looking contents, subdirectories and filtered trees take the general path.

The transaction then:

1. Creates a COW child of the parent repository's snapshot via `withGitMergeWorkspace`, and separately mounts the existing storage read-only to borrow its objects.
2. Initializes a private bare scratch repository (`--object-format=sha1 --ref-format=files`) with the source's remotes, a private index and an empty sparse worktree. The borrowed object directory is passed as `GIT_ALTERNATE_OBJECT_DIRECTORIES`, with lazy fetching and replace refs disabled.
3. Runs `read-tree` on the parent, then `ls-tree` on only the changed paths' ancestors, and checks out only the `.gitattributes`/`.gitignore` files on those chains. Unchanged blobs are never read.
4. Applies the changeset to the sparse worktree, runs `update-index --force-remove` for removals (including file/directory replacements) and `add -A` for additions and modifications.
5. Runs `write-tree`, returning `ErrNothingToCommit` when the tree is unchanged and empty commits are not allowed, then `commit-tree` with explicit author/committer identity and dates. Signoff uses `git interpret-trailers --no-divider`, matching `git commit --trailer`, so a `---` line in the message stays text.
6. `copyNativeCommitObjects` copies only the transaction's new objects into the child: loose objects, plus the pack (with `.idx`/`.rev`) that `git add` writes for blobs over `core.bigFileThreshold`. Writes use `O_EXCL` and never touch existing objects.
7. `publishNativeCommit` advances the selected branch, or detaches `HEAD` for a commit-ID parent, writes a fresh config, and removes the index, logs, hooks, `ORIG_HEAD` and `COMMIT_EDITMSG`. The engine never runs hooks, and since the result selects the Git directory itself, a hook symlinked into the source worktree would otherwise escape it. All writes are rooted in the child and replace files rather than truncating them, so inherited symlinks and hardlinks cannot redirect them.

The result is a `Directory` selecting the bare Git directory in the child snapshot. Snapshot ancestry shares every existing object, and no scratch index, ref or alternates file is persisted. New objects are written to scratch first because Git may freshen the mtime of an existing pack in its primary object store; pointed at the writable child, it could copy up a history-sized pack.

Unlike a fresh fetch, native publication keeps the parent storage's unrelated refs and tags. A ref whose `Name` equals its SHA (as the schema's SHA resolvers produce) is a detached commit, not a branch to advance.

The span `git native commit transaction` records `dagger.git.native.supported`, `dagger.git.native.fallback_reason`, and `dagger.git.native.new_objects`/`new_object_bytes`. The byte count covers the compressed loose-object and transaction-pack files copied into the child, not disk allocation.

### Eligibility

Unsupported inputs are rejected with a fixed `nativeCommitUnsupportedReason` code:

- `git-directory-layout`, `object-directory-layout`: `.git` is a gitfile or symlink, or `objects` is not a directory
- `linked-worktree`, `shallow-history`, `object-alternates`, `partial-repository`: storage the snapshot does not fully own
- `object-format`: anything but SHA-1
- `ref-storage`: reftable refs, which loose-ref publication and the replaced config would lose
- `ref-kind`: a parent ref that is neither a branch nor a detached commit
- `gitmodules-change`, `gitlink-change`: changes touching `.gitmodules` or gitlinks
- `unsafe-write-path`: a publication path through an inherited non-directory
- `snapshot-depth`: see below

`nativeCommitFallback` reports whether every leaf of a joined or wrapped error is such a code, so helpers can tell an expected ineligibility from a real failure joined to it.

## Workspace Reconciliation

Entry points: `TryNativeWorkspaceMerge` and `nativeWorkspaceMerge` in `core/changeset_native.go`, called from `changesetMergeForWorkspaceCommit` (the private, persistable `Changeset.__mergeForWorkspaceCommit` field) in `core/schema/workspace_commit.go`.

`Workspace.withCommit` merges the committed changeset into the approved working tree as well as into HEAD, so incoming and unselected edits both survive. When both the uncommitted and the incoming changeset pass `GitCommitChangesetNativeBase` against HEAD, the uncommitted changeset is the working delta as-is. Otherwise, for empty or off-baseline inputs, the working tree is rebuilt by applying the uncommitted changeset to HEAD and diffed against the incoming changeset's `Before`. `__mergeForWorkspaceCommit` then tries the native merge and falls back to the general fail-on-conflict `__mergeWithChangeset`, which restages the whole baseline in temporary Git repositories.

The native merge proves the same provenance for both changesets against the working tree's `Before`, then mounts the local repository read-only to borrow its objects. In a private bare scratch repository it:

1. Stages each changeset in its own sparse worktree and private index with `stageNativeChanges`, the staging shared with commit construction, and writes a temporary commit on the parent.
2. Runs `git merge-tree --write-tree --name-only --merge-base=<parent>` on the two commits, named `workspace` and `incoming` so conflict messages refer to the sides rather than scratch commit IDs. A conflict is an error naming the conflicted paths and Git's `CONFLICT` messages.
3. Replays, on a COW child of `Before`, the filesystem transitions of the legacy checkout sequence rather than checking out the merged tree:

```text
Before
  + raw working delta,  then checkout: working tree  -> parent
  + raw incoming delta, then checkout: incoming tree -> working tree
  + checkout: working tree -> merged tree
```

`nativeWorkspaceCheckout` removes and `checkout-index`es only entries that differ between two trees. That matters because the legacy result is not a pure function of the Git tree: entries Git skips keep their incoming raw bytes (including CRLF), permissions, ownership and xattrs, while rewritten entries are normalized. Missing parent directories are left for Git to create with its own mode. The baseline is never walked or hashed, and scratch commits, objects and indexes never enter the output snapshot.

Eligibility is checked only on declared paths and the materialized deltas, with fixed reason codes on top of the storage, gitlink and `.gitmodules` gates above:

- `merge-controls-change`: any `.gitattributes` or `.gitignore` change, which could restage unchanged baseline files in the legacy whole-worktree add
- `empty-directory`: an added directory with no files, which Git cannot represent
- `ignored-merge-path`, `noncanonical-merge-base` (`validateNativeWorkspaceBase`): a declared path ignored by the parent's rules, or a baseline file whose clean conversion differs from its index blob
- `unreported-filesystem-change`, `directory-metadata`, `directory-xattrs` (`validateNativeWorkspaceContent`): a delta file the changeset does not declare, or directory mode, ownership or xattrs Git cannot reproduce. Baseline metadata is read through `os.OpenRoot`, and never through an ancestor the delta replaced or introduced.
- `unsafe-write-path`: a checkout path through an existing non-directory

The span `git native workspace merge` records `dagger.git.native_merge.supported`, `dagger.git.native_merge.fallback_reason` and `dagger.git.native_merge.scoped_stage_paths`.

## Fallback Policy

Native paths are optimizations; the legacy path is always correct. `nativeFallback` is the shared policy:

- **Any error falls back**, not only unsupported inputs: unanticipated repository states, missing objects and internal timeouts included. The error is recorded in full, paths included, as the span's fallback reason, and the caller takes the legacy path: the checkout-based `GitCommitChangeset` for commits, the general `__mergeWithChangeset` for workspace reconciliation. A `merge-tree` conflict falls back too, so the legacy merge reports it.
- **The caller's own cancellation is returned**, decided by the caller's `ctx.Err()`. An error that merely wraps a deadline from some internal context still falls back.
- **`ErrNothingToCommit` is returned as-is.** It is the commit's answer, not a failure, and the legacy path would reach it only after a full checkout.
- **Produced snapshots are released first.** A failure or cancellation observed after the child snapshot was committed releases it, with an uncancelled cleanup context, before the error is returned or discarded. This applies to a reconciliation result as well as a commit.
- **Snapshot chains are bounded.** Each native commit is a COW child of its parent's storage, so a long session stacks overlay layers. `checkNativeSnapshotDepth` rejects a child whose overlay mount has more than 64 `lowerdir` entries (`maxNativeSnapshotDepth`); the legacy path starts from a fresh snapshot and resets the chain. Non-overlay snapshotters have no such limit.

## Testing

Unit tests (`go test ./core -count=1`):

- `core/git_commit_test.go`: `TestGitNativeCommitMatchesCheckout` (exact commit SHAs against `git commit`, including attributes, ignore rules, signoff, packed parents and detached SHA-named refs), `TestGitNativeCommitPublicationIsRooted`, `TestGitNativeCommitLargeFile`, `TestGitNativeCommitRejectsGitlinks`, `TestGitNativeCommitObjectMetrics`, `TestGitNativeCommitRefStorageEligibility`, `TestGitNativeCommitStorageEligibility`.
- `core/git_commit_create_test.go`: `TestNativeCommitFallback`, `TestNativeFallbackPolicy`, `TestNativeSnapshotDepthBound`.
- `core/changeset_native_test.go`: `TestNativeWorkspaceMergeMatchesCheckout` (complete filesystem manifests against the legacy checkout sequence under umasks 022 and 000, including attributes, ownership, xattrs, replacements, renames, noops, conflicts and packed storage), `TestNativeWorkspaceMergeFallbacksAndErrors`, `TestNativeWorkspaceMergeBaseEvidence`, `TestNativeWorkspaceDeltaReplacedAncestors`, `TestNativeWorkspaceDeltaMetadataFallback`.

Integration tests run against a from-source engine, e.g. `dagger call engine-dev test --pkg ./core/integration --run 'TestGit/TestGitRefWithCommitNative'`:

- `core/integration/git_commit_test.go` (`TestGit`): `TestGitRefWithCommitNative` compares native and legacy commit objects, follow-up and concurrent SHA-named commits, and asserts from telemetry that native transactions ran without fetching and left source packs untouched. `TestGitRefWithCommitReftable` exercises the public `ref-storage` fallback.
- `core/integration/workspace_commit_test.go` (`TestWorkspace`): `TestWorkspaceWithCommitReconciliationOracle` compares exact commits, parentage, pending paths and consumer filesystem manifests against an identity-wrapped legacy merge on the same receiver, across overlapping edits, conflicts, type changes, attributes, ignored files and rich metadata. `TestWorkspaceWithCommitNativeReconciliationTrace` requires the native merge to run `merge-tree` without fetching or checking out the baseline. `TestWorkspaceScopedCommitPerformance` is a latency harness over a 12,000-file packed repository with three sequential scoped commits. It verifies committed paths, parentage, preserved pending edits and host isolation, and requires native commits and native reconciliation without commit-time fetches or general merges.
