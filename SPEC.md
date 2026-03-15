# Workspace Git Commit API

## Summary

Add `Workspace.withBranch`, `Workspace.stage`, and `Workspace.commit` so that
LLM agents running inside the Dagger engine can commit changesets to Git
branches on the user's host — without disrupting the user's checked-out
working tree.

Every branch gets its own **`git worktree`** on the host filesystem. This means
all existing Workspace operations (`glob`, `search`, `file`, `directory`,
`exists`, `findUp`) work identically on any branch, and the user can `cd` into
any worktree to watch agents work in real-time.

## Motivation

An autonomous LLM agent hierarchy needs to commit parallel work back to the
user's Git repository so the user can observe progress. Multiple agents may
work on different branches simultaneously. The design must:

1. **Never disrupt the user's checked-out state** (unless they are on that
   branch).
2. **Commit precisely the changeset** — never sweep in unrelated user edits.
3. Let existing Workspace read operations (`glob`, `search`, etc.) work on any
   branch without special-casing.
4. Let the user observe agent work in real-time via normal filesystem tools.
5. **Coexist with local user changes** — the user may have work-in-progress in
   the same worktree. Agent changes end up staged; user changes remain
   unstaged.

## API

### Fields

```graphql
type Workspace {
  """The Git branch this workspace is on."""
  branch: String!
  """Absolute path to the workspace root directory."""
  root: String!
  """The client ID that owns this workspace's host filesystem."""
  clientId: String!
}
```

### Methods

```graphql
extend type Workspace {
  """
  Return a Workspace for the given branch. If the branch is different from
  the currently checked-out branch, a git worktree is created on the host.
  If the branch does not exist, it is created from the current branch tip.
  """
  withBranch(
    """The branch name (e.g. "agent/auth")."""
    branch: String!
  ): Workspace!

  """
  Apply a Changeset to the workspace and stage the affected paths in git.
  Files are written (added/modified) and removed on disk, then precisely
  the changed paths are staged via git add / git rm. Any pre-existing
  unstaged user edits are preserved as unstaged changes.
  Returns true if any changes were staged, false if the changeset was empty.
  """
  stage(
    """The changes to apply and stage."""
    changes: Changeset!
  ): Boolean!

  """
  Commit whatever is currently staged in the workspace's git index.
  Returns the commit hash. Fails if there is nothing staged.
  """
  commit(
    """The commit message."""
    message: String!
  ): String!
}
```

### Typical Usage (Dang)

```dang
type Agent {
  new(ws: Workspace!) {
    let ws2 = ws.withBranch("agent/feature")

    // First change
    let before = ws2.directory(".")
    let after1 = before.withNewFile("hello.txt", contents: "hello world")
    ws2.stage(changes: after1.changes(before))

    // Second change (reads current state which includes hello.txt)
    let before2 = ws2.directory(".")
    let after2 = before2.withNewFile("bye.txt", contents: "goodbye")
    ws2.stage(changes: after2.changes(before2))

    // Single commit covering both staged changes
    ws2.commit(message: "feat: add greeting files")

    self
  }
}
```

## Semantics

### `stage(changes)`

Applies a changeset to the worktree and stages exactly the affected paths,
while preserving any pre-existing unstaged user edits. This uses the
technique from [container-use PR #234](https://github.com/dagger/container-use/pull/234):

1. **Capture** user's unstaged changes: `git diff --binary` → save as patch.
2. **Backup** all changes: `git stash create` → virtual stash ref (does not
   pollute the stash list; serves as a safety net).
3. **Reset** to pristine state: `git reset --hard HEAD`.
4. **Export** the changeset diff to the worktree (merge-mode write of
   added/modified files, deletion of removed files — same mechanism as
   `Changeset.export`).
5. **Stage** precisely the affected paths:
   - `git add -- <added> <modified>`
   - `git rm -- <removed>`
6. If the user had unstaged changes:
   a. `git commit -m "dagger: staging temp"` — temporarily commit the staged
      state so it survives the next steps.
   b. `git apply <user-patch>` — restore user's changes on disk.
   c. `git reset` — unstage the user's re-applied changes (git apply stages
      them by default).
   d. `git reset --soft HEAD~1` — undo the temp commit, moving agent changes
      back to the staging area.
7. On error at any point: print recovery command
   `git stash apply <stashRef>` so the user can recover their work.

**End result:** agent's changes are staged, user's changes are unstaged. A
subsequent `git commit` commits exactly the agent's work.

Return `true` if any paths were staged, `false` if the changeset was empty.

### `commit(message)`

1. Run `git commit -m <message>`. Since `stage` already staged the changes,
   no further staging is needed.
2. Return the commit hash via `git rev-parse HEAD`.

This is intentionally simple — all the precision lives in `stage`.

### Incremental workflow

`stage` and `commit` are independent operations. You can call `stage` multiple
times to build up a set of changes, then `commit` once. Or stage and commit
one-at-a-time. The git index is the only state — no internal bookkeeping.

```dang
// Stage several changesets, commit once:
ws.stage(changes: changeset1)
ws.stage(changes: changeset2)
ws.commit(message: "feat: both changes")

// Or stage and commit individually:
ws.stage(changes: changeset1)
ws.commit(message: "feat: first change")
ws.stage(changes: changeset2)
ws.commit(message: "feat: second change")
```

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

### 1. Workspace struct (`core/workspace.go`)

```go
type Workspace struct {
    Root     string `field:"true"`
    ClientID string `field:"true"`
    Branch   string `field:"true" doc:"The Git branch this workspace is on."`

    // RepoRoot is the path to the main repo (where .git/ lives).
    // Not exposed in the schema. Needed to create worktrees.
    RepoRoot string
}
```

### 2. Client-side operations via session attachables

The `stage` operation requires careful sequencing of the file export and git
commands. It uses two client round-trips:

1. **Pre-export setup** (`GitStageSetup` diffcopy): captures user's unstaged
   changes, creates a virtual stash backup, and resets the worktree to a
   pristine state. Returns the user's diff patch (may be empty).

2. **File export** (`LocalDirExport`): writes changeset files to the now-clean
   worktree.

3. **Post-export staging** (`GitStageFinalize` diffcopy): stages the changeset
   paths, then restores user's unstaged changes using the temp-commit technique
   from container-use.

| Operation | Direction | Opts field | Handler |
|-----------|-----------|------------|---------|
| Detect branch | import (source) | `GitBranchDetect` | `git symbolic-ref --short HEAD` |
| Create worktree | import (source) | `GitWorktreeAdd` | `git worktree add` |
| Stage setup | import (source) | `GitStageSetup` | capture diff, stash create, reset --hard |
| Stage finalize | import (source) | `GitStageFinalize` | git add/rm, temp commit, apply user patch, reset |
| Commit | import (source) | `GitCommit` | `git commit -m` / `git rev-parse HEAD` |

#### Stage setup (`GitStageSetup`)

```go
type GitStageSetupOpts struct{}
```

Client handler:
```go
case opts.GitStageSetup != nil:
    // 1. Capture user's unstaged changes
    userPatch := git("diff", "--binary")

    // 2. Virtual stash as safety net (no stash list pollution)
    stashRef := git("stash", "create")

    // 3. Reset to pristine state
    git("reset", "--hard", "HEAD")

    // Return: stashRef + "\n" + userPatch
    stream.SendMsg(&BytesMessage{Data: ...})
```

#### Stage finalize (`GitStageFinalize`)

```go
type GitStageFinalizeOpts struct {
    Added     []string `json:"added"`
    Modified  []string `json:"modified"`
    Removed   []string `json:"removed"`
    StashRef  string   `json:"stash_ref"`
    UserPatch string   `json:"user_patch"`  // base64-encoded, may be empty
}
```

Client handler:
```go
case opts.GitStageFinalize != nil:
    // Stage the changeset paths
    git("add", "--", added..., modified...)
    git("rm", "-f", "--", removed...)

    if userPatch != "" {
        // Temp-commit the staged state
        git("commit", "-m", "dagger: staging temp")
        // Restore user's changes on disk
        git("apply", userPatch)
        // Unstage user's changes
        git("reset")
        // Undo temp commit, agent changes back to staging
        git("reset", "--soft", "HEAD~1")
    }

    staged := len(added) + len(modified) + len(removed) > 0
    stream.SendMsg(&BytesMessage{Data: strconv.FormatBool(staged)})
```

#### Commit operation (`GitCommit`)

```go
type GitCommitOpts struct {
    Message string `json:"message"`
}
```

Client handler:
```go
case opts.GitCommit != nil:
    git("commit", "-m", opts.GitCommit.Message)
    hash := git("rev-parse", "HEAD")
    stream.SendMsg(&BytesMessage{Data: hash})
```

### 3. Schema resolvers (`core/schema/workspace.go`)

#### `stage` (new)

Wrapped with `DagOpWrapper` (needs buildkit session to mount changeset).

```go
func (s *workspaceSchema) stage(ctx context.Context, parent, args) (dagql.Boolean, error) {
    changeset := args.Changes.Load(ctx, srv)
    paths := changeset.ComputePaths(ctx)

    // Step 1: Pre-export setup — capture user changes, reset to clean state
    stashRef, userPatch := bk.GitStageSetup(ctx, ws.Root)

    // Step 2: Export diff to worktree (merge mode, handles removals)
    dir := changeset.Before.Diff(changeset.After)
    bk.LocalDirExport(ctx, mountedDir, ws.Root, true, paths.Removed)

    // Step 3: Stage paths and restore user changes
    staged := bk.GitStageFinalize(ctx, ws.Root, paths, stashRef, userPatch)
    return dagql.Boolean(staged), nil
}
```

#### `commit` (simplified)

Wrapped with `DagOpWrapper`.

```go
func (s *workspaceSchema) commit(ctx context.Context, parent, args) (dagql.String, error) {
    hash := bk.GitCommit(ctx, ws.Root, args.Message)
    return dagql.String(hash), nil
}
```

### 4. Removed complexity

The following are no longer needed:
- **Temp `GIT_INDEX_FILE`** — staging goes into the real index.
- **`git apply --cached`** — files are written by `LocalDirExport`, staged
  with `git add`.
- **`GitApplyAndCommit`** import option — replaced by separate setup/finalize
  and commit options.

### 5. Files to modify

| File | Change |
|------|--------|
| `core/workspace.go` | No change. |
| `core/changeset.go` | Remove `GitCommit` method (no longer needed). |
| `core/schema/workspace.go` | Replace `apply`/`commit` with `stage`/`commit`. |
| `engine/opts.go` | Replace `GitApplyCommitOpts` with `GitStageSetupOpts`, `GitStageFinalizeOpts`, `GitCommitOpts`. |
| `engine/client/filesync.go` | Replace `gitApplyAndCommit` with setup/finalize/commit handlers. |
| `engine/buildkit/filesync.go` | Replace `GitCommitChangeset` with `GitStageSetup`, `GitStageFinalize`, `GitCommit`. Remove `randomHex`. |
| `core/integration/workspace_test.go` | Rewrite tests for `stage` + `commit` API. |

## Data Flow

```
Agent (engine)                        Host (client)
──────────────                        ─────────────

ws = currentWorkspace()
  ── GitBranch ────────────────────►  git symbolic-ref --short HEAD
  ◄── "main" ──────────────────────

ws2 = ws.withBranch("agent/auth")
  ── GitWorktreeAdd ───────────────►  git worktree add -b agent/auth
                                        ~/src/project-worktrees/agent-auth
  ◄── "/home/.../agent-auth" ──────

ws2.stage(changes: changeset)
  ── GitStageSetup ────────────────►  git diff --binary → userPatch
                                       git stash create  → stashRef
                                       git reset --hard HEAD
  ◄── stashRef + userPatch ────────

  ── LocalDirExport ───────────────►  write changed files to clean worktree

  ── GitStageFinalize ─────────────►  git add -- new.txt modified.txt
                                       git rm -- deleted.txt
                                       git commit -m "dagger: staging temp"
                                       git apply <userPatch>
                                       git reset
                                       git reset --soft HEAD~1
  ◄── "true" ──────────────────────

ws2.commit(message: "feat: auth")
  ── GitCommit ────────────────────►  git commit -m "feat: auth"
                                       git rev-parse HEAD
  ◄── "abc1234..." ────────────────
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

# User can edit files in the worktree — their changes stay unstaged
$ echo "user note" >> ~/src/project-worktrees/agent-auth/notes.txt

# When done, merge from the main repo
$ cd ~/src/project
$ git merge agent/auth
```

## Edge Cases

1. **Branch already exists locally**: `git worktree add <path> <branch>` (no
   `-b`). If the branch is already checked out in another worktree, git errors
   — this is correct, we already have a Workspace for it.

2. **Worktree directory already exists**: Check whether it's a valid worktree
   for the requested branch. If yes, reuse it. If stale, clean up and recreate.

3. **Working on the main checkout**: When `ws.Branch` equals the checked-out
   branch, `ws.Root == ws.RepoRoot`, and `stage`/`commit` operate directly on
   the main checkout. The stash-and-restore dance preserves user's unstaged
   edits.

4. **Detached HEAD**: `currentWorkspace` falls back to `"HEAD"` for the branch
   name. `withBranch` always creates a named branch.

5. **Empty changeset**: `stage` returns `false`. A subsequent `commit` would
   fail with "nothing to commit" unless something else was staged.

6. **Concurrent agents on different branches**: Each worktree has its own index
   file, so staging and commits don't interfere.

7. **Concurrent agents on the same branch**: The git index lock serializes
   access. Second agent's `git add` blocks until the first completes.

8. **Commit with nothing staged**: `git commit` fails. The error surfaces to
   the agent. Use `stage` first.

9. **User has unstaged changes to a changeset file**: The `reset --hard` wipes
   the user's version, the export writes the changeset version, and the user
   patch restores the user's changes as unstaged on top. If the user's patch
   conflicts with the changeset, `git apply` fails and the error includes the
   `git stash apply <ref>` recovery command.

10. **Error recovery**: `git stash create` produces a ref that survives
    indefinitely (until GC). On any error after `reset --hard`, the error
    message includes `git stash apply <ref>` so the user can recover their
    prior state manually.

11. **Worktree cleanup**: Not addressed in this spec. Future work could add
    cleanup when the Dagger session ends.
