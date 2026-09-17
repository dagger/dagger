# Workspace Git: snapshots, commits, pulls and pushes

Design for independent agent sessions that produce Git commits and pending
edits, which users can integrate into a shared checkout. Related implementation
details: [canonical host Git reconstruction](host-git-reconstruction.md) and
[workspace save performance](workspace-save-performance.md).

## Table of Contents

- [Problem](#problem)
- [Solution](#solution)
- [Core Concept](#core-concept)
  - [Snapshot](#snapshot)
  - [Commit and reset](#commit-and-reset)
  - [Compare](#compare)
  - [Pull](#pull)
  - [Push and remotes](#push-and-remotes)
  - [Save](#save)
- [CLI](#cli)
- [Agent branch sync (proposed)](#agent-branch-sync-proposed)
- [Known gaps](#known-gaps)
- [Status](#status)

## Problem

1. **Independent work** — Multiple agents need to keep working while the user
   chooses which contributions to integrate into a checkout.
2. **Resumable sessions** — A live host checkout alone cannot reconstruct the
   workspace an agent was using when its conversation was saved.
3. **Incremental delivery** — Each save must account for work already delivered,
   even when other agents or the user have advanced the destination history.
4. **Visible changes** — Users need to review both commits and pending edits
   before saving an agent's contribution.
5. **Checkout-independent Git** — Engine-owned workspaces need ordinary Git
   commit, comparison, integration, and push operations.

## Solution

A Git-backed workspace is a **`GitRef` plus an uncommitted overlay**. Snapshot
captures the host state into that model when possible, improving the session's
ability to resume. Commit and pull produce ordinary Git
history in engine-owned storage. Compare uses Git ancestry; push sends a ref
directly from the engine. Bundles transport objects between repositories.

Save integrates the source into a captured destination: fast-forward when
possible, otherwise cherry-pick source commits onto the destination history.
The client then applies a checked fast-forward and writes the merged pending
changes. The source workspace and existing destination commits keep their
identities; cherry-picked copies can have new hashes.

Agent sessions act as independent factory lines. Saving one session delivers
its work while every session continues from its own workspace. The user can
save contributions from several agents, or select commits through
`compareCommitsFrom` and `withCommitsFrom`. Saving does not require agents to adopt the
checkout's combined history or restart their work.

Each session tracks its last saved **source Workspace**, including pending
edits. That value is the comparison point for its next delivery. For example,
after saving agent A's first contribution, then agent B's, A's next save
contributes only work since A's previous saved value. The destination may have
cherry-picked copies of A's commits with different hashes. The saved source
value still identifies exactly what A has already delivered.

## Core Concept

The GraphQL excerpts below show the relevant public fields. Workspace mutations
and the new Git types and fields use the unreleased v1 schema view
(`AfterVersion("v1.0.0-0")`); see [version gating](../../internal-docs/version-gating.md).

### Snapshot

```graphql
extend type Workspace {
  """
  Best-effort capture to make sessions resumable. Return a stable value when
  Git capture is available; otherwise preserve the original workspace.
  Local capture requires the owning client. Existing stable values retain
  their baseline; remote refs are pinned to their resolved commits.
  """
  snapshot: Workspace! @experimental(reason: "Best-effort capture for resumable sessions; capture and fallback behavior may change.")
}
```

`snapshot` is an experimental, best-effort enhancement for session resumability.
It is explicit and effectful (`DoNotCache`). Returning or syncing a
workspace does not implicitly capture it. Reads, edits, and module loading must
use the returned value to share its baseline. To adopt later host changes,
snapshot `currentWorkspace` again.

For a local repository with commits, the client's own Git captures history and
worktree content through `CaptureGit`. Let `R` be the selected remote-served
ancestor, `L` the local HEAD, and `S` a temporary commit with parent `L` and the
approved worktree as its tree. The returned value is composed approximately as:

```text
repo = git(url: <credential-free remote>)
  .withBundle(bundle: blob(<bytes>).asGitBundle, prerequisiteRef: <hint for R>)

repo.ref(name: L).asWorkspace(cwd: <cwd>)
  .withChanges(repo.ref(S).tree.changes(from: repo.ref(L).tree))
```

The v3 bundle carries `refs/dagger/checkpoint/head -> L` and, when dirty,
`refs/dagger/checkpoint/worktree -> S`, with objects beyond prerequisite `R`.
`S` supplies the overlay; workspace HEAD remains `L`, so this synthetic commit
does not become a user commit in logs or pushes. The recipe records captured
bytes and pinned refs, not an instruction to capture the host again. Workspace
cwd, selected config and lockfile paths, and environment are preserved.

Tracked changes are captured automatically. Untracked files require an
interactive **Include / Drop / Cancel** decision. Include binds approval to
per-path state tokens; changed bytes require review again. Drop omits all
untracked files while retaining tracked edits and leaving the checkout alone.
Untracked nested repositories are reported as boundaries and excluded even
when Include is chosen. A noninteractive call that needs approval fails.

Capture approval is designed for interactive sessions. Noninteractive control
over untracked-file selection is outside the current scope; the public API
has no include/exclude or size-limit arguments. Capture enforces internal bounds: the snapshot
resolver allows 256 MiB total and uses service defaults of 16 MiB per untracked
file, 64 MiB of untracked content, and 4096 untracked files. Committed blobs are
subject to capture bounds but exempt from worktree secret heuristics.

**Portability and fallback.** A usable remote base gives a recipe that can be
reconstructed without the originating checkout, subject to remote access and
base availability. Without one, capture uses the session-scoped `host.__gitDir`
reconstruction. This still freezes the Git workspace for the session.

If there is no repository, no HEAD commit, or no client Git-capture support,
`snapshot` returns the original workspace unchanged so the session can still
run. That session may remain dependent on its live host and may not be resumable
without it. This is an intentional fallback and a [known gap](#known-gaps).
Approval rejection and capture errors remain errors. Git mutations require
successful freezing and do not silently use this fallback.

Implementation: [snapshot composition](../../core/schema/workspace_checkpoint.go)
and [client capture](../../engine/session/git/git_capture.go).

### Commit and reset

```graphql
extend type Workspace {
  """Create a real Git commit, advance HEAD, and retain unselected edits."""
  withCommit(
    """Delta to merge into both HEAD and the working tree; fail on conflicts or no-op."""
    changes: ID! @expectedType(name: "Changeset")
    message: String!
    """Explicit RFC3339 author and committer date."""
    date: String!
    """Defaults from the calling client's Git config at commit time, else Dagger."""
    authorName: String
    """Defaults from the calling client's Git config, else dagger@localhost."""
    authorEmail: String
    signoff: Boolean = false
  ): Workspace!

  """
  Move HEAD to a full commit hash. Retain the previous working tree as pending
  changes by default; hard discards pending changes and uses the target tree.
  """
  withReset(commit: String!, hard: Boolean = false): Workspace!
}
```

Both operations freeze local receivers first and leave the host untouched.
Commit resolves missing identity fields before recording the pure operation,
so replay does not read Git config or the clock. `signoff` adds the author's
`Signed-off-by` trailer. Reset retains reachable history only: an orphaned
commit cannot be recovered by resetting the returned value forward again.

Workspace commit delegates to the composable `GitRef.withCommit` API:

```graphql
extend type GitRef {
  """
  Three-way merge the changeset against this ref's tree and create a
  single-parent commit. Fail on conflicts; preserve compatible parent edits.
  """
  withCommit(
    changes: ID! @expectedType(name: "Changeset")
    message: String!
    date: String!
    authorName: String!
    authorEmail: String!
    committerName: String
    committerEmail: String
    committerDate: String
    allowEmpty: Boolean = false
    signoff: Boolean = false
  ): GitRef!
}
```

Committer fields default to the author fields and date. This lower-level API
has no ambient client-config dependency. Workspace commit filters its changeset,
calls this primitive, and composes the remaining overlay on the returned ref.
Ordinary Git commits carry the history and identity used by comparison and pull.

For tools that need a checkout, `Workspace.git.directory` returns a
self-contained **Git metadata directory** with full reachable history and an
index matching HEAD. Mount it at `.git` alongside `workspace.directory("/")`.
The overlay stays uncommitted; the original host staging split is not retained.
Git writes to that mounted copy do not update the workspace automatically.
`GitRepository.withDirectory(directory:)` can adopt a resulting repository
while retaining the receiver's logical URL and remote routing.

Implementation: [workspace commit](../../core/schema/workspace_commit.go),
[GitRef commit](../../core/schema/git_commit_create.go), and
[workspace reset](../../core/schema/workspace_reset.go).

### Compare

Existing `GitRef.log(base:)` and `commonAncestor(other:)` compare refs across
repositories. With `baseline` an earlier snapshot:

| Question | Query |
| --- | --- |
| Commits added since baseline | `ws.git.head.log(base: baseline.git.head, limit: 100)` |
| Baseline commits absent from current history | `baseline.git.head.log(base: ws.git.head, limit: 100)` |
| Fork point | `ws.git.head.commonAncestor(other: baseline.git.head)` |
| Pending edits above HEAD | `ws.git.uncommitted` |
| File changes since an earlier value | `ws.changes(from: baseline)` |

`log` defaults to 10 entries and requires a positive limit; `0` does not mean
unlimited. `Workspace.changes(from:)` returns cwd-relative paths. Omitting
`from` retains the cumulative behavior.

### Pull

```graphql
extend type Workspace {
  """Preview integration oldest first, accounting for earlier applicable picks."""
  compareCommitsFrom(
    source: ID! @expectedType(name: "Workspace")
    commits: [String!] = []
    maxCommits: Int = 100
  ): [WorkspaceCommitPick!]!

  """Integrate selected source commits, preserving this workspace's pending edits."""
  withCommitsFrom(
    source: ID! @expectedType(name: "Workspace")
    commits: [String!] = []
    maxCommits: Int = 100
  ): Workspace!
}

type WorkspaceCommitPick implements Node {
  id: ID!
  commit: GitCommit!
  status: WorkspaceCommitPickStatus!
  reason: WorkspaceCommitPickReason!
  """Workspace-root-relative paths; empty unless conflicting."""
  conflictPaths: [String!]!
}

enum WorkspaceCommitPickStatus {
  PICKABLE
  PICKED
  REDUNDANT
  CONFLICT
}

enum WorkspaceCommitPickReason {
  NONE
  CONTENT
  DIRTY
}
```

The source must already be a frozen Git-backed workspace. A local receiver is
captured automatically. Source pending edits are ignored; receiver pending
edits and workspace metadata survive integration.

When the selected commits include every new ancestor of their tip and the
receiver can fast-forward, hashes are preserved. Otherwise commits are
cherry-picked oldest first. Hashes and cherry-pick origin trailers identify
`PICKED` commits; matching patches or empty applications are `REDUNDANT`.
Conflicts distinguish committed content (`CONTENT`) from overlapping receiver
edits (`DIRTY`). Any conflict fails `withCommitsFrom`. Divergent merge commits
require manual integration.

`commits` accepts full hashes in any order, restricted to the source's latest
10,000 commits. `maxCommits` ranges from 1 to 1000 and bounds either differing
history. Exceeding it fails instead of producing a partial plan.

Cherry-picks preserve source authors and author dates, use the calling client's
Git config for committer identity, reuse source committer dates, and record
origin trailers. Resolved inputs and scratch-repository operations reconstruct
the result without recapturing either checkout.

Implementation: [pull resolvers](../../core/schema/workspace_pull.go) and
[Git integration](../../core/workspace_pull.go).

### Push and remotes

```graphql
extend type GitRepository {
  """Register routing metadata, also installed in materialized checkouts."""
  withRemote(name: String!, url: String!, pushUrl: String = ""): GitRepository!
}

extend type GitRef {
  """Push engine-side. Every invocation pushes; reading the receipt does not."""
  push(
    """Explicit destination repository; mutually exclusive with remote."""
    to: ID @expectedType(name: "GitRepository")
    """Registered remote name. Empty selects origin routing."""
    remote: String = ""
    """Branch name or fully qualified ref. Required for detached/non-branch refs."""
    branch: String = ""
    """Full lowercase object ID for a lease. Empty means ordinary non-force push."""
    expectedRemoteSHA: String = ""
  ): GitPushResult!
}

type GitPushResult implements Node {
  id: ID!
  ref: String!
  """Previous remote object ID; empty for a newly created ref."""
  previousSHA: String!
  sha: String!
  disposition: GitPushDisposition!
}

enum GitPushDisposition {
  CREATED
  FAST_FORWARD
  FORCED
  UP_TO_DATE
}
```

A workspace pushes through `ws.git.head.push(branch: "feature/x")`. Without
`to`, routing uses the selected remote's push URL, then its fetch URL; default
origin routing can fall back to the repository's logical URL. Captured push
routing survives snapshots and subsequent repository edits. A checkout with
several `pushurl` entries contributes only the first.

An empty or omitted `expectedRemoteSHA` uses ordinary non-force rules, including
creation of a missing ref. A supplied SHA permits replacement only under that
lease, checked even for an up-to-date push. There is no empty-string
"must not exist" lease.

Remote metadata is separate from authorization. Delegated pushes are approved
through the owning client's session before destination access or credential
use. Push reuses destination authentication and operation-local owner
credentials, including SSH-agent preparation when needed. The engine does not
run checkout hooks or modify the host checkout. The returned receipt is
replayable without repeating the push.

Implementation: [push routing](../../core/schema/git_push.go),
[push execution](../../core/git_push.go), and
[Git schema](../../core/schema/git.go).

### Save

```graphql
extend type Workspace {
  """Write commits and pending edits to a checkout on the calling client."""
  export(
    """Destination checkout; relative paths start at the calling client's cwd."""
    path: String = ""
    """Previously exported frozen source, for an incremental save with path."""
    from: ID @expectedType(name: "Workspace")
  ): Void!
}
```

With `path`, the source must be frozen. The destination is explicitly selected
on the **calling client**; a frozen value carries no bound export destination.
Save captures destination Git state, retaining tracked dirt while leaving
unrelated host untracked files out of capture.

The engine then integrates source commits using the pull machinery and merges
pending changes. Divergence is acceptable when the commits apply cleanly.
Destination Git config supplies the committer identity for any cherry-picked
copies. `from` limits the contribution to work since the previous exported
source, including previously saved pending edits that have since been committed.
The comparator records delivery from this agent even when the user has since
saved other agents' work into the same checkout.
Passing the same source and comparator is a no-op. The source itself is unchanged.
Source and destination must have related histories. For an incremental save,
`from` must be an ancestor of the source HEAD. Rewriting history past that
baseline currently makes incremental export fail; see [known gaps](#known-gaps).

The result travels in a bundle containing a synthetic transport commit. Its
parents retain the integrated target history and the captured before state;
its tree contains the merged worktree. `ApplyBundle` imports the objects, checks
the captured ref state, and updates HEAD under a ref transaction. The synthetic
transport commit does not become the checkout's HEAD.

The writer preserves unrelated staged and unstaged edits and refuses conflicting
content or untracked obstructions. It checks file kinds and modes, including on
retry. Once imported, commits are retained on
`refs/dagger/checkpoints/<short-sha>` if the checked host update fails. An
engine-side integration conflict can fail before import, so that failure does
not promise a recovery ref in the checkout.

Without `path`, export retains the local-workspace overlay behavior: write at
its host root on the calling client. `from` can select the delta from an earlier
local workspace value. Export paths remain workspace-root-relative even when
the workspace cwd is a subdirectory. Frozen workspaces require an explicit path.

Implementation: [export resolver](../../core/schema/workspace_export.go),
[save composition](../../core/workspace_export.go), and
[checked client update](../../engine/session/git/git_apply.go).

## CLI

`dagger agent` materializes `currentWorkspace.snapshot` once before binding the
workspace or composing agent tools. Initial capture and explicit reload share
the interactive approval flow.

The Changes sidebar compares the agent workspace with the **session's last
saved source value**, initially the bind-time snapshot. It shows:

- **Uncommitted changes:** a delta from the baseline when history is unchanged;
  otherwise pending edits above the current HEAD.
- **Commits to save:** current history absent from the baseline.
- **Checkpoint-only commits:** baseline history absent from the current value,
  for example after resetting agent history.

Each history section displays at most 20 commits and reports truncation. Preview
reads immutable session values; it does not poll the live checkout for incoming
host commits.

| Action | Behavior |
| --- | --- |
| Ctrl+S | `source.export(path: hostRoot, from: baseline)`, then advance baseline to that exact source value on success. |
| Ctrl+U | Snapshot `currentWorkspace` again, replace the agent workspace, and reset the baseline. Unsaved agent work is discarded. |

Save leaves the conversation and its workspace in place, including original
source commit hashes. Only reload adopts host history. A failed save leaves
the baseline unchanged so it can be retried.

The empty Changes sidebar means this session has no work beyond its last saved
value. It does not imply that the agent and checkout have identical histories.
Pushing the agent's HEAD publishes that agent's history; publishing the combined
result of several agents uses the integrated checkout or workspace.

Implementation: [agent binding](../../internal/cmd/dagger/agent.go),
[save and reload](../../internal/cmd/dagger/llm.go), and
[Changes preview](../../internal/cmd/dagger/llm_changes.go).

## Agent branch sync (proposed)

Add a separate action that mirrors an agent's Git history to a local branch:

```text
refs/heads/dagger/agents/<trace-id>/<agent-handle>
```

Dagger owns this ref. Each sync captures one source Workspace value, imports
its reachable Git history, and sets the branch to that value's exact HEAD.
The branch may move forward, backward, or to amended history. Commit hashes,
messages, authors, and parent relationships are preserved. No ancestry
relationship to a previously synced value is required.

The user can inspect this branch and integrate its commits into their own
branch with ordinary Git. Several agents have separate refs and can continue
working independently. Sync does not change the agent workspace or advance
the last-saved baseline used for integration into the user's checkout.

The branch identity should be retained with the session across resume, even
when a resumed invocation emits a new trace. Forked agents need distinct
identities. Previous synced tips should be recorded in the branch reflog.

**Pending edits and worktrees.** A branch ref represents committed history;
pending workspace edits need a separate representation. For a branch that is
not checked out, updating the ref is sufficient to mirror HEAD. It does not
require resetting the user's current checkout.

To mirror the complete workspace as files, a possible extension is a dedicated
Dagger-owned worktree for the agent branch: reset it to the captured HEAD and
then materialize the pending overlay, with the index matching HEAD. Each sync
would replace the prior mirrored file state as well as the ref. Ownership of
the ref alone does not authorize overwriting edits in a user-managed worktree
that happens to have it checked out. Handling checked-out branches and choosing
how to expose pending edits remain design decisions for this action.

This provides a local destination for rewritten agent history. Integrating a
revision into a user branch that already contains the earlier version remains
a separate operation; branch sync does not change incremental export's
ancestry requirement.

## Known gaps

### Best-effort resumability

Snapshot success does not guarantee that a session can resume without its
originating client. Capture can fall back to a live workspace or a
session-scoped Git reconstruction, and remote-backed recipes still depend on
remote access and availability of the base commit. The public Workspace API
does not report which guarantee was achieved. Sessions can use these fallbacks
today; reliable capture and reporting of resumability remain experimental.

### Rewriting already-saved history

Rewriting commits created since the last save is compatible with incremental
delivery as long as the saved baseline remains an ancestor. Rewriting a commit
at or before that baseline is currently rejected by incremental export.

For example, save commit A, reset to its parent, and create amended commit A2.
Ctrl+S still compares from A, which is no longer an ancestor of A2, and fails.
The proposed agent branch sync would expose the rewritten history on its own
ref. Incrementally integrating that revision into a user branch still needs
a defined workflow. Ctrl+U discards unsaved work, so it is not a recovery
operation for this case.

## Status

Snapshot, commit/reset, pull, push, save, and CLI integration are implemented on
this branch as of 2026-09-14. Snapshot remains experimental, with best-effort
resumability and interactive capture approval. Agent branch sync is proposed;
integration of revisions to already-saved history remains an open design gap.
