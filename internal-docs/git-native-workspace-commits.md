# Git-Native Workspace Commits

This document describes how `GitRef.withCommit` and `Workspace.withCommit` record small edits by working directly on Git object databases, rather than rebuilding repositories from filesystems.

An agent workspace commit of a one-line edit used to materialize a complete checkout with `.git`, apply the changeset, stage and commit it, and then check the result out again, re-fetching history the engine already had cached along the way. The Git commit itself was the cheap part. The intrinsic work is to stage the changed blobs, write the affected trees and one commit object, and reuse everything else. The direction here is to do that Git work natively, over cached objects, with each mechanism an optimization over an existing path that remains in place as the fallback.

The source of truth is the code, mainly:

- `core/git_commit_create.go`: native commit construction, eligibility and the fallback policy
- `core/schema/git_commit_create.go`: the `GitRef.withCommit` resolvers

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

Unsupported inputs are rejected with a fixed `nativeCommitUnsupportedReason` code, never a path or ref:

- `git-directory-layout`, `object-directory-layout`: `.git` is a gitfile or symlink, or `objects` is not a directory
- `linked-worktree`, `shallow-history`, `object-alternates`, `partial-repository`: storage the snapshot does not fully own
- `object-format`: anything but SHA-1
- `ref-storage`: reftable refs, which loose-ref publication and the replaced config would lose
- `ref-kind`: a parent ref that is neither a branch nor a detached commit
- `gitmodules-change`, `gitlink-change`: changes touching `.gitmodules` or gitlinks
- `unsafe-write-path`: a publication path through an inherited non-directory
- `snapshot-depth`: see below

`nativeCommitFallback` reports whether every leaf of a joined or wrapped error is such a code, so helpers can tell an expected ineligibility from a real failure joined to it.

## Fallback Policy

Native paths are optimizations; the legacy path is always correct. `nativeFallback` is the shared policy:

- **Any error falls back**, not only unsupported inputs: unanticipated repository states, missing objects and internal timeouts included. The error is recorded as the span's fallback reason, and the caller takes the legacy path.
- **The caller's own cancellation is returned**, decided by the caller's `ctx.Err()`. An error that merely wraps a deadline from some internal context still falls back.
- **`ErrNothingToCommit` is returned as-is.** It is the commit's answer, not a failure, and the legacy path would reach it only after a full checkout.
- **Produced snapshots are released first.** A failure or cancellation observed after the child snapshot was committed releases it, with an uncancelled cleanup context, before the error is returned or discarded.
- **Snapshot chains are bounded.** Each native commit is a COW child of its parent's storage, so a long session stacks overlay layers. `checkNativeSnapshotDepth` rejects a child whose overlay mount has more than 64 `lowerdir` entries (`maxNativeSnapshotDepth`); the legacy path starts from a fresh snapshot and resets the chain. Non-overlay snapshotters have no such limit.

## Testing

Unit tests (`go test ./core -count=1`):

- `core/git_commit_test.go`: `TestGitNativeCommitMatchesCheckout` (exact commit SHAs against `git commit`, including attributes, ignore rules, signoff, packed parents and detached SHA-named refs), `TestGitNativeCommitPublicationIsRooted`, `TestGitNativeCommitLargeFile`, `TestGitNativeCommitRejectsGitlinks`, `TestGitNativeCommitObjectMetrics`, `TestGitNativeCommitRefStorageEligibility`, `TestGitNativeCommitStorageEligibility`.
- `core/git_commit_create_test.go`: `TestNativeCommitFallback`, `TestNativeFallbackPolicy`, `TestNativeSnapshotDepthBound`.

Integration tests run against a from-source engine, e.g. `dagger call engine-dev test --pkg ./core/integration --run 'TestGit/TestGitRefWithCommitNative'`:

- `core/integration/git_commit_test.go` (`TestGit`): `TestGitRefWithCommitNative` compares native and legacy commit objects, follow-up and concurrent SHA-named commits, and asserts from telemetry that native transactions ran without fetching and left source packs untouched. `TestGitRefWithCommitReftable` exercises the public `ref-storage` fallback.
- `core/integration/workspace_commit_test.go` (`TestWorkspace`): `TestWorkspaceScopedCommitPerformance` is a latency harness over a 12,000-file packed repository with three sequential scoped commits. It verifies committed paths, parentage, preserved pending edits and host isolation, and requires native commits without commit-time fetches.
