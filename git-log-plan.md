# Plan: `GitRef.log` → `[GitCommit!]!`

Target branch: `core-git-commit` (verified: all dependencies exist there — GitCommit type,
`commit(id:)` → GitCommit, `MergeBase`/`refJoin`, `commonAncestor`, `dagql.ResultArray`/
`ObjectResultArray`, and `WorkspaceGit.head` which is already on main). No entanglement
with `llm-workspace-llm-binding`; that branch picks this up via merge later.

## Target API

```graphql
extend type GitRef {
  """
  Commits reachable from this ref, newest first, starting with the ref's own commit.
  """
  log(
    """Maximum number of commits to return."""
    limit: Int! = 10
    """Only include commits touching these paths (repo-root-relative)."""
    paths: [String!]
    """Exclude commits reachable from this ref (i.e. base..this)."""
    base: GitRefID
  ): [GitCommit!]!
}
```

- **No `WorkspaceGit` changes.** `workspace.git.head` already returns a `GitRef`, so
  workspaces inherit `log` for free, including `.git`-pointer/worktree flattening.
- Deferred (do NOT add in v1): `firstParent`, author filters, date filters,
  `GitCommit.parents`/`history`.

## Key design decision

Log elements are built by `srv.Select`ing the **existing `GitRepository.commit(id:)` field**
per SHA (the `workspaceModules` pattern in `core/schema/workspace_module.go`), NOT
constructed inline. This gives every element a first-class re-loadable recipe ID, the
per-SHA content digest from `gitCommitResult`, cache dedup with direct `commit(sha)` calls,
and persisted-cache support with zero new machinery. `Remote.Lookup` short-circuits full
SHAs (`util/gitutil/ls_remote.go` ~line 177), and rev-list emits full SHAs, so strict-refs
validation passes.

## Phase 1 — Core layer (`core/git.go`)

1a. **Extract a shared multi-ref mount helper from `MergeBase`** (`core/git.go:845`).
    MergeBase already implements the needed topology: same-repo fast path
    (`repo.Backend.mount(ctx, 0 /* depth 0 = FULL fetch */, false, []GitRefBackend{...}, fn)`)
    and cross-repo fallback via `refJoin` (`core/git.go:898`). Shape:

    ```go
    // mountRefs mounts one or two refs with full history and invokes fn with a
    // GitCLI positioned in a repo containing all of them, plus their resolved SHAs.
    func mountRefs(ctx context.Context, refs []*GitRef, fn func(git *gitutil.GitCLI, shas []string) error) error
    ```

    Rewrite `MergeBase` on top of it. Standalone behavior-preserving commit.

1b. **Add `GitRef.Log`:**

    ```go
    type GitLogOptions struct {
        Limit int
        Paths []string
        Base  *GitRef // nil = no exclusion
    }
    func (ref *GitRef) Log(ctx context.Context, opts GitLogOptions) ([]*GitCommitMetadata, error)
    ```

    Inside the `mountRefs` callback:
    - `git rev-list -n <limit> <refSHA> [^<baseSHA>] [-- <paths...>]` for ordered SHAs.
    - Per SHA: `git cat-file commit <sha>` + reuse `parseGitCommitMetadata`
      (`core/git.go:624`) verbatim. All local execs within ONE mount.
      (`git cat-file --batch` is a possible later optimization; not needed for limit≈10–100.)

1c. **Add `GitCommit.PrefillMetadata(meta *GitCommitMetadata)`** — takes `metadataMu`,
    sets `metadata` only if nil. This makes an N-commit log with full field selection cost
    one mount instead of N depth-1 fetches (each lazy `Metadata()` call mounts the backend).

## Phase 2 — Schema layer (`core/schema/git.go`)

Register on the `GitRef` fields block (next to `commonAncestor`, ~line 190):

```go
dagql.NodeFunc("log", s.log).
    View(AfterVersion("v1.0.0-0")).  // new v1 surface, per internal-docs/version-gating.md
    Doc(`Commits reachable from this ref, newest first.`).
    Args(
        dagql.Arg("limit").Doc(`Maximum number of commits to return.`),
        dagql.Arg("paths").Doc(`Only include commits touching these paths (repo-root-relative).`),
        dagql.Arg("base").Doc(`Exclude commits reachable from this ref (i.e. base..this).`),
    ),
```

```go
type gitLogArgs struct {
    Limit dagql.Int `default:"10"`
    Paths dagql.Optional[dagql.ArrayInput[dagql.String]]
    Base  dagql.Optional[core.GitRefID]
}
func (s *gitSchema) log(ctx context.Context, parent dagql.ObjectResult[*core.GitRef], args gitLogArgs,
) (dagql.ResultArray[*core.GitCommit], error)
```

Resolver:

1. Validate `limit >= 1`.
2. If `base` set, load like `commonAncestor` does (`args.Base.Load(ctx, srv)`).
3. `parent.Self().Log(ctx, opts)` → `[]*GitCommitMetadata`.
4. Per SHA: `srv.Select(ctx, parent.Self().Repo, &commit, Selector{Field: "commit", Args: {id: sha}})`.
5. `commit.Self().PrefillMetadata(meta)` before appending to the ResultArray.

No `IsPersistable()` on the field for v1 (matches `commonAncestor`/`targetCommit`);
persistence works per-element via `commit(id:)` + GitCommit's PersistedObject impl —
cross-session reload falls back to lazy cat-file.

## Phase 3 — Regenerate

`dagger --x-release <current> generate` (match the branch's prior `re-generate` commit
`9e64fb876`): SDK clients, GraphQL schema snapshot,
`docs/current_docs/extending/types/git-ref.mdx`, toolchain bindings.

## Phase 4 — Tests

`(GitSuite) TestGitLog` in `core/integration/git_test.go`, modeled on
`TestGitCommonAncestor` (~line 1347: container-built repo with known commits, `.AsGit()`):

- Order + inclusivity: linear A→B→C; `head.log()` = [C, B, A]; metadata round-trips
  (message, author, dates, parentShas) — exercises prefill.
- `limit`: newest N; `limit: 0` errors.
- `paths`: commits touching only a subdir filter correctly.
- `base`: `branch.log(base: master)` = branch-side only; `base == self` → empty;
  cross-repo base via second `.AsGit()` (exercises refJoin path).
- Remote repo smoke test (reuse the pinned remote from `TestGitCommit` ~line 272);
  confirms depth-0 full fetch on remote backends.
- Workspace path: one case in `core/integration/workspace_api_test.go` driving
  `workspace.git.head.log` against a host checkout.
- Element ID reload: `load(gitCommitID)` on a log element in a fresh session.

No new unit tests (parseGitCommitMetadata unchanged, covered by `core/git_commit_test.go`).
Use the `dagger-dev-tests` skill for running integration tests.

## Phase 5 — Changelog

Changie fragment `.changes/unreleased/Added-*.yaml`: "Add `GitRef.log` to list commits
reachable from a ref, with limit, path, and base-exclusion filters."

## Behavior notes (document in docstrings/changelog, don't code around)

- Remote-backed refs do a FULL fetch for log — same tradeoff `commonAncestor` made.
  Local (workspace) repos unaffected.
- Shallow local checkout (CI): rev-list silently truncates at the shallow boundary; inherited.
- Disjoint `base` (no common ancestor) is not an error — returns up to `limit` from ref side.

## Commit sequence (use the `committing` skill for messages)

1. `core: extract shared ref mount helper` (MergeBase refactor, no behavior change)
2. `core: add GitRef log` (core + schema + tests + changelog)
3. `re-generate`
