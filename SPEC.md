# Workspace & Worktree — Git-Aware Agent Commit API

## Summary

Provide a two-tier API for LLM agents to read from and commit to Git branches
on the user's host:

- **`Workspace`** — read-only view of the user's repository. Detects the repo
  root and current branch. Provides `directory`, `file`, `glob`, `search`,
  `exists`, `findUp`.

- **`Worktree`** — read-write handle for a specific branch, returned by
  `Workspace.worktree(branch)`. Tracks pending changesets applied via
  `withChanges`, and commits them atomically with `commit`. Each branch gets
  its own **`git worktree`** on the host filesystem.

The `Worktree` type is functional: `withChanges` returns a new `Worktree` with
the changeset applied to the host and appended to the pending list. `commit`
creates a Git commit from exactly the accumulated changesets, then returns a
clean `Worktree` with no pending changes.

## Motivation

An autonomous LLM agent hierarchy needs to commit parallel work back to the
user's Git repository so the user can observe progress. Multiple agents may
work on different branches simultaneously. The design must:

1. **Never disrupt the user's checked-out state** (unless they target that
   branch).
2. **Commit precisely the changeset** — never sweep in unrelated user edits.
3. Let existing Workspace read operations work on any branch without
   special-casing.
4. Let the user observe agent work in real-time via normal filesystem tools.
5. **Coexist with local user changes** — the user may have work-in-progress in
   the same worktree. The commit flow must preserve it.

## API

### Workspace (read-only)

```graphql
type Workspace {
  "The Git branch this workspace is on."
  branch: String!
  "Absolute path to the workspace root directory."
  root: String!
  "The client ID that owns this workspace's host filesystem."
  clientId: String!

  "Return a Worktree for the given branch."
  worktree(
    "The branch name (e.g. \"agent/auth\")."
    branch: String!
  ): Worktree!

  # ... existing read methods unchanged:
  directory(path: String!, ...): Directory!
  file(path: String!): File!
  exists(path: String!, ...): Boolean!
  glob(pattern: String!): [String!]!
  search(...): [SearchResult!]!
  findUp(name: String!, from: String): String
}
```

`Workspace.worktree(branch)` creates (or reuses) a git worktree for the given
branch on the host. If `branch` matches the workspace's current branch, the
worktree points at the main checkout. If the branch doesn't exist yet, it is
created from the current branch tip.

### Worktree (read-write)

```graphql
type Worktree {
  "The Git branch this worktree is on."
  branch: String!

  """
  Apply a Changeset to the worktree and track it for the next commit.
  Files are written (added/modified) and removed on disk immediately.
  Returns a new Worktree with the changeset appended to the pending list.
  """
  withChanges(changes: Changeset!): Worktree!

  """
  Commit all pending changesets to the branch. The commit contains exactly
  the accumulated changes from prior withChanges calls — nothing more.
  Returns a new Worktree with an empty pending list.
  """
  commit(message: String!): Worktree!

  # Read methods (same signatures as Workspace, operating on the worktree root):
  directory(path: String!, ...): Directory!
  file(path: String!): File!
  exists(path: String!, ...): Boolean!
  glob(pattern: String!): [String!]!
  search(...): [SearchResult!]!
  findUp(name: String!, from: String): String
}
```

### Typical Usage (Dang)

```dang
type Agent {
  new(ws: Workspace!) {
    let wt = ws.worktree("agent/feature")

    // First change
    let before = wt.directory(".")
    let after = before.withNewFile("hello.txt", contents: "hello world")
    let wt2 = wt.withChanges(after.changes(before))

    // Second change
    let before2 = wt2.directory(".")
    let after2 = before2.withNewFile("bye.txt", contents: "goodbye")
    let wt3 = wt2.withChanges(after2.changes(before2))

    // Single commit covering both changes
    wt3.commit(message: "feat: add greeting files")

    self
  }
}
```

## Semantics

### `Workspace.worktree(branch)`

1. If `branch` equals the workspace's current branch, return a `Worktree`
   whose root is the main checkout (`ws.Root`).
2. Otherwise, ask the client to `git worktree add` at the conventional path
   (see Worktree Layout). If the branch doesn't exist, create it with `-b`.
3. Return a `Worktree` with an empty pending changeset list.

### `Worktree.withChanges(changes)`

1. Export the changeset to the worktree root on the host (same mechanism as
   `Changeset.export` — merge-mode write of the diff layer, removal of deleted
   paths).
2. Return a new `Worktree` with `changes` appended to the internal pending
   list.

The changeset is applied to disk immediately so that:
- The user can see progress in real-time.
- Subsequent `wt.directory(".")` calls reflect the applied changes.

### `Worktree.commit(message)`

1. Merge all pending changesets into a single combined changeset (using the
   existing `Changeset.withChanges` / `withChangesets` machinery if needed, or
   simply accumulating paths).
2. Generate a patch from the combined changeset.
3. Export the patch to a temp file on the client (outside the repo).
4. Apply the patch and create the commit via git plumbing:
   - `git stash --include-untracked` (if local user changes exist)
   - Create a temp `GIT_INDEX_FILE`
   - `git read-tree HEAD` into the temp index
   - `git apply --cached <patch>` into the temp index
   - `git write-tree` → `git commit-tree` → `git update-ref HEAD`
   - `git reset --hard HEAD` to update the worktree
   - `git stash pop` (if stashed)
   - Clean up temp files
5. Return a new `Worktree` with an empty pending list.

This ensures the commit contains **exactly** the accumulated changesets,
regardless of what else exists in the working tree.

### Read methods on `Worktree`

`directory`, `file`, `exists`, `glob`, `search`, and `findUp` all delegate to
the same host-filesystem implementations used by `Workspace`, but rooted at the
worktree's path. They are `CachePerClient` (not persistently cached) so they
always reflect the current disk state.

## Worktree Layout

Given a repo at `~/src/dagger`, worktrees are placed under a sibling
`-worktrees` directory:

```
~/src/dagger/                          # main checkout (user's workspace)
~/src/dagger-worktrees/agent-auth/     # worktree for branch "agent/auth"
~/src/dagger-worktrees/agent-tests/    # worktree for branch "agent/tests"
```

Path derivation:
- Take the repo root directory (`~/src/dagger`).
- Append `-worktrees` to form the parent (`~/src/dagger-worktrees/`).
- Sanitize the branch name: replace `/` with `-`.
- The worktree path is `<parent>/<sanitized-branch>`.

Rationale for this location:
- **Outside the repo tree** — avoids `glob` and `search` on the main workspace
  accidentally traversing into worktree directories.
- **Discoverable** — sits right next to the repo, easy to `cd` into.
- **No `.gitignore` management** — nothing added inside the main repo.

## Implementation

### 1. New `Worktree` type (`core/worktree.go`)

```go
type Worktree struct {
    // Root is the absolute path to the worktree directory on the host.
    Root     string `field:"true"`
    // ClientID routes host operations through the correct client session.
    ClientID string `field:"true"`
    // Branch is the Git branch this worktree tracks.
    Branch   string `field:"true"`
    // RepoRoot is the path to the main repo (where .git/ lives).
    RepoRoot string

    // Pending is the list of changesets applied since the last commit.
    // Accumulated by withChanges, consumed by commit.
    Pending []*Changeset
}
```

### 2. Workspace changes (`core/workspace.go`)

Remove `withBranch`, `apply`, `commit` from Workspace. Add `worktree`.

`Branch` and `RepoRoot` stay on Workspace for detection purposes. The
`worktree` method creates the git worktree (if needed) and returns a
`Worktree`.

```go
type Workspace struct {
    Root     string `field:"true"`
    ClientID string `field:"true"`
    Branch   string `field:"true"`
    RepoRoot string
}
```

### 3. Schema resolvers (`core/schema/workspace.go`, `core/schema/worktree.go`)

#### On `Workspace`:

- **`worktree(branch)`** — creates/reuses git worktree via `GitWorktreeAdd`,
  returns `Worktree` with empty `Pending`.

#### On `Worktree`:

- **`withChanges(changes)`** — wraps with `DagOpWrapper`. Loads the changeset,
  calls `changeset.Export(ctx, wt.Root)` to apply files, returns a new
  `Worktree` with the changeset appended to `Pending`.

- **`commit(message)`** — wraps with `DagOpWrapper`. Merges pending changesets,
  generates a patch, exports to temp file, applies via git plumbing, returns
  clean `Worktree`.

- **`directory`, `file`, `exists`, `glob`, `search`, `findUp`** — same
  implementations as `Workspace` but rooted at `wt.Root`.

### 4. Client-side operations (unchanged from current impl)

| Operation | Mechanism |
|-----------|-----------|
| Detect branch | `diffcopy` import with `GitBranchDetect` |
| Create worktree | `diffcopy` import with `GitWorktreeAdd` |
| Export changeset to disk | `CopyToCaller` (dir export, merge mode) |
| Apply patch + commit | `diffcopy` import with `GitApplyAndCommit` |
| Export patch temp file | `LocalFileExport` to `/tmp/dagger-changeset-*.patch` |

### 5. Files to modify

| File | Change |
|------|--------|
| `core/worktree.go` | **New.** `Worktree` struct, type methods. |
| `core/workspace.go` | Remove `RepoRoot` if not needed; keep `Branch`. |
| `core/schema/worktree.go` | **New.** Schema resolvers for `Worktree`. |
| `core/schema/workspace.go` | Replace `withBranch`/`commit`/`apply` with `worktree`. Move read method impls to shared helpers. |
| `core/integration/workspace_test.go` | Rewrite commit/apply tests to use `Worktree` API. |
| `engine/buildkit/filesync.go` | No change (existing `GitCommitChangeset`). |
| `engine/client/filesync.go` | No change (existing `gitApplyAndCommit`). |
| `engine/opts.go` | No change. |

## Data Flow

```
Agent (engine)                        Host (client)
──────────────                        ─────────────

ws = currentWorkspace()
  ── GitBranch ────────────────────►  git symbolic-ref --short HEAD
  ◄── "main" ──────────────────────

wt = ws.worktree("agent/auth")
  ── GitWorktreeAdd ───────────────►  git worktree add -b agent/auth
                                        ~/src/project-worktrees/agent-auth
  ◄── "/home/.../agent-auth" ──────

wt.directory(".").withNewFile(...)   (engine-side, pure)

wt2 = wt.withChanges(changeset)
  ── Changeset.Export ─────────────►  write files to ~/src/project-worktrees/agent-auth/
                                       (merge mode, removals applied)

wt2.directory(".")                 ►  read from ~/src/project-worktrees/agent-auth/
                                       (reflects applied changes)

wt3 = wt2.commit("feat: auth")
  ── LocalFileExport ──────────────►  write /tmp/dagger-changeset-xxxx.patch
  ── GitApplyAndCommit ────────────►  stash → temp index → apply --cached
                                       → write-tree → commit-tree
                                       → update-ref → reset --hard → stash pop
  ◄── "<commit-hash>" ────────────
```

## User Experience

```bash
# User is working on main
~/src/project $ git branch
* main

# Agents start working — worktrees appear as siblings
$ ls ~/src/project-worktrees/
agent-auth/
agent-tests/

# Watch an agent work in real-time
$ cd ~/src/project-worktrees/agent-auth
$ watch git log --oneline -5

# When done, merge from the main repo
$ cd ~/src/project
$ git merge agent/auth
```

## Edge Cases

1. **Branch already exists locally**: `git worktree add <path> <branch>` (no
   `-b`). If the branch is already checked out in another worktree, git errors
   — this is correct, we already have a Worktree for it.

2. **Worktree directory already exists**: Check whether it's a valid worktree
   for the requested branch. If yes, reuse it. If stale, clean up and recreate.

3. **Working on the main checkout**: `ws.worktree(ws.branch)` returns a
   Worktree whose root is the main checkout. Commits go directly to the
   checked-out branch. The stash/pop flow preserves any user WIP.

4. **Detached HEAD**: `currentWorkspace` falls back to `"HEAD"` for the branch
   name. `worktree` always creates a named branch.

5. **Empty changeset**: `git apply --cached` on an empty patch is a no-op.
   `commit-tree` still creates a commit (equivalent to `--allow-empty`).

6. **Concurrent agents on different branches**: Each worktree has its own index
   file, so commits don't interfere.

7. **Multiple `withChanges` before `commit`**: The pending list accumulates all
   changesets. At `commit` time they are merged into one patch. If changesets
   conflict with each other, the merge fails with a clear error.

8. **User edits in the worktree**: The commit flow stashes user changes before
   updating the worktree and restores them after. If the stash pop conflicts,
   the error surfaces to the agent.

9. **Worktree cleanup**: Not addressed in this spec. Future work could add
   cleanup when the Dagger session ends.

10. **`withChanges` without `commit`**: Perfectly valid. The changes are on disk
    (visible to the user) but not committed. A subsequent `commit` will capture
    them. If the Worktree is discarded, the changes remain as uncommitted
    modifications in the worktree.
