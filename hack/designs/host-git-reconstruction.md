# Canonical host git: checkout repositories from client packs

As-built design for how the engine obtains the git state of *host* checkouts
— workspaces and local module contexts. The engine never interprets a host
checkout's raw `.git` layout: the client's own git packs the repository and
the engine reconstructs a canonical one from the pack. This supersedes the
pointer-file "flattening" machinery that grew around the recurring worktree
`.git`-pointer bug class.

## 1. The problem: raw host layout inside the engine

A checkout created by `git worktree` or `git submodule` has no `.git`
*directory* at its root — it has a `.git` **file**, a one-line pointer:

    gitdir: /home/user/src/proj/.git/worktrees/feature

Syncing such a tree into the engine produced two distinct failure classes:

1. **The landmine.** The pointer's target is a host path, so inside the
   engine it is dangling *by construction*. Git runs repository discovery
   before parsing subcommand flags, so *any* git invocation whose cwd is
   near the synced tree died with `fatal: not a git repository: (null)` —
   even commands that need no repo, like the Go SDK codegen container's
   `git config --global user.email <val>`. This class was point-fixed at
   least four times in different places before this design.
2. **The interpreter.** To make `Workspace.git` and contextual
   `GitRepository` args work from such checkouts, `FlattenGitPointer`
   hand-reimplemented git's on-disk layout: gitfile parsing, `commondir`
   chasing, layering per-worktree state over shared state, stripping
   `worktrees/`/`gitdir`/`locked`, and appending `core.bare`/`core.worktree`
   config overrides. It was already wrong for layouts that exist today
   (`objects/info/alternates` holds host-absolute paths → silently missing
   objects; partial clones copy promisor config the engine can't honor), and
   it required a boundary-escaping primitive — "load any absolute host path,
   `noCache`" — guarded by a hand-rolled "does this look like a gitdir"
   check against crafted pointers smuggling host paths into snapshots.

## 2. The invariant

> The engine never interprets a host checkout's raw git layout, and a
> dangling `.git` pointer file never enters a synced context.

Host git was already the oracle for checkout *config* (`GetConfig`). This
design makes it the oracle for repository *reads* as well: the client
packs, the engine clones. The client hands over exactly two forms of
checkout state — refs+objects (a bundle) and a binary patch of the
uncommitted working tree (§6) — and every `.git` the engine reads is one
it built itself.

## 3. Session RPCs: `CheckoutState`, `PackCheckout`, `PackUncommitted`

Three additions to the Git session attachable (`engine/session/git/git.proto`;
handlers in `git_pack.go` and `git_uncommitted.go`):

- **`CheckoutState(checkout_path) → state_digest`** — sha256 over (HEAD SHA,
  symbolic HEAD, object format, all branch+tag refs). A cheap probe whose
  result changes exactly when the refs move; it is the cache key that keeps
  packing off the hot path.
- **`PackCheckout(checkout_path, expected_state_digest) →
  stream(metadata, chunks)`** — one metadata message (`head_sha`, `head_ref`,
  `object_format`, `state_digest`), then the bytes of `git bundle create <tmp>
  --branches --tags HEAD` in 1MiB chunks. The client rejects a stale expected
  digest or refs that move during bundle creation, and the engine retries under
  a fresh cache key. A repository with no commits yet (unborn HEAD) returns
  metadata carrying only the branch name and state digest, with no bundle.
- **`PackUncommitted(checkout_path, expected_head_sha) →
  stream(metadata, chunks)`** — the checkout's uncommitted changes
  (tracked modifications plus ordinary untracked files; ignored files and
  untracked nested repositories stay out) as one binary git patch against
  the pinned HEAD. §6 covers the fast path built on it.

Because the *client's* git resolves everything, every layout works natively:
worktrees, submodules, `--separate-git-dir`, sha256 repos, alternates and
partial clones (missing objects are materialized client-side, where the
user's credentials and network live), and whatever git ships next.

Error taxonomy, chosen to preserve the pre-existing degrade contract:

| Condition | Result | Engine behavior |
|---|---|---|
| no `.git` entry at checkout root | `NOT_A_REPO` | `ErrNoGitContext`: contextual args resolve null, `Workspace.git` fails plainly |
| `.git` present but unusable (dead pointer, corrupt) | hard error | fails loudly — a broken environment is not silently degraded |
| no git binary / client predates the RPCs (`caller.Supports`) | `ErrGitPackUnsupported` | fall back to the tree as synced: a plain `.git` dir keeps working as before |

Repo-ness is defined by the `.git` entry at the checkout's *own root* — no
walking up — matching how module contexts and workspaces have always defined
their git-ness.

## 4. Reconstruction: `Host.__gitDir` + `MaterializeGitCheckoutPack`

`Host.__gitDir(path, stateDigest)` (internal-only, `PerClientInput`;
`core/schema/host.go`) runs `PackCheckout` and rebuilds a standalone git
directory in a scratch snapshot (`core/git_hostdir.go`):

1. `git init -q --initial-branch=main [--object-format=…]`
2. `git symbolic-ref HEAD <head_ref>` (unborn: this is the whole repo)
3. `git fetch --no-tags --update-head-ok <bundle> '+refs/*:refs/*'` — the
   fetch's connectivity check makes a torn or truncated pack fail *here*,
   loudly, instead of surfacing later as a subtly broken repository.
   `--update-head-ok` because the symbolic HEAD is set before the fetch and
   this scratch repo has no work tree the checked-out-branch guard protects.
4. detached HEAD: `git update-ref --no-deref HEAD <head_sha>`
5. `git read-tree HEAD` — the index is derived state, rebuilt stat-zeroed
   so identical ref states reconstruct identical bytes
6. `git pack-refs --all`; strip `logs/`, `hooks/`, `branches/`,
   `description`, `FETCH_HEAD`, `COMMIT_EDITMSG` (the same normalization
   the public bundle import applies)

The result is byte-identical for a given ref state **regardless of host
layout** — a worktree and a plain clone at the same commit converge — and
carries nothing host-specific (no hooks, no reflogs, no stat caches).
`stateDigest` is deliberately unused in the resolver body: it is a pure
dagql cache key, so the reconstruction is reused until the refs move.

Consumers compose rather than interpret
(`core.MaterializeHostGitCheckout`): `tree.without(".git") +
withDirectory(".git", host.__gitDir(...))`, then hand the composed tree to
`LocalGitRepository` exactly as before. Downstream (`head`, `uncommitted`,
`Cleaned`) is unchanged.

## 5. Cache keying: live vs. epoch-pinned

Two callers, two keying policies:

- **Module contexts** (`ModuleSource.LoadContextGit`) pass an empty cache
  key → the live `CheckoutState` digest. A context is resolved fresh per
  load; its git view should track the checkout.
- **Workspaces** (`materializeWorkspaceGit`, `core/schema/workspace.go`)
  pin the key to the workspace **read epoch** (`"epoch:"+N`). A checkout
  that advances mid-session must *not* be silently re-read: everything a
  session derives from the workspace assumes one coherent view of the
  checkout. The epoch bumps on export/reload — the same scoping
  `Workspace.file` / `.directory` host reads already use. `CheckoutState` still runs per
  materialization (it is also the repo-ness probe); only the *cache slot*
  is pinned.

## 6. Fast path: HEAD checkout + uncommitted patch

`Workspace.git` on a host-backed workspace does not sync the checkout's
directory at all. `materializeWorkspaceGitUncommitted`
(`core/schema/workspace.go`) builds the repository view from packs alone:
reconstruct the canonical `.git` (§4, epoch-keyed per §5), check out
HEAD's tree from it engine-side, then apply the checkout's uncommitted
changes via `Directory.__withGitUncommitted(checkoutPath,
expectedHeadSHA)` (internal-only), which runs `PackUncommitted` and
applies the streamed patch to the clean checkout
(`core.MaterializeGitUncommittedPack`). Untracked nested repositories
arrive as boundary markers, not contents.

`expected_head_sha` couples the two RPCs: a checkout whose HEAD moves
between them reports `HEAD_MISMATCH` instead of producing a patch against
the wrong base. Unborn checkouts, changed submodules, and clients that
predate the RPC degrade (`ErrGitUncommittedUnsupported`) to the
full-directory path: sync the rootfs and swap in the canonical `.git`
(§4). Keeping the patch application behind a DAG field gives the result a
call identity without the patch bytes in it; its cache slot rides the
epoch-pinned chain from §5.

## 7. Neutralization: pointer files never enter contexts

`core.DropRootGitPointerFile` removes a `.git` **regular file** at a synced
snapshot's root (directories untouched). Applied at:

- local module context loads (`loadModuleSourceContext`,
  `loadContextFromSource` — the latter only when the loaded path *is* the
  context root)
- workspace rootfs resolution (`resolveRootfs`, host-backed workspaces,
  root path only) — covering every route to a root read (host, overlay,
  references, mounts)

Deliberately **not** applied at:

- `Host.directory` — the generic sync API keeps its explicit
  include/exclude contract. (Auto-detecting a checkout and promoting it to
  a git-aware sync is attractive future work, but opt-in.)
- nested `.git` files below the root — a submodule checkout inside a synced
  parent tree has an *in-tree* gitdir target and is valid content.
- plain `.git` directories — modules that read a raw `.git` dir from a
  plain-clone context keep working.

The proof obligation for this half: the Go SDK codegen's `git config
--global` exec is **byte-for-byte unchanged** and now simply works, because
the mounted context no longer contains the landmine. (The earlier plan to
rewrite it to synthesize a config file was rejected as a workaround for the
very thing being fixed.)

## 8. Why not the alternatives

- **Keep flatten, ask host git for the paths** (`rev-parse
  --absolute-git-dir --git-common-dir` + `Host.directory` the results):
  deletes the pointer *parsing* but keeps the fragile parts — worktree
  plumbing stripping, config rewriting, the smuggling guard, torn
  file-by-file syncs of a live `.git`, alternates/partial-clone breakage,
  and the out-of-boundary host read. Minimum viable deletion; the model it
  keeps is what kept biting.
- **Sync the `.git` dir and use it directly** (status quo for plain
  checkouts, pre-design): file-by-file sync of a live repo has no
  coordination with host git — a concurrent commit/`gc` can produce a
  snapshot whose refs point into a replaced pack. And a raw `.git` never
  cache-converges (reflogs grow, index stat-cache rewrites on every host
  `git status`, gc shuffles loose↔packed), so equal commits are never
  cache-equal. The pack is created under git's own locking and validated on
  fetch.
- **Perf, honestly:** vs. incremental `.git` filesync, the bundle wins on
  *caching* (re-materialize only when refs move — the old flatten path was
  `noCache` per evaluation — and never ships loose garbage/reflogs) and
  loses on *worst case* (a ref move re-packs and re-ships full history where
  the mirror sync would upload one loose object). §9 is the planned answer;
  correctness and deleting the layout model are the justification, not
  speed.

## 9. Follow-up: thin packs

Not yet implemented. The v1 shapes leave room for incremental transfer:

- extend `PackCheckoutRequest` with `have_shas` — the ref tips of the
  engine's previous reconstruction for that path; the client packs
  `git bundle create --not <haves> --branches --tags HEAD` (bundles support
  prerequisites natively), so unchanged history never re-ships.
- the engine seeds the new snapshot from the previous reconstruction
  (`cache.New(ctx, prevRef)` is copy-on-write) and fetches the thin bundle
  into it; packs accumulate per state change, bounded by an occasional
  `git repack -ad`.
- the previous reconstruction is just a cache entry: if GC evicted it, the
  bundle's prerequisite check fails loudly and the engine retries with a
  full pack. Worst case is exactly v1.
- trade: incremental materialization yields "same refs, equivalent objects"
  rather than byte-identical snapshots — determinism drops from
  content-level to ref-level, which is the level the dagql cache keys on
  anyway.

## 10. Deleted vs. retained

Deleted, as proof the workarounds are no longer needed:

- `core/modulesource_gitfile.go` — `FlattenGitPointer`,
  `resolveGitPointer`, gitfile/commondir parsing, worktree-plumbing
  stripping, config overrides (~270 lines)
- `flattenWorkspaceGitPointer`, `loadWorkspaceHostDir` — including the
  boundary-escaping absolute-host-path read

Retained, because they defend something else:

- `disableGitRepoDiscovery` (`core/changeset_git.go`) — hermetic-exec idiom
  for engine-side `git diff --no-index` over *arbitrary engine-built trees*
  (a `Directory.withNewFile(".git", "gitdir: /x")` is still constructible);
  this is about tree content, not host-layout leakage.
- `GetConfig`'s temp-dir cwd (`engine/session/git/git_config.go`) —
  client-side hermetic exec by design.
- tolerant `GetGitConfig` error handling (`engine/engineutil/client.go`) —
  graceful degradation for git-less hosts.

## 11. Pointers

- RPCs: `engine/session/git/git.proto`, `git_pack.go`, `git_uncommitted.go`
  (and their `_test.go`s); proxy methods in `git.go`; wrappers
  `GitCheckoutState` / `PackGitCheckout` / `PackGitUncommitted` +
  `ErrGitPackUnsupported` / `ErrGitUncommittedUnsupported` in
  `engine/engineutil/client.go`
- Reconstruction: `core/git_hostdir.go` (`MaterializeHostGitCheckout`,
  `MaterializeGitCheckoutPack`, `MaterializeGitUncommittedPack`,
  `DropRootGitPointerFile`, `ErrNoGitContext`); `core/schema/host.go`
  (`__gitDir`); `core/schema/directory.go` (`__withGitUncommitted`)
- Consumers: `core/schema/workspace.go` (`materializeWorkspaceGit`,
  `materializeWorkspaceGitUncommitted`, `resolveRootfs`),
  `core/modulesource.go` (`LoadContextGit`, `loadContextFromSource`),
  `core/schema/modulesource.go` (`loadModuleSourceContext`)
- Tests: `TestModuleWorktreeGoSDK` (the headline regression: Go module at a
  worktree root loads), `TestWorkspaceWorktreeTreeNeutralized`,
  `TestWorkspaceWorktreePlainParity`
  (`core/integration/module_path_inputs_test.go`, `workspace_test.go`);
  `TestContextGit*` now runs through the new path;
  `TestWorkspaceGitUncommittedUsesPackedHostDelta`
  (`workspace_git_delta_test.go`) covers the fast path
