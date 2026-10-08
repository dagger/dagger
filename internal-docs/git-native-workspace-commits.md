# Git-Native Workspace Commits

This document describes how `GitRef.withCommit` and `Workspace.withCommit` record small edits by working directly on Git object databases, rather than rebuilding repositories from filesystems.

An agent workspace commit of a one-line edit used to materialize a complete checkout with `.git`, apply the changeset, stage and commit it, and then check the result out again, re-fetching history the engine already had cached along the way. The Git commit itself was the cheap part. The intrinsic work is to stage the changed blobs, write the affected trees and one commit object, and reuse everything else. The direction here is to do that Git work natively, over cached objects, with each mechanism an optimization over an existing path that remains in place as the fallback.

The source of truth is the code, mainly:

- `core/git_commit_create.go`: native commit construction, eligibility and the fallback policy
- `core/changeset_native.go`: native workspace reconciliation
- `core/git_local_incremental.go`, `core/git_local.go`: incremental source checkout and checkout provenance
- `core/git_commit_remote.go`, `core/git_local_history.go`, `core/git_history_native.go`: remote promotion, lazy history and history joins
- `core/git_host_history.go`, `engine/session/git/git_pack_commit.go`: approved host history donation
- `core/schema/git_commit_create.go`, `core/schema/workspace_commit.go`: the `GitRef.withCommit` and `Workspace.withCommit` resolvers

## Native Commit Construction

Entry points: `GitCommitChangesetNative` and `withNativeCommitIndex` in `core/git_commit_create.go`, called from `gitRefWithCommitDirectory` in `core/schema/git_commit_create.go` before the general three-way reconciliation.

`GitCommitChangesetNativeBase` first proves same-base provenance without evaluating or hashing either directory. The parent must be a local or remote ref resolved to a full SHA-1 and named as a branch or by that SHA, and the changeset's `Before` must be a direct `DirectoryGitTreeLazy` over that same commit, with Git metadata discarded, from a repository with the same **recipe digest** as the parent's. Recipe identity carries authorization scope; equal-looking contents, subdirectories and filtered trees take the general path. A remote parent first acquires owned storage (see [Remote Workspaces and Lazy History](#remote-workspaces-and-lazy-history)).

The transaction then:

1. Creates a COW child of the parent repository's snapshot via `withGitMergeWorkspace`, and separately mounts the existing storage read-only to borrow its objects.
2. Initializes a private bare scratch repository (`--object-format=sha1 --ref-format=files`) with the source's remotes, a private index and an empty sparse worktree. The borrowed object directory is passed as `GIT_ALTERNATE_OBJECT_DIRECTORIES`, with lazy fetching and replace refs disabled.
3. Runs `read-tree` on the parent, then `ls-tree` on only the changed paths and their ancestors, and checks out only the `.gitattributes`/`.gitignore` files on those chains, in one `checkout-index`. Unchanged blobs are never read.
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
- `linked-worktree`, `shallow-history`, `object-alternates`, `partial-repository`: storage the snapshot does not fully own. The one allowed boundary is an owned `shallow` file at the repository's `HistorySource` anchor.
- `object-format`: anything but SHA-1
- `ref-storage`: reftable refs, which loose-ref publication and the replaced config would lose
- `ref-kind`: a parent ref that is neither a branch nor a detached commit
- `gitmodules-change`: a change to a `.gitmodules` file
- `gitlink-change`: a staged path at or inside a gitlink, i.e. a change to a submodule (see below)
- `unsafe-write-path`: a publication path through an inherited non-directory
- `snapshot-depth`: see below

`nativeCommitFallback` reports whether every leaf of a joined or wrapped error is such a code, so helpers can tell an expected ineligibility from a real failure joined to it.

### Gitlinks

Only gitlinks the delta reaches matter. `ls-tree` matches its pathspecs as prefixes, so once it descends into an ancestor of a changed path it lists every child of that directory; entries are classified by exact name, and a gitlink beside an edited file is left alone. A staged path at or inside a gitlink, such as a file replacing it or filling its directory, falls back with `gitlink-change`.

Removing the directory a gitlink is checked out in removes the gitlink, as `git add -A` would on a full checkout: the user deleted the submodule from their workspace, and keeping the gitlink would leave its checkout a pending removal that no commit could record. Changesets report an uninitialized submodule (`update = none`) only as an empty directory entry (`module/`), which is otherwise never staged, so both `stageNativeChanges` and the checkout path's `stageCheckoutChanges` also look up the removed directories, in the same `ls-tree` or one `ls-files` respectively, and remove the gitlinks among them. Removed paths beneath such a gitlink are submodule content, never in the index. The same holds at any depth inside a removed directory, and inside a directory a file replaced, where `git add` deletes the gitlink anyway. `.gitmodules` is left as the changeset has it: rewriting a file the user did not change would leave the commit and the workspace disagreeing, and Git ignores a section without a gitlink.

The workspace merge does not stage such removals: it reconciles the removed directory from the raw deltas, and still falls back when a staged path reaches a gitlink. `validateNativeWorkspaceBase` lists declared paths with `ls-files`, which matches at or under each path but never a sibling, so a gitlink in a directory a file replaced falls back there too. Incremental source checkout of a commit whose delta changes a gitlink is a full checkout (see below).

## Workspace Reconciliation

Entry points: `TryNativeWorkspaceMerge` and `nativeWorkspaceMerge` in `core/changeset_native.go`, called from `changesetMergeForWorkspaceCommit` (the private, persistable `Changeset.__mergeForWorkspaceCommit` field) in `core/schema/workspace_commit.go`.

`Workspace.withCommit` merges the committed changeset into the approved working tree as well as into HEAD, so incoming and unselected edits both survive. When both the uncommitted and the incoming changeset pass `GitCommitChangesetNativeBase` against HEAD, the uncommitted changeset is the working delta as-is. Otherwise, for empty or off-baseline inputs, the working tree is rebuilt by applying the uncommitted changeset to HEAD and diffed against the incoming changeset's `Before`. `__mergeForWorkspaceCommit` then tries the native merge and falls back to the general fail-on-conflict `__mergeWithChangeset`, which restages the whole baseline in temporary Git repositories, for any input the native merge does not support.

The native merge proves the same provenance for both changesets against the working tree's `Before`, then mounts the local repository read-only to borrow its objects. In a private bare scratch repository it:

1. Stages each changeset in its own sparse worktree and private index with `stageNativeChanges`, the staging shared with commit construction, and writes a temporary commit on the parent.
2. Runs `git merge-tree --write-tree --name-only --merge-base=<parent>` on the two commits, named `workspace` and `incoming` so conflict messages refer to the sides rather than scratch commit IDs. A conflict is final (`nativeMergeConflict`): an error naming the conflicted paths and Git's `CONFLICT` messages, with no legacy merge behind it.
3. Replays, on a COW child of `Before`, the filesystem transitions of the legacy checkout sequence rather than checking out the merged tree:

```text
Before
  + raw working delta,  then checkout: working tree  -> parent
  + raw incoming delta, then checkout: incoming tree -> working tree
  + checkout: working tree -> merged tree
```

`nativeWorkspaceCheckout` removes and `checkout-index`es only entries that differ between two trees. That matters because the legacy result is not a pure function of the Git tree: entries Git skips keep their incoming raw bytes (including CRLF), permissions, ownership and xattrs, while rewritten entries are normalized. Missing parent directories are left for Git to create with its own mode. The baseline is never walked or hashed, and scratch commits, objects and indexes never enter the output snapshot.

The merge base is HEAD's real tree, while the legacy merge commits a synthetic base with `git add -A` over `Before`, which leaves out tracked files the ignore rules match. The two agree on every declared path, but not on what else a directory holds, and Git's directory rename detection depends on that. When one side moves every file out of a directory that still holds a tracked ignored file, and the other side adds a file there, the legacy merge reports a `CONFLICT (file location)` for a directory rename that did not happen; the native merge keeps the added file where it was put. That is the intended result.

Eligibility is checked only on declared paths and the materialized deltas, with fixed reason codes on top of the storage, gitlink and `.gitmodules` gates above:

- `merge-controls-change`: any `.gitattributes` or `.gitignore` change, which could restage unchanged baseline files in the legacy whole-worktree add
- `ignored-merge-path`, `noncanonical-merge-base` (`validateNativeWorkspaceBase`): a declared path ignored by the parent's rules, or a baseline file whose clean conversion differs from its index blob
- `unreported-filesystem-change` (`validateNativeWorkspaceContent`): a delta file the changeset does not declare, such as a mode-only change, which the legacy whole-worktree add could stage. Directories are not checked: the delta's directories may have any mode, owner and xattrs, the root's included, whether introduced or changed, and empty added directories need nothing, since Git never sees them. The replay applies the same raw deltas through the same checkout transitions as the legacy merge, so it ends with the same directories (`TestNativeWorkspaceMergeMatchesCheckout`, `TestWorkspaceWithCommitReconciliationOracle`).
- `unsafe-write-path`: a checkout path through an existing non-directory

Every fallback is also a zero-length wcprof io op, `git.native_merge.fallback[<reason>]`, and each git command a merge runs is a `git.<verb>` op under its phase (`stage_<side>`, `merge_tree`, `replay`).

The span `git native workspace merge` records `dagger.git.native_merge.supported`, `dagger.git.native_merge.fallback_reason` and `dagger.git.native_merge.scoped_stage_paths`. Ineligible provenance is recorded too, as `workspace-` or `incoming-` followed by the failed check (`before-not-git-tree`, `before-commit-mismatch`, `before-repository-mismatch`, ...).

The general merge it falls back to (`gitMergeChangesets`) commits the whole base tree as its merge base, since directory rename detection depends on every path, but first tries a scoped base (`initScopedGitRepo`): objects are written only for paths either changeset declares or its diff overlays, removed or replaced subtrees, and control files; every other file is only hashed into the index (`update-index --info-only`, `write-tree --missing-ok`). Any failure other than the merge's own conflict redoes the merge with the full `git add -A` base. The span `scoped git merge base` records `dagger.git.scoped_merge.supported` and `dagger.git.scoped_merge.fallback_reason`.

## Incremental Source Checkout

Entry points: `GitCheckoutBase`, `validateTree` and `provenTree` in `core/git_local.go`; `incrementalTree`, `planIncrementalGitCheckout` and `applyIncrementalGitCheckout` in `core/git_local_incremental.go`; `gitRefWithCommitRepository` in `core/schema/git_commit_create.go`.

`GitRef.withCommit` selects the private, persistable `__withCommitRepository` recipe. It records checkout provenance on the resulting `LocalGitRepository`:

```go
type GitCheckoutBase struct {
    Parent    dagql.ObjectResult[*GitRef]    // exact parent recipe (remote refs pinned to SHA)
    CommitSHA string                         // resolved HEAD of the new commit
    Tree      dagql.ObjectResult[*Directory] // remote parents only: evaluated tree(discardGitDir: true)
}
```

`Parent` and `Tree` are owned DAG dependencies, attached and persisted with the repository, never a mount path or a guess from a dirty worktree. Public `withContents` never infers them from supplied storage.

For a remote parent, `Parent` is the ref re-selected by its resolved SHA, the same recipe source-only trees use, and its `tree(discardGitDir: true)` is evaluated up front and pinned as `Tree`, so reuse never needs another fetch after mirror or cache eviction. `validateTree` checks the producer recipe, not filesystem equality: the tree's call frame must be `tree` with `discardGitDir: true` on a receiver whose recipe digest equals `Parent`'s. `provenTree` applies it on attach, encode and decode. The engine cache may answer `tree()` on a content-equivalent ref (`keepGitDir`, service and known hosts are outside its content digest) with another recipe's result and frame; such a tree is dropped and `Parent` is kept. A remote parent without a proven `Tree` is not eligible and takes the full checkout.

When `LocalGitRef.Tree(discardGitDir: true)` is requested for exactly `CommitSHA`, and both SHAs are full SHA-1s, `incrementalTree`:

1. Plans from Git metadata alone, before evaluating the parent tree, over a raw mount of the storage. The storage must pass the native commit layout gates (owned shallow storage may keep its anchor boundary), and the commit's raw headers (read with `cat-file`, bypassing replace refs and grafts) must name `Parent` as its single parent. `diff-tree` supplies the delta, whose modes are checked for gitlinks, with the mode and blob of each path to write.
2. Takes the parent's canonical tree (the pinned `Tree` for remote parents, `Parent.tree(discardGitDir: true)` otherwise) and creates a COW child of its snapshot. A cold parent (pending lazy computation; a restored stored snapshot is not cold) is materialized only for a top-level checkout (`incrementalParentTree`), and that nested evaluation may only apply its delta to an already materialized grandparent; otherwise it falls back to a full checkout.
3. Initializes a private scratch Git directory and index with the same borrowed objects and clean checkout config as a full source checkout, never the source repository's config, attributes or worktree. The index holds only the added and modified paths, staged from the delta's modes and blobs by one `update-index -z --index-info` over stdin, never the whole child tree. It removes old leaves deepest first and `checkout-index --all`es those paths, below verified directory ancestors; Git creates missing directories with the same modes and umask as a full checkout. Attributes are read from the commit (`--attr-source`), as a full checkout reads them from the index, not from parent-tree `.gitattributes` files that were themselves converted on checkout. A delta of only removals needs no scratch repository.
4. Normalizes the rewritten paths, their ancestors and the root to mtime 1, as a full checkout would, without following symlinks (`normalizeIncrementalGitCheckout`). Untouched inodes are left alone.

Other refs of the same repository never inherit the annotated tip's materialization, and retained `.git` checkouts keep the full path. A cold history, such as a session resumed on a pruned or restarted engine, therefore costs at most one full checkout of the requested commit's parent plus its delta, never a walk through every ancestor; after that, each commit costs its delta.

Unsupported inputs fall back to the full checkout with a fixed reason code: `commit-format`, `repository-layout`, `parent-mismatch`, `unsafe-path`, `gitlink-change` (a gitlink added, removed or changed in the delta, including inside a replaced directory), `checkout-controls` (a changed `.gitattributes` or `.gitmodules`, which could change the checkout of unchanged files) and `cold-parent` (a cold parent while already materializing a cold parent).

Planning reads only the delta (`diff-tree --raw -r`), never either full tree. A full checkout initializes submodules from the gitlinks and `.gitmodules` alone, and the content is pinned by the gitlink SHA, so gitlinks the delta leaves unchanged are already present in the parent's canonical tree.

The span `materialize incremental git checkout` records `dagger.git.checkout.incremental.supported`, `dagger.git.checkout.incremental.fallback` and `dagger.git.checkout.incremental.changed_paths`.

## Remote Workspaces and Lazy History

Entry points: `nativeCommitRepository`, `GitRemoteCommitBase` and `packRemoteCommitBaseDepth` in `core/git_commit_remote.go`; `fullHistory`, `mountHistory` and `HydrateGitRepository` in `core/git_local_history.go`; `mountOwnedShallowHistory`, `mountRefsWithLocalDonor` and `nativeParentHistoryRefs` in `core/git_history_native.go`; the private `__nativeCommitBase` and `__hydrateRepository` resolvers in `core/schema/git_commit_create.go`.

A workspace checked out from a remote ref has no local storage to borrow objects from. `nativeCommitRepository` gives commit construction and reconciliation one by selecting `GitRef.__nativeCommitBase(depth: 1)` on the parent's repository re-pinned to its resolved SHA, so both share one promotion even when the caller named a branch. The field's cache key (`gitRefNativeCommitBaseKey`) is the parent's exact **recipe digest**, not its content digest, which may alias equivalent refs across authentication and service bindings.

`GitRemoteCommitBase` fetches through the existing authenticated mirror path at the requested depth. Holding the per-URL mirror lock and a read-only mount of the mirror snapshot, it runs `pack-objects --revs` for the pinned SHA into a fresh bare repository in a new snapshot, with replace refs and lazy fetching disabled. The mirror is shared by URL across authorization scopes, so its packs are never copied wholesale, its refs are never inherited, and nothing is written back to it. The result holds `HEAD` at the SHA and the recipe's remotes, and no alternates. Ordinary commits request **depth one**: the commit and its tree, with an explicit `shallow` file naming that SHA even when the mirror already holds full history.

The committed repository records the pinned remote ref as `LocalGitRepository.HistorySource`: one exact remote anchor, inherited unchanged by descendant commits (`validateHistorySource` requires it to match the parent's). Like `CheckoutBase`, it is attached, persisted (`historySourceResultID`) and walked as an owned dependency. Storage with a `HistorySource` may be shallow at exactly that anchor (`ownedShallowBoundary`); any other boundary is an error. Private alternate views copy the boundary explicitly (`copyGitShallowBoundary`), because Git never reads an alternate's `shallow` file.

Mounts are split by demand:

- **Raw mounts** (`LocalGitRepository.mount` without refs, validated by `nativeGitDir`) serve native staging, reconciliation, incremental checkout and identity. They never hydrate.
- **History mounts** (`LocalGitRef.mountHistory`) hydrate only when the depth is unbounded, the commit is absent, or a bounded walk reaches the boundary (`gitLogReachesShallowBoundary`). Short logs, bounded retained checkouts and source-only trees stay shallow; `Tree(discardGitDir: true)` and `GitCommit.Tree` mount at depth one.
- **Complete-history consumers**: mounts that request refs (bundle, push, export) and `ResolveShortSHA` always hydrate first.

`fullHistory` selects `HistorySource.__hydrateRepository(directory)`. `HydrateGitRepository` takes the complete authorized closure (`__nativeCommitBase(depth: 0)`, served from the host checkout when it is an [approved donor](#approved-host-history)), merges it with the local storage into a new snapshot, and removes `shallow` only after the complete pack succeeded; neither input nor the mirror changes. The field's key combines the source recipe digest with the directory's structural input digest, so content-equivalent storage never shares owned provenance and repeated demand reuses one hydration. When neither an approved donor nor the remote can supply the closure, hydration fails rather than returning truncated history.

Joined history reads in `mountRefs` (log ranges, merge bases) avoid the remote where the answer is provable locally, in order:

1. `mountOwnedShallowHistory`: an owned shallow child compared with its anchor, or with any of the anchor's descendants already in its store (its direct parent, an earlier workspace commit, the pinned remote ref), is answered from the child's store and exact boundary. The anchor is an ancestor of both sides, so the truncation below it changes neither ranges nor merge bases. The other ref must carry the child's capability (`ownedShallowHistoryCandidate`): a remote ref of the history source's exact repository recipe, or an owned checkout with the same history source. Both commits must be in the store at or above the anchor, checked in the private view on raw parents; siblings, older ancestors and unrelated refs take the paths below.
2. `mountRefsWithLocalDonor`: when a plain local repository among the refs (a **local donor**, typically the host checkout the agent started from, already loaded into the engine) is complete, not partial and not grafted (`donorGitDir`; Git may prune the ancestors a graft hides, which the donor view would still walk), and its raw objects contain every remote ref's commit and every owned boundary, a private alternates view over those stores answers the read. A commit's ancestry is fixed by its SHA, so the donor's closure is what the remote would serve.
3. `nativeParentHistoryRefs`: a complete owned child substitutes its own objects for its exact remote parent.

Candidates are matched by recipe digest (`ownedShallowHistoryCandidate`, `nativeParentHistoryCandidate`), owned ancestry by raw parents in a view without refs or grafts, parentage by raw commit headers (`validateNativeParentHistory`), and donor coverage by raw objects (`donorHasCommit`), all bypassing replace refs and grafts. Substitutions last only for the read: nothing is fetched, no remote-to-local alias is published, and the mirror is not locked. Other reads keep the existing joins; with a remote ref that is `refJoin`, which unshallows the remote while holding its mirror lock. `initRemote` records waits on that lock as wcprof lock waits, so a promotion queued behind another caller's fetch shows the holder in the waits and critpath views.

## Approved Host History

Entry points: `RegisterCapturedHostHistory`, `approvedHostCommitPack`, `importApprovedHostCommitBase` and `importHostCommitPack` in `core/git_host_history.go`, reached from `GitRemoteCommitBase`; `registerCheckpointHostHistory` in `core/schema/workspace_checkpoint.go`; `GitAttachable.PackCommit` in `engine/session/git/git_pack_commit.go`; `ReceiveGitCommitPack` in `engine/engineutil/git_history.go`.

Hydration downloads complete history that the client's checkout usually already holds. Unlike a local donor, which is already loaded into the engine, that checkout is reachable only through the client's session, so it serves history only as an **approved donor**: a checkout from which the owning client captured the anchor commit.

**Registration.** After a clean capture whose base is a remote ref (`CurrentWorkspace().snapshot()` without a bundle), `registerCheckpointHostHistory` records a donor on the owning client's `Query`. It is keyed by the repository's **recipe digest** and the captured anchor SHA, and records the owner client ID and checkout path. Registration does no host IO and packs nothing. The registry holds capabilities, not objects: it is never persisted, and no client route enters the portable recipe, so replay and other clients keep the remote. Dirty or unpushed (bundle-backed) captures never register. Registration never fails the capture: a capture it cannot key (another owner, missing metadata, an unrecognized route, a SHA-256 anchor, which `PackCommit` does not serve) registers nothing. The capture's span records the outcome: `git.history.donor_registered`, or `git.history.donor_skipped` with the reason (and the error, if one caused it), so a donor that never engages is debuggable.

**Demand.** `GitRemoteCommitBase` asks for a donation only at **depth zero**, so capture and ordinary depth-one promotion never touch the host. `capturedHostHistory` matches only a remote ref pinned by SHA, with the registered recipe and anchor, requested by the owning client. The engine then calls `PackCommit` on that client's attachable, if it is still connected.

**PackCommit.** A separate RPC from `PackCheckout`, which sends every branch and tag, so an older client can never read a scoped request as permission to send more. The client:

1. Requires a SHA-1 commit and a complete (non-shallow), SHA-1 donor. There is no depth: the engine only ever asks for the complete closure.
2. Initializes a scratch bare repository that borrows only the donor's object database as an alternate, with lazy fetching and replace objects disabled and inherited `GIT_*` variables stripped. No refs, config, grafts or shallow boundary leave the host.
3. Probes the anchor commit, then packs a non-thin `pack-objects --revs` pack of exactly its closure into scratch, capped at `MaxGitPackBytes`, and streams it. There is no separate preflight walk: the scratch repository is no partial clone and lazy fetching is off, so `pack-objects` itself fails on any missing ancestor, tree or blob instead of writing a partial pack.

The checkout's refs are deliberately not checked against the captured state. The closure of a fixed SHA does not depend on where HEAD, branches or tags point, and the importer verifies hashes and the exact closure inventory, so a local commit, branch switch, pull, rebase or tag fetch between capture and deep demand (the normal case) leaves the donor usable and changes nothing it sends. For the same reason `PackCommit` takes no checkout lock: it only reads the object database into private scratch, so a long pack or transfer never starves `CheckoutState`, `CaptureGit` or `PackCheckout` on the same checkout. Its metadata carries no state digest.

A missing checkout or anchor commit, a shallow donor or another object format reports `HISTORY_UNAVAILABLE`, and a failed pack (including missing or corrupt objects in the closure, and the size cap) `PACK_FAILED`. The engine treats all of them as a miss.

**Import.** `ReceiveGitCommitPack` spools the stream and requires metadata matching the requested commit. `importHostCommitPack` builds a fresh bare repository (`--ref-format=files`) in a new snapshot and:

1. Runs `index-pack --stdin`, which verifies the pack and object hashes. Objects are not fsck-checked, matching the remote path, which does not fsck fetched packs: the closure is pinned by SHA and so byte-identical to what the remote serves, legacy objects such as zero-padded tree modes included. The closure walk below still parses every commit and tree.
2. Requires the store to equal the commit's closure exactly, nothing outside it and nothing missing, without buffering either object list: a full `rev-list --objects --missing=error` walk, whose output is only counted, proves the closure is stored, and the closure's object count must equal the pack index's entry count (duplicates included). Git treats the empty tree as present whether or not it is stored, so a closure holding it also requires it in the pack index.
3. Writes remotes from the recipe, never from donor config.

The result is owned storage like any promotion: losing the donor or the remote afterwards loses nothing.

**Fallback.** Donation is only an optimization. A disconnected owner skips it. Any other donor failure (any `PackCommit` error, the host's pack timeout, a lost transport, a protocol violation, or a pack the importer rejects) is recorded as `git.history.donor_fallback` on the span, and the authorized remote path runs instead. A rejected import is released uncommitted first, so nothing from it is kept. Only the caller's own cancellation, decided by `ctx.Err()` because the host's timeout also surfaces as `DeadlineExceeded`, and failed engine-side cleanup are errors.

The spans `git request approved host commit closure` and `git import approved host commit closure` record `git.history.depth`.

## Upstream Name Resolution

Entry points: `LocalGitRepository.Upstream`, `GitUpstream` and `HasCommit` in `core/git_local.go`; `withContents`, `ref`, `revision`, `commit`, `branches`/`tags`, `upstreamRef` and `commitSHARef` in `core/schema/git.go`; `gitRefWithCommitRepository` in `core/schema/git_commit_create.go`.

Owned storage holds only the history it was built from. A remote ref resolves names with `ls-remote` and fetches lazily with the repository's own authentication; owned storage lists only its own refs (`ls-remote file://<gitdir>`). Without a link back, anything that turned a remote repository into owned storage lost the ability to resolve its other branches and tags: a dirty or unpushed `CurrentWorkspace().snapshot()` (its `withBundle` ends in `withContents`), and any commit on a remote-backed workspace. Module code cannot recover the capability by calling `git(url)`: implicit host credentials are only offered to the non-module caller, so a private remote is unreadable from an agent tool even though the snapshot's own recipe authenticated it.

`LocalGitRepository.Upstream` retains that remote. `withContents` sets it from its receiver (`GitUpstream`: a remote receiver itself, or owned storage's own `Upstream`), and `gitRefWithCommitRepository` carries it to the committed repository, so it survives every descendant commit, `asRepository` and `withRemote`. It is only ever a remote repository (`validateUpstream`), attached, persisted (`upstreamResultID`) and walked as an owned dependency like `HistorySource`.

`GitRepository.ref` on owned storage resolves locally first: `HEAD`, local names, local full SHAs and local abbreviated SHAs never touch the upstream. Only a `RefNotFoundError` (or an abbreviated SHA that matches no local commit; an ambiguous one is final) falls through to `upstreamRef`, which selects `ref` on the upstream with the caller's name, pinned commit and lock request. The result is the remote's own `GitRef`, so reading it fetches through the ordinary authenticated mirror path; nothing is written into the owned storage. Local names shadow remote ones, as a local branch shadows its upstream in Git.

A full SHA the raw storage does not contain (`HasCommit`, which never hydrates) resolves through the upstream too (`commitSHARef`, and `commit` for `GitCommit`), so a SHA read from an upstream-resolved ref can be passed back in. A revision whose base resolved through the upstream (`side~2`) is handed to the upstream whole, where the walk fetches the history it needs; owned storage never has it. `branches` and `tags` list the upstream's names (with the same patterns) alongside the local ones, so the listings match what `ref` accepts; an unreachable upstream fails the listing rather than silently truncating it.

`Upstream` is deliberately separate from `HistorySource`. `HistorySource` marks owned *shallow* storage anchored at a remote commit and excludes the storage from serving as a local donor; a captured bundle is complete, and must remain a donor. `Upstream` grants nothing new: it is the exact repository, with the exact authentication, that the receiver already held, and it is never inferred from supplied storage.

## Fallback Policy

Native paths are optimizations over complete legacy paths, which handle every input they reject. Where the two disagree, the native path is the reference: it works from the real Git objects, while the legacy paths approximate them from a checkout (see the merge base above). `nativeFallback` is the shared policy:

- **Any error falls back**, not only unsupported inputs: unanticipated repository states, missing objects and internal timeouts included. The error is recorded in full, paths included, as the span's fallback reason, and the caller takes the legacy path: the checkout-based `GitCommitChangeset` for commits, the general `__mergeWithChangeset` for workspace reconciliation, a full checkout for incremental source checkout. Git errors quote at most the first pathspec and 512 bytes of the command line (`gitErrorArgs`).
- **The caller's own cancellation is returned**, decided by the caller's `ctx.Err()`. An error that merely wraps a deadline from some internal context still falls back.
- **Results are returned as-is.** `ErrNothingToCommit` is the commit's answer and a `nativeMergeConflict` the merge's, not failures; the legacy path would reach them only after restaging everything.
- **Produced snapshots are released first.** A failure or cancellation observed after the child snapshot was committed releases it, with an uncancelled cleanup context, before the error is returned or discarded. This applies to reconciliation and incremental checkout results as well as a commit.
- **Snapshot chains are bounded.** Each native commit is a COW child of its parent's storage, and each incremental checkout a COW child of its parent's tree, so a long session stacks overlay layers. `checkNativeSnapshotDepth` rejects a child whose overlay mount has more than 64 `lowerdir` entries (`maxNativeSnapshotDepth`); the legacy path starts from a fresh snapshot and resets the chain. Non-overlay snapshotters have no such limit.
- **Remote inputs follow the same policy.** A failed promotion sends the commit or reconciliation to the legacy path. A native commit on a parent older than the owned shallow boundary fails inside the transaction and falls back to the checkout path, which hydrates. Joined reads that no owned boundary or local donor covers keep the existing join.
- **Unprovable checkout provenance is dropped, not fatal.** A parent `Tree` that `validateTree` rejects is dropped while `Parent` is kept, so a remote parent takes the full checkout.
- **Approved donors fall back to the remote.** Any donor failure, including a pack the importer rejects, runs the authorized remote path instead (see [Approved Host History](#approved-host-history)).
- **Hydration has no further fallback.** Once history beyond the boundary is demanded and no approved donor supplies it, an unavailable remote is an error; history is never silently truncated.

## Testing

Unit tests (`go test ./core ./core/schema ./engine/session/git ./engine/engineutil -count=1`):

- `core/git_commit_test.go`: `TestGitNativeCommitMatchesCheckout` (exact commit SHAs against `git commit`, including attributes, ignore rules, signoff, packed parents and detached SHA-named refs), `TestGitNativeCommitPublicationIsRooted`, `TestGitNativeCommitLargeFile`, `TestGitNativeCommitRejectsGitlinks`, `TestGitNativeCommitSiblingGitlink` (an edit beside a gitlink commits natively, a change at or inside it falls back), `TestGitNativeCommitGitlinkDirectories` (removed or replaced submodule directories, alone or inside removed and replaced directories, remove their gitlinks, with the same SHA from `stageCheckoutChanges`), `TestGitNativeCommitObjectMetrics`, `TestGitNativeCommitRefStorageEligibility`, `TestGitNativeCommitStorageEligibility`, `TestGitNativeCommitBeyondOwnedShallowBoundaryFallsBack`.
- `core/git_commit_create_test.go`: `TestNativeCommitFallback`, `TestNativeFallbackPolicy`, `TestNativeSnapshotDepthBound`.
- `core/changeset_native_test.go`: `TestNativeWorkspaceMergeMatchesCheckout` (complete filesystem manifests against the legacy checkout sequence under umasks 022 and 000, including attributes, ownership, xattrs, replacements, renames, noops, conflicts and packed storage), `TestNativeWorkspaceMergeFallbacksAndErrors`, `TestNativeWorkspaceMergeBaseEvidence`, `TestNativeWorkspaceMergeGitlinks`, `TestNativeWorkspaceDeltaReplacedAncestors`, `TestNativeWorkspaceDeltaMetadataFallback`.
- `core/git_local_incremental_test.go`: `TestIncrementalGitCheckout` (complete source manifests against a fresh full checkout under umasks 022 and 000, including attributes, type replacements, symlinks, executable bits, option-like and non-ASCII path names, a delta of hundreds of paths, removal-only and same-tree commits, untouched inodes and pack mtimes), `TestIncrementalGitCheckoutGates`, `TestIncrementalGitCheckoutGitlinks` (unchanged submodules against a full checkout, and gitlink changes in the delta), `TestIncrementalGitCheckoutConvertedAttributes`, `TestIncrementalGitCheckoutActualParent` (grafts and replace refs), `TestIncrementalGitCheckoutProvenance`, `TestIncrementalGitCheckoutColdChain` (a cold 100-commit chain evaluates two trees with one full checkout, through the real cache's lazy evaluation). `core/git_persistence_test.go`: `TestGitCheckoutBasePersistence` (dependency retention across cache restarts, malformed payloads), `TestRemoteGitCheckoutBasePersistence` (pinned tree and `HistorySource` ownership across restarts without a mirror, bad sources, unproven trees dropped), `TestGitCheckoutBaseContentEquivalentParentTree`.
- `core/git_commit_remote_test.go`: `TestRemoteCommitBaseProvenance` (authorization scope, ref kinds, fallback and cancellation), `TestOwnedShallowPromotion` (exact depth-one inventory, commits on the anchor, boundary mismatches), `TestRemoteCommitBaseIsolation` (no unrelated objects, refs or alternates under concurrent promotion), `TestRemoteCommitBaseFallbackAndErrors`. `core/schema/git_lazy_test.go`: `TestNativeCommitBaseCacheScope` (promotion and hydration keyed by exact recipes, not content aliases).
- `core/git_history_native_test.go`: `TestNativeParentHistoryProvenance`, `TestNativeParentHistoryHeaders` (grafts and replace refs), `TestDonorHistoryJoin` (remote refs and owned boundaries covered by a local donor; uncovered, shallow and mismatched inputs left to the existing join).
- `core/git_host_history_test.go`: `TestCapturedHostHistoryScope` (recipe, owner, anchor and pinned-ref scoping; no request at depth one), `TestCapturedHostHistorySharedWithClones`, `TestImportHostCommitPack` (out-of-closure, duplicate, incomplete, empty-tree substitution, corrupt, truncated and missing packs rejected; an accepted store holds exactly the closure), `TestImportHostCommitPackRejectsMalformedTree`, `TestImportHostCommitPackAcceptsLegacyHistory`, `TestApprovedHostCommitBaseFallback` (rejected imports released uncommitted and fall back; cancellation returned). `core/schema/workspace_test.go`: `TestCheckpointHostHistorySkipsBundles`, `TestCheckpointHostHistorySkipsSHA256` (no donor, no failure, and the recorded skip reason). `engine/session/git/git_pack_commit_test.go`: `TestPackCommitExactClosure` (complete closure; no unrelated objects, replacements or refs), `TestPackCommitMovedCheckout` (refs moved after capture still donate only the anchor's closure), `TestPackCommitStreamsUnlocked`, `TestPackCommitUnavailable` (missing, unknown-commit, shallow, partial and missing-ancestor donors; incomplete or corrupt closures fail the pack without streaming any of it). `engine/engineutil/git_history_test.go`: `TestReceiveGitCommitPack` (every donor and protocol failure is a miss; only the caller's cancellation or deadline is returned).

Integration tests run against a from-source engine, e.g. `dagger call engine-dev test --pkg ./core/integration --run 'TestGit/TestGitRefWithCommitNative'`:

- `core/integration/git_commit_test.go` (`TestGit`): `TestGitRefWithCommitNative` compares native and legacy commit objects, follow-up and concurrent SHA-named commits, and asserts from telemetry that native transactions ran without fetching and left source packs untouched. `TestGitRefWithCommitReftable` exercises the public `ref-storage` fallback. `TestGitRefIncrementalCheckoutOracle` compares committed trees against unannotated full-checkout oracles, including attributes, dirty inputs and path replacements; `TestGitRefIncrementalCheckoutTrace` requires a warmed single-path commit to check out only its delta; `TestGitRefNativeCommitHistory` runs 56 sequential and concurrent commits with fsck, ancestry and checkout equivalence, and allows at most one full parent checkout; `TestGitRefRetainedCheckoutSurvivesSourceScope` covers retained `.git` checkouts.
- `core/integration/workspace_commit_test.go` (`TestWorkspace`): `TestWorkspaceWithCommitReconciliationOracle` compares exact commits, parentage, pending paths and consumer filesystem manifests against an identity-wrapped legacy merge on the same receiver, across overlapping edits, conflicts, type changes, attributes, ignored files and rich metadata. `TestWorkspaceWithCommitNativeReconciliationTrace` requires the native merge to run `merge-tree` without fetching or checking out the baseline. `TestWorkspaceScopedCommitPerformance` is a latency harness over a 12,000-file packed repository with three sequential scoped commits. It verifies committed paths, parentage, preserved pending edits and host isolation, and requires native commits, native reconciliation and incremental source checkouts without commit-time fetches, general merges or full source checkouts. `TestWorkspaceRemoteFirstNativeCommit` runs the reconciliation oracle on a remote-backed workspace and requires one shared promotion, a native `commit-tree`, preserved push routing and incremental reuse of the remote parent tree.
- `core/integration/workspace_remote_history_test.go` (`TestWorkspace`): `TestWorkspaceRemoteParentHistoryDoesNotFetch` (ahead/behind against the remote parent with its service stopped), `TestWorkspaceRemoteLazyHistoryOrdinary` (commits, source, status, short logs and parent ranges without a full fetch), `TestWorkspaceRemoteLazyHistoryDemand` (deep logs, old paths and refs, older and divergent ranges, retained checkouts and bundles hydrate inside the demanding call and match a complete Git oracle; one hydration is reused offline), `TestWorkspaceRemoteLazyHistoryUnavailable` (descendants keep working offline; deep history fails explicitly). `TestWorkspaceApprovedHostHistoryOrdinary`, `TestWorkspaceApprovedHostHistoryOffline`, `TestWorkspaceApprovedHostHistoryMovedCheckout` and `TestWorkspaceApprovedHostHistoryFallback` capture a real host checkout against a smart-HTTP origin: ordinary commits never request a donation, deep history is imported once from the donor with the origin closed and survives removing the donor, the import holds exactly the commit's closure, a donor whose HEAD, branches and tags moved after capture still donates only that closure, and missing, replaced and shallow donors fall back to the remote.
