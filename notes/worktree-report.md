# Report: git worktrees vs. Dagger — the recurring `.git` pointer-file problem

Status: investigation notes, written to seed a future session.
Context: written from a session running in a **worktree checkout** of this
repo, where `install`-ing a workspace module failed. Root cause identified;
holistic design sketched; nothing implemented yet.

## TL;DR

A checkout created by `git worktree` (or `git submodule`) has no `.git`
*directory* at its root — it has a `.git` **file**, a one-line pointer:

    gitdir: /home/vito/src/dagger/.git/worktrees/llm-workspace

When such a tree is synced into the engine, that target path doesn't exist in
the sandbox. Git runs **repository discovery before parsing subcommand flags**,
so *any* `git` invocation whose cwd is inside the synced tree dies with:

    fatal: not a git repository: (null)

— even commands that need no repo at all (e.g. `git config --global`). This
class of bug has now been fixed piecemeal at least four times in different
places. We want one holistic fix.

## The failure observed this session

Reproduce: in a worktree checkout, load workspace modules (e.g. the `install`
tool re-resolving the workspace, which loads the `dev` module → deps → the Go
SDK module `modules/alpine`). Fails with:

```
failed to call module "alpine" to get functions: call constructor: exit code: 128
cmd: ["git","config","--global","user.email","suraci.alex@gmail.com"]
stderr: fatal: not a git repository: (null)
```

Mechanism (all in `core/sdk/go_sdk.go`):

- `goSDK.moduleDependencyConfigSelectors` → `gitConfigSelectors` (~line 852)
  fetches the client's git config via the session attachable
  (`engineutil.Client.GetGitConfig`) and injects each entry into the codegen
  container by **exec'ing** `git config --global <key> <value>`.
- The container's workdir is the mounted module context
  (`withWorkdir(goSDKUserModContextDirPath/<srcSubpath>)`, ~line 590) — i.e.
  the repo tree, with the dangling worktree `.git` pointer file at its root.
- Git's startup discovery walks up from the workdir, latches onto the broken
  gitfile, and dies before `config --global` executes.

Note the irony: the *value* being written (`user.email`) was itself obtained
safely via the client-side git attachable; only the *injection* exec is broken.

## Inventory of prior point fixes (the pattern)

Each is a defense against the same invariant violation — "a host checkout's
raw git layout leaked somewhere git gets executed":

| Site | Class | Fix applied |
|---|---|---|
| `core/changeset_git.go` (`disableGitRepoDiscovery`) | engine-side `git diff --no-index` near host paths | `GIT_DIR=/dev/null` kills discovery |
| `engine/session/git/git_config.go` (`GetConfig`) | client-side git exec with repo cwd | `cmd.Dir = os.TempDir()` |
| `engine/engineutil/client.go` (~480, `GetGitConfig`) | consuming git info | tolerate `NOT_FOUND` / `CONFIG_RETRIEVAL_FAILED` |
| `core/modulesource_gitfile.go` (`resolveGitPointer` / `FlattenGitPointer`) | materializing a usable repo in a snapshot | load gitdir + commondir from host, flatten into a normal `.git`, strip worktree plumbing, append config overrides |
| `engine/session/git/git_apply.go` (`GetHead`, `ApplyBundle`) | needing git facts/effects on the checkout | delegate to the **client's own git**; commits land via bundle + fast-forward |
| `modules/tui-qa/main.dang` (~135), `modules/contributor/main.dang` (~266) | userland modules | ad-hoc `-c user.email=…` / explicit `git config` in fresh containers |

Related plumbing worth knowing:

- `engine/server/session_workspaces.go`: `workspaceGitIdentity` (~876) reads
  user.name/email once at workspace load via the attachable (hermetic
  commits); `cloneGitTree` (~969) uses `tree(discardGitDir: true)` for remote
  workspaces — i.e. remote snapshots already ship **without** `.git`.
- `core/workspace.go` (~185): `GitAuthorName/Email` recorded on Workspace.
- `core/modfunc.go` (~1129): contextual `GitRepository`/`GitRef` args degrade
  to null on `ErrNoGitContext` (no `.git` at all) but a *broken* pointer is a
  hard error by design.
- Tests: `core/changeset_git_test.go` (`TestCompareDirectories_OldDirIsBrokenWorktree`,
  broken-worktree fixtures), `util/gitutil/glob_test.go` (~237 comment),
  `engine/telemetry/labels_test.go` (worktree label tests,
  `EnableDotGitCommonDir: true` in `engine/telemetry/labels.go`).

Today's failure is a **new sub-class**: an engine-*composed container exec*
that incidentally runs git with cwd inside the poisoned tree. Nothing about
SDK codegen needs the repo; discovery is collateral damage.

## Proposed holistic design (three invariants, not N patches)

Root principle: **the engine never runs `git` against a host checkout's raw
layout — and the raw layout never enters the engine.**

### 1. Engine-composed git invocations must be hermetic

- **Immediate fix for the observed bug:** stop exec'ing `git config` in
  `gitConfigSelectors`. Synthesize the gitconfig file content and inject it
  with `withNewFile` + `GIT_CONFIG_GLOBAL=<path>` env var. Zero git
  invocations → immune to tree content; fewer execs; better cacheability.
  Serialization needs: `[section]` / `[section "subsection"]` (for
  `url.<base>.insteadOf`, split key at first and last dot), value quoting
  (escape `\` and `"`).
- For any remaining spot that must exec repo-less git: promote the
  `changeset_git.go` idiom into a shared helper in `util/gitutil`
  (`GIT_DIR=/dev/null` + `GIT_CEILING_DIRECTORIES`), so the next author finds
  it instead of reinventing it.

### 2. Host git is the only oracle for host checkouts

Already the trajectory (`GetHead`, `GetConfig`, `ApplyBundle`): the client's
git natively understands every layout — worktrees, submodules,
`--separate-git-dir`, sha256, partial clones, future layouts. The engine
should never re-implement layout resolution. Close the gap anywhere the
engine still reads `.git` out of a synced snapshot.

### 3. `Workspace.git` backed by a clone-from-host, not copied `.git` files

Workspace *is* a git-backed concept — lean into it:

- `FlattenGitPointer` works but hand-reimplements git's layout (commondir,
  worktree plumbing, config overrides). It will break on the next variant
  (alternates, mid-repack packed-refs races, …).
- Instead: the **client** produces a `git bundle create` of the relevant refs
  (mirror image of the existing `ApplyBundle` machinery — the infra flows the
  other way already) plus the uncommitted diff; the engine reconstructs a
  **standalone canonical repo by cloning from the bundle**.
- Every consumer (contextual `GitRepository` args, `Workspace.git`, agent
  `commit`) then sees a normal repo, byte-identical regardless of host layout.
  Bonus: snapshots become deterministic across "same commit as clone vs.
  worktree" — a caching win.
- Sync-time invariant: a `.git` gitfile at a synced root is **neutralized**
  (dropped, as `discardGitDir: true` already does for remote clones) rather
  than shipped as a landmine. Git-ness is provided via the API, never a raw
  pointer file.

### Guardrail

A shared "run this from a worktree checkout" test harness (generalize the
fixtures in `core/changeset_git_test.go`) so the whole suite exercises the
layout that keeps biting us, and new features get coverage by default.

## Suggested next steps (in order)

1. Implement the `gitConfigSelectors` fix (file synthesis + unit test for the
   gitconfig serialization). Verify `install` works from a worktree checkout.
   This is small, self-contained, and unblocks worktree users immediately.
2. Add the `util/gitutil` "no-repo git" helper; sweep for other engine-run
   git execs (grep `"git", "config"` / withExec git patterns in core/).
3. Audit remaining snapshot-`.git` readers; migrate to the session git
   attachable.
4. Design doc (per `dagger-design-proposals` conventions) for the
   bundle-based clone-from-host `Workspace.git`; covers contextual git args,
   `Workspace.git`, and the sync-time `.git`-gitfile neutralization invariant.
5. Worktree test harness + CI coverage.

## Session breadcrumbs

- The failing traceparent from this session:
  `00b1e6f74760a0edfadccc02931d331f-c129f94f4c05af38` (alpine module
  constructor, `git config --global user.email`).
- Unrelated work also pending in this workspace: a new `modules/history`
  Dang module (a `log` tool over `Workspace.git.stagedCommits`) — written but
  never loaded, because `install`/workspace re-resolution is what trips the
  worktree bug. Loading it is a convenient end-to-end verification once the
  fix lands.
