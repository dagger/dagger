# Workspace.commit — Git Worktree-Based Branch Commit API

## Summary

Add `Workspace.withBranch` and `Workspace.commit` so that LLM agents running
inside the Dagger engine can commit changesets to Git branches on the user's
host — without disrupting the user's checked-out working tree.

Every branch gets its own **`git worktree`** on the host filesystem. This means
all existing Workspace operations (`glob`, `search`, `file`, `directory`,
`exists`, `findUp`) work identically on any branch, and the user can `cd` into
any worktree to watch agents work in real-time.

## Motivation

An autonomous LLM agent hierarchy needs to commit parallel work back to the
user's Git repository so the user can observe progress. Multiple agents may
work on different branches simultaneously. The design must:

1. **Never disrupt the user's checked-out state** (unless they are on that branch).
2. **Minimize data transfer** between engine and client.
3. Let existing Workspace read operations (`glob`, `search`, etc.) work on any
   branch without special-casing.
4. Let the user observe agent work in real-time via normal filesystem tools.

## API

### New Fields

```graphql
type Workspace {
  """The Git branch this workspace is on."""
  branch: String!
  # ... existing fields: root, clientId
}
```

### New Methods

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
  Export a Changeset to the workspace's branch and create a Git commit.
  The changeset files are written to the worktree and committed with the
  given message. This is a side-effect operation.
  """
  commit(
    """The changes to commit."""
    changes: Changeset!
    """The commit message."""
    message: String!
  ): Void!
}
```

### Semantics

- **`currentWorkspace`** is augmented to detect the current branch via
  `git symbolic-ref --short HEAD`. The `Branch` field is populated
  automatically.

- **`withBranch(branch)`** where `branch` equals the workspace's current
  branch is a no-op (returns self). Otherwise it asks the client to run
  `git worktree add` and returns a new Workspace whose `Root` points at the
  worktree directory.

- **`commit(changes, message)`** exports the changeset to the workspace root
  (same mechanism as `Changeset.export`) then runs `git add -A` and
  `git commit -m <message>` in the worktree directory on the client side.

- All existing Workspace operations are **unchanged**. They already operate on
  `ws.Root`, which for a worktree simply points at a different directory on the
  host.

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
  accidentally traversing into worktree directories. (The host-side `glob` uses
  `filepath.WalkDir` which does not respect `.gitignore`; `search` defaults to
  `--no-ignore`.)
- **Discoverable** — sits right next to the repo, easy to `cd` into.
- **No `.gitignore` management** — nothing added inside the main repo.

## Implementation

### 1. Workspace struct (`core/workspace.go`)

Add two fields:

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

When `withBranch` creates a worktree, the new Workspace has a different `Root`
(the worktree path) but the same `RepoRoot` (the original repo).

### 2. Client-side operations via session attachables

Three new operation types routed through the existing `DiffCopy` gRPC streams.

#### 2a. Detect current branch (`LocalImportOpts` / `FileSyncSource`)

New field on `LocalImportOpts` in `engine/opts.go`:

```go
GitBranchDetect bool
```

Client handler in `engine/client/filesync.go` `FilesyncSource.DiffCopy`:

```go
case opts.GitBranchDetect:
    cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "--short", "HEAD")
    cmd.Dir = absPath
    out, err := cmd.Output()
    // Return trimmed branch name as BytesMessage
```

Engine-side wrapper in `engine/buildkit/filesync.go`:

```go
func (c *Client) GitBranch(ctx context.Context, repoDir string) (string, error)
```

#### 2b. Create worktree (`LocalImportOpts` / `FileSyncSource`)

New field on `LocalImportOpts`:

```go
GitWorktreeAdd *GitWorktreeAddOpts
```

```go
type GitWorktreeAddOpts struct {
    Branch       string
    WorktreePath string
}
```

Client handler:

1. If `WorktreePath` already exists and is a valid worktree, return it as-is.
2. Try `git worktree add <path> <branch>` (existing branch).
3. If the branch doesn't exist, `git worktree add -b <branch> <path>` (create).
4. Return the resolved absolute path as `BytesMessage`.

Engine-side wrapper:

```go
func (c *Client) GitWorktreeAdd(ctx context.Context, repoDir, branch, worktreePath string) (string, error)
```

#### 2c. Commit in worktree (`LocalExportOpts` / `FilesyncTarget`)

New field on `LocalExportOpts`:

```go
GitCommit *GitCommitOpts
```

```go
type GitCommitOpts struct {
    Message string
}
```

When `GitCommit` is set, `FilesyncTarget.DiffCopy`:

1. Receives the directory export as usual (file writes via `fsutil.Receive`),
   applying it to the export `Path` (which is the worktree root).
2. After the filesystem write completes, runs in the same directory:
   ```
   git add -A
   git commit --allow-empty -m "<message>"
   ```
3. Returns success/error.

This piggybacks on the existing `LocalDirExport` flow — the only addition is
the post-write git commit step.

Engine-side: modify `LocalDirExport` or add a new method:

```go
func (c *Client) GitCommitChangeset(
    ctx context.Context,
    srcPath string,
    destPath string,
    merge bool,
    removePaths []string,
    message string,
) error
```

### 3. Schema resolvers (`core/schema/workspace.go`)

#### `currentWorkspace` (modified)

After detecting the repo root, also detect the branch:

```go
branch, err := bk.GitBranch(ctx, repoRoot)
if err != nil {
    branch = "HEAD" // detached HEAD fallback
}
result := &core.Workspace{
    Root:     repoRoot,
    ClientID: clientMetadata.ClientID,
    Branch:   branch,
    RepoRoot: repoRoot,
}
```

#### `withBranch` (new)

```go
func (s *workspaceSchema) withBranch(
    ctx context.Context,
    parent *core.Workspace,
    args struct{ Branch string },
) (*core.Workspace, error) {
    if parent.Branch == args.Branch {
        return parent, nil
    }
    worktreePath := parent.RepoRoot + "-worktrees/" + sanitizeBranch(args.Branch)
    actualPath, err := bk.GitWorktreeAdd(ctx, parent.RepoRoot, args.Branch, worktreePath)
    if err != nil {
        return nil, err
    }
    return &core.Workspace{
        Root:     actualPath,
        ClientID: parent.ClientID,
        Branch:   args.Branch,
        RepoRoot: parent.RepoRoot,
    }, nil
}
```

#### `commit` (new)

```go
func (s *workspaceSchema) commit(
    ctx context.Context,
    parent dagql.ObjectResult[*core.Workspace],
    args struct {
        Changes dagql.ID[*core.Changeset]
        Message string
    },
) (core.Void, error) {
    ws := parent.Self()
    changeset := /* load args.Changes */

    // Export changeset + git commit via the combined operation
    err := bk.GitCommitChangeset(ctx,
        srcRoot,     // mounted changeset diff
        ws.Root,     // worktree path
        true,        // merge
        removePaths, // from changeset
        args.Message,
    )
    return core.Void{}, err
}
```

### 4. Files to modify

| File | Change |
|------|--------|
| `core/workspace.go` | Add `Branch`, `RepoRoot` fields |
| `core/schema/workspace.go` | Add `withBranch`, `commit` resolvers; branch detection in `currentWorkspace` |
| `engine/opts.go` | Add `GitBranchDetect`, `GitWorktreeAddOpts`, `GitCommitOpts` |
| `engine/client/filesync.go` | Handle git-branch-detect, git-worktree-add in `FileSyncSource.DiffCopy`; handle git-commit in `FileSyncTarget.DiffCopy` |
| `engine/buildkit/filesync.go` | Add `GitBranch()`, `GitWorktreeAdd()`, `GitCommitChangeset()` |
| `core/integration/workspace_test.go` | Tests for the new API |

## Data Flow

```
Agent (engine)                        Host (client)
──────────────                        ─────────────

ws = currentWorkspace()
  ── GitBranch ────────────────────►  git symbolic-ref --short HEAD
  ◄── "main" ──────────────────────   ← returns branch name

ws2 = ws.withBranch("agent/auth")
  ── GitWorktreeAdd ───────────────►  git worktree add
                                        ~/src/project-worktrees/agent-auth
                                        -b agent/auth
  ◄── "/home/.../agent-auth" ──────   ← returns resolved path

ws2.glob("**/*.go")                ►  WalkDir in ~/src/project-worktrees/agent-auth/
ws2.search(pattern: "TODO")        ►  rg in ~/src/project-worktrees/agent-auth/
ws2.file("main.go")                ►  read from ~/src/project-worktrees/agent-auth/

ws2.commit(changes, "feat: auth")
  ── Changeset.Export ─────────────►  write files to ~/src/project-worktrees/agent-auth/
     + GitCommit                       git add -A
                                       git commit -m "feat: auth"
```

## User Experience

```bash
# User is working on main
~/src/project $ git branch
* main

# Agents start working — worktrees appear as siblings
$ ls ~/src/
project/
project-worktrees/

$ ls ~/src/project-worktrees/
agent-auth/
agent-tests/

# Watch an agent work in real-time
$ cd ~/src/project-worktrees/agent-auth
$ watch git log --oneline -5
$ code .   # open in editor

# When done, merge from the main repo
$ cd ~/src/project
$ git merge agent/auth
$ git merge agent/tests
```

## Edge Cases

1. **Branch already exists locally**: `git worktree add <path> <branch>` (no
   `-b`). If the branch is already checked out in another worktree, git errors
   — this is correct, we already have a Workspace for it.

2. **Worktree directory already exists**: Check whether it's a valid worktree
   for the requested branch. If yes, reuse it. If it's stale or for a different
   branch, clean up and recreate.

3. **Committing to the main workspace**: When `ws.Branch` is the checked-out
   branch of the main repo, `ws.Root == ws.RepoRoot`, and commit operates
   directly on the main checkout. This is intentional — if you're on that
   branch, you see the changes.

4. **Detached HEAD**: `currentWorkspace` falls back to `"HEAD"` for the branch
   name. `withBranch` always creates a named branch.

5. **Empty changeset**: `git commit --allow-empty` with the message succeeds.
   Alternatively we could detect and return an error. TBD.

6. **Concurrent agents on different branches**: Each worktree has its own index
   file, so `git add` / `git commit` don't interfere.

7. **Concurrent agents on the same branch**: The git index lock
   (`.git/worktrees/<name>/index.lock`) serializes access. Second agent's
   `git add` blocks until the first completes.

8. **Patch conflicts**: The changeset is exported as a file-level diff. If the
   worktree has diverged from the changeset's `Before` state, the file writes
   may produce incorrect results. Agents should base their changesets on the
   current state of the branch (read via `ws.directory(".")`).

9. **Worktree cleanup**: Not addressed in this spec. Future work could add
   `Workspace.remove()` or automatic cleanup when the Dagger session ends.

10. **Windows / non-Unix paths**: The worktree path derivation uses
    filepath-safe operations. Branch name sanitization replaces `/` with `-`.
    Further special characters may need handling.
