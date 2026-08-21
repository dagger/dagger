# Agent Git branches and managed worktrees

Draft design for preserving arbitrary Git work performed by an agent without
replaying it into the user's current checkout.

Builds on [portable-agent-workspaces.md](portable-agent-workspaces.md),
[sandboxes.md](sandboxes.md), and [async-agents.md](async-agents.md).

Status: **draft**. This records a direction for further iteration; API names,
branch naming, cleanup policy, and the exact client transaction remain open.

## 1. Summary

Agents should be allowed to use ordinary Git inside a sandbox: commit, amend,
merge, rebase, cherry-pick, reset, and resolve conflicts with stock tools. The
sandbox remains the only stateful execution environment. Once the agent is
satisfied, its final repository is normalized into an immutable, compact
`Workspace` backed by a standard `GitBundle` plus a final-head-relative
`Changeset`.

Saving that result must not implicitly replay it onto a moving or dirty user
checkout. The default Ctrl+S behavior instead imports the exact Git objects into
the user's repository, updates an agent-owned local branch, and materializes the
agent's remaining uncommitted state in a Dagger-managed linked worktree. The
user's current branch, index, worktree, and in-progress Git operation remain
untouched. The user integrates the result with normal Git when ready.

The model is:

```text
agent Workspace
  -> arbitrary Git commands in sandbox
  -> compact Workspace (exact HEAD H + effective worktree W)
  -> GitBundle carrying H and synthetic worktree commit S
  -> local agent branch at H
  -> managed worktree containing W
  -> user merge/cherry-pick/rebase when desired
```

Remote publication is a separate explicit operation on `GitRef`. A proposed
`GitRef.push` pushes an immutable source ref to an authenticated remote
`GitRepository`, using fast-forward rules by default and force-with-lease when
an expected remote object ID is supplied. Checkpointing, agent save, and
worktree management never push implicitly.

## 2. Problem

Today Ctrl+S saves an agent's pending commits into the user's checkout. If the
local `HEAD` moved since the commits were staged, the client prepares a replay
of the staged range onto the new `HEAD`, approximately like a sequence of
cherry-picks. This preserves concurrent local progress when the replay is
clean, but the interaction is weak when it conflicts:

- the user's save action unexpectedly becomes a history rewrite;
- the conflict is prepared away from the checkout, so there is no durable
  conflicted worktree for ordinary Git tools to inspect;
- the failed save leaves the user to infer and reproduce the intended
  integration;
- arbitrary sandbox Git history is not necessarily a bounded linear stack that
  can be replayed correctly;
- merges, resets, amendments, and an already-completed rebase have semantics
  that should not be silently transformed again; and
- applying uncommitted changes after moving the checkout is a second side
  effect, so one half can succeed while the other fails.

At the same time, copying raw `.git` changes from a sandbox as a filesystem
`Changeset` is invalid. Canonical engine Git directories do not match every
client layout: a linked worktree or submodule may use a `.git` pointer, the
actual repository may live elsewhere, and raw metadata contains reflogs,
locks, stat-cache index data, alternate paths, worktree registrations, hooks,
and config that are neither portable nor semantic Workspace state.

The desired design lets Git interpret Git state, uses standard object transport,
and moves integration policy out of immutable `Workspace` values.

## 3. Goals

1. Let an agent run arbitrary Git commands in an existing sandbox session.
2. Preserve exact completed commit objects and topology, including merges,
   amendments, and rebases.
3. Compact the final Workspace so cold trace resume does not replay the sandbox
   command ancestry.
4. Never apply root `.git` as ordinary Workspace file changes.
5. Make Ctrl+S safe when the user's checkout moved, is dirty, or is in the
   middle of another Git operation.
6. Preserve remaining uncommitted agent work without inventing a commit.
7. Give the user an ordinary local branch that can be merged, rebased,
   cherry-picked, diffed, or pushed with normal Git.
8. Keep managed worktrees recoverable and mostly invisible to users who do not
   need them.
9. Support repeated saves by the same agent without overwriting work modified
   outside Dagger.
10. Make remote publication explicit and safe for future autonomous agents.

## 4. Non-goals

- Automatically merge an agent branch into the user's current branch.
- Preserve raw `.git` layout, pack arrangement, locks, reflogs, hooks, config,
  remotes, rerere data, or sequencer scratch files.
- Continue an in-progress sandbox rebase outside the sandbox in the first
  version. Finalization requires settled Git state.
- Preserve staged-versus-unstaged index distinctions in the first version.
  The final index is normalized to `HEAD` and remaining effective content is
  represented as uncommitted worktree state.
- Invent a WIP commit for uncommitted agent changes.
- Atomically update a superproject and nested submodule repositories.
- Push during checkpoint, save, trace restore, or worktree creation.
- Reuse agent names as identity. Agent instance IDs are authoritative.

## 5. State model

Let:

```text
B  immutable Workspace the sandbox began from
E  B's effective Git HEAD
H  final logical HEAD produced in the sandbox
W  final effective worktree produced in the sandbox
S  synthetic transport commit whose parent is H and whose tree is W
A  best common ancestor of E and H, when one exists
```

`H` is ordinary history. `S` is not. It exists only to transport and retain the
remaining worktree tree through Git's object model.

The compact payload is a version-3 bundle:

```text
prerequisite: A, when available
refs:
  refs/dagger/agent/head     -> H
  refs/dagger/agent/worktree -> S, when W differs from H
objects:
  reachable(H, S) - reachable(A)
```

If `E` and `H` have no common ancestor, the bundle is prerequisite-free. If `H`
is already reachable from `E` (for example after a reset backwards), object
transport may be empty or contain only `S`. The semantic object IDs are selected
explicitly after import; transport ref names are not part of the Workspace
contract.

The immutable compact Workspace is reconstructed as:

```text
base repository
  .withBundle(bundle)
  .ref(H)
  .asWorkspace(cwd: B.cwd)
  .withChanges(tree(S).changes(from: tree(H)))
```

This is the same public-composition principle as portable checkpoints. Bundle
bytes are embedded through a root `blob` call so the compact Workspace recipe
does not retain the sandbox `Container.withExec` ancestry.

## 6. Sandbox behavior

### 6.1 Ordinary execution

The existing named sandbox session remains the execution state. The agent may
run normal commands:

```text
sandbox.exec(["git", "fetch", ...], session: "work")
sandbox.exec(["git", "rebase", ...], session: "work")
sandbox.exec(["git", "status", "--short"], session: "work")
```

A conflict is ordinary sandbox state. The agent uses more `exec` calls to edit,
stage, continue, skip, or abort. No rebase-specific module object or Agent type
is introduced.

### 6.2 Finalization

Add:

```graphql
extend type Sandbox {
  """
  Return a compact immutable Workspace representing a sandbox session's final
  Git HEAD and effective worktree.

  Refuses an unmerged index or an in-progress Git sequencer operation.
  """
  workspace(session: String! = ""): Workspace!
}
```

The module retains the exact initial `Workspace` for every session, in addition
to its existing container and before-tree handles. `workspace`:

1. locates and validates the final repository;
2. refuses unmerged index entries and active merge, rebase, cherry-pick,
   revert, or bisect state;
3. derives `H` and `W`, excluding root `.git` from ordinary file state;
4. constructs an unflattened final Workspace from the repository;
5. calls compact checkpointing with the session's initial Workspace as the
   prerequisite source; and
6. returns the compact result, which replaces the agent's bound Workspace
   through ordinary Workspace-return routing.

### 6.3 Content-only extraction

The existing `Sandbox.changes` remains useful for non-Git commands, but it must
never return root `.git` changes. If `.git` changed, it refuses with an
actionable message directing the caller to `workspace(session:)`. Silently
excluding `.git` while returning only files would discard commits, so refusal
is safer than partial success.

## 7. Compact checkpointing

Current checkpointing treats a replayable Directory-backed Workspace as already
portable and returns it unchanged. A sandbox container recipe is replayable but
not compact: cold restore would execute every recorded Git command again.

Extend the existing immutable field rather than adding a new storage service:

```graphql
extend type Workspace {
  checkpoint(
    # Existing policy arguments remain unchanged.
    compact: Boolean = false
    base: ID @expectedType(name: "Workspace")
  ): Workspace!
}
```

With `compact: true`, a Git-backed Directory or overlay Workspace is evaluated
and returned as an inline `blob -> GitBundle -> GitRef -> Workspace` public
composition. `base` supplies a known prerequisite history so the bundle remains
proportional to sandbox-created state. Without a usable prerequisite, the
bundle is self-contained and subject to existing bundle limits.

Compaction is idempotent. A Workspace already rooted in a compact inline bundle
composition is returned unchanged. Compaction never pushes or writes to a host.

All existing immutable Workspace reads and `with*` derivations remain unchanged.
The eventual removal of effectful `Workspace.export` is independent of those
APIs.

## 8. Local save target

### 8.1 Names

Every agent instance gets one stable local branch and managed worktree.
Suggested names:

```text
branch:
  refs/heads/dagger/agent/<sanitized-agent-name>-<short-instance-id>

hidden worktree snapshot ref:
  refs/dagger/agents/<full-instance-id>/worktree

worktree path:
  $XDG_STATE_HOME/dagger/worktrees/<repository-id>/<instance-id>/
```

The human name is cosmetic. The full agent instance ID keys registry state,
paths, leases, and telemetry. The short ID in the branch name prevents name
collisions while keeping the branch readable.

The worktree lives outside the repository root so it never appears as an
untracked nested repository. The branch is a normal `refs/heads` ref so ordinary
Git commands can merge and inspect it. The synthetic worktree commit remains on
a hidden non-head ref and never appears in normal history.

### 8.2 Ctrl+S

Ctrl+S no longer writes into the current checkout. The local CLI:

1. evaluates the agent's final compact Workspace;
2. obtains a bundle for the exact final `H` and optional `S`;
3. imports the bundle into the user's common Git object database under
   transaction refs, without changing the current checkout;
4. validates that imported refs resolve to the advertised object IDs;
5. creates or compare-and-swap updates the agent-owned branch to `H`;
6. creates or recreates the managed worktree at that branch;
7. normalizes its index to `H` and materializes `W` as uncommitted content;
8. updates the hidden worktree snapshot ref to `S`;
9. records the branch, path, head, worktree tree, common Git directory, and
   timestamp in the local registry; and
10. reports integration instructions.

Importing objects before visible ref updates is harmless residue if a later
step fails. The user's checked-out ref, index, worktree, and in-progress Git
state are never touched.

Example result:

```text
Saved agent work:

  branch:    dagger/agent/fix-auth-c08fe768e79e
  worktree:  ~/.local/state/dagger/worktrees/ab12.../c08f.../
  commits:   3
  pending:   2 modified files

Merge committed work with:

  git merge dagger/agent/fix-auth-c08fe768e79e

The remaining uncommitted changes are available in the managed worktree.
```

### 8.3 Why the hidden ref matters

The linked worktree holds ordinary files, but the hidden `S` ref makes its
uncommitted content durable as Git objects:

- Git GC cannot discard blobs reachable only from pending work;
- a missing managed worktree can be reconstructed;
- repeated saves can lease the prior effective tree;
- cleanup can distinguish recorded agent output from outside edits; and
- the synthetic commit never pollutes the agent branch.

## 9. Repeated saves

The same agent instance normally updates the same branch and worktree. The
registry stores the previous branch head and synthetic worktree tree. Before an
update, the CLI verifies:

- the branch still resolves to the recorded previous head;
- the managed worktree is still registered to the same repository and agent;
- its effective tree and index policy still match the previous saved state; and
- no other Git operation is in progress there.

If the checks pass, a non-fast-forward branch update is allowed with a strict
old-object-ID lease. The branch is agent-owned, so this safely supports an agent
that amended, rebased, or reset its own history.

The simplest recoverable update is replacement rather than in-place merge:
retain the old head and `S` refs, remove and recreate a verified unmodified
managed worktree, update the branch by compare-and-swap, materialize the new
`W`, then advance the registry and hidden ref. A crash leaves enough refs to
reconstruct either generation.

If the user modified the branch or worktree, Dagger refuses to overwrite it.
The user may preserve those edits, explicitly discard them, or save the agent as
a new generation such as:

```text
dagger/agent/fix-auth-c08fe768e79e-2
```

No heuristic merge occurs during save.

## 10. User integration

The branch/worktree split makes the result ordinary Git state:

```text
# Inspect commits.
git log --oneline --graph HEAD..dagger/agent/<name-id>

# Review the total branch difference.
git diff HEAD...dagger/agent/<name-id>

# Integrate committed work.
git merge dagger/agent/<name-id>

# Or select individual commits.
git cherry-pick <sha>...

# Inspect and finish uncommitted work.
cd "$(dagger agent worktree <agent-id>)"
git status
git add ...
git commit ...
```

Ctrl+S does not invent a commit for `W`. A later explicit command may commit the
managed worktree, but message, authorship, and scope remain user/agent choices.

A future optional integration command may create another managed worktree from
the user's current `HEAD` and attempt a merge or replay there. If it conflicts,
the durable conflicted worktree remains available for ordinary resolution. This
must remain separate from the exact-save path.

## 11. Worktree management

Managed worktrees are a client-local concern. The engine and immutable
Workspace recipe contain no local path.

The CLI registry is keyed by canonical Git common-directory identity plus agent
instance ID and records at least:

```text
repository identity
agent instance ID and display name
branch ref
managed worktree path
last saved branch head
last saved synthetic worktree object/tree
creation and update timestamps
lifecycle state
```

Rules:

- Create worktrees only beneath Dagger's state directory.
- Place a Dagger ownership marker in every managed directory.
- Use `git worktree add/remove` as the registration authority; never delete a
  registered directory directly.
- Never remove a dirty or externally modified worktree automatically.
- Never delete a user-facing agent branch implicitly.
- A missing clean worktree may be recreated from the branch and hidden `S` ref.
- Prune only entries whose repository identity and ownership marker match.
- A branch merged into a user-selected destination may be offered for cleanup,
  not removed automatically.
- Linked worktree branch exclusivity is respected. Agent-ID branch uniqueness
  avoids collisions with other managed worktrees.

Expected commands include:

```text
dagger agent worktrees
dagger agent worktree <agent>
dagger agent prune
dagger agent discard <agent>
```

`discard` refuses dirty or externally modified state unless the user explicitly
confirms data loss.

## 12. `GitRef.push`

Agents will increasingly need to publish exact immutable Git results without a
client-local checkout. Push belongs on a Git value, not on `Workspace`.

Proposed API delta:

```graphql
extend type GitRef {
  """
  Push this immutable ref to an authenticated remote Git repository.

  Normal Git fast-forward/create rules apply when expectedRemoteHead is null.
  When expectedRemoteHead is set, update destinationRef only if the remote ref
  still resolves to that exact object ID; an empty value requires the remote
  ref to be absent. This provides force-with-lease semantics for rebased or
  amended agent history.
  """
  push(
    to: ID! @expectedType(name: "GitRepository")
    destinationRef: String!
    expectedRemoteHead: String
  ): GitPushResult!
}

type GitPushResult implements Node {
  id: ID!
  destinationRef: String!
  previousObjectID: String!
  objectID: String!
  disposition: GitPushDisposition!
}

enum GitPushDisposition {
  CREATED
  FAST_FORWARD
  LEASED_REPLACEMENT
  ALREADY_PRESENT
}
```

The destination `GitRepository` carries URL, HTTP token/header or SSH socket,
known-host, service-binding, and object-format context. The source `GitRef`
contributes exact reachable objects. Implementation may derive a `GitBundle` or
pack internally; bundle is transport, while the source ref and destination ref
are semantics.

Required behavior:

1. Resolve the source to an immutable object ID before any remote write.
2. Require a fully qualified destination ref initially; branch-name sugar may
   normalize to `refs/heads/...` later.
3. Verify object-format compatibility.
4. Transfer only objects required by the destination update.
5. With no lease, use ordinary non-force push semantics.
6. With a lease, use the equivalent of
   `--force-with-lease=<ref>:<expected-object-id>`.
7. Treat an empty expected value as "the ref must not exist."
8. Return actual before/after IDs and disposition.
9. Never expose credentials in results, errors, telemetry, bundle headers, or
   persisted recipes.
10. Mark the operation effectful/non-cacheable and ensure a reloaded result does
    not accidentally repeat the push. It must not appear in checkpoint or
    compact Workspace recipes.
11. Never run host repository hooks implicitly. Remote server hooks still apply
    normally.
12. Fail rather than silently retarget when the remote moved.

Example:

```text
let origin = git(
  url: "git@github.com:acme/project.git",
  sshAuthSocket: host.sshAuthSocket,
  sshKnownHosts: knownHosts,
)

final.git.head.push(
  to: origin,
  destinationRef: "refs/heads/agent/fix-auth",
  expectedRemoteHead: priorRemoteHead,
)
```

Remote push is explicit. Ctrl+S creates local state only. A separate Publish
action may push the local agent branch or directly push the final immutable
`GitRef`.

## 13. API changes

All existing immutable Workspace reads and `with*` fields remain unchanged.
The proposed additions are:

```graphql
extend type Workspace {
  checkpoint(
    # Existing arguments unchanged.
    compact: Boolean = false
    base: ID @expectedType(name: "Workspace")
  ): Workspace!
}

extend type WorkspaceGit {
  bundle(base: ID @expectedType(name: "GitRef")): GitBundle!
  worktree: Changeset!
}

extend type Sandbox {
  workspace(session: String! = ""): Workspace!
}

extend type GitRef {
  push(
    to: ID! @expectedType(name: "GitRepository")
    destinationRef: String!
    expectedRemoteHead: String
  ): GitPushResult!
}
```

`Sandbox.changes` is tightened to refuse root `.git` drift. Effectful
`Workspace.export` is removed after the CLI no longer depends on it. The current
checkout-oriented `WorkspaceGit.push` is superseded by `GitRef.push`; removal
may follow compatibility policy rather than landing atomically with the new
field.

No `Workspace.sync`, rebase state object, or specialized stateful rebase module
is introduced.

## 14. Client implementation outline

The CLI is already the correct authority for local repository layout, paths,
worktree registration, user prompts, and credentials. Saving uses stock Git
through the client process:

1. Find the canonical common Git directory for the current checkout.
2. Derive a stable repository identity without recording credential-bearing
   URLs.
3. Download/evaluate the final bundle.
4. Validate bundle header, object format, refs, prerequisites, and advertised
   object IDs.
5. Fetch the bundle under transaction refs without tags or ordinary ref
   updates.
6. Lock the per-repository Dagger registry.
7. Verify any prior agent-branch and managed-worktree leases.
8. Create recovery refs for the prior agent generation.
9. Create or replace the agent ref with an object-ID compare-and-swap.
10. Create or recreate the managed linked worktree.
11. Normalize its index to `H`; apply `W`; verify the resulting tree.
12. Advance the hidden `S` ref and registry atomically as far as local
    filesystem semantics permit.
13. Remove transaction/recovery refs only after verification.

The user's current checkout is never the mutation target, so save avoids the
most dangerous multi-file transaction. Imported unreachable objects and
transaction refs are recoverable cleanup residue; user refs and worktrees are
changed only after validation.

## 15. Edge cases and policy

### Detached and unborn sandbox HEAD

A detached final `HEAD` is transported exactly and assigned to the normal agent
branch during save. An unborn repository needs explicit initial support for an
empty tree and branch name; it may be rejected in the first version.

### Merge and rebase results

Completed merges and rebases are ordinary commit graphs and are preserved
exactly. An unmerged index or active sequencer is refused by
`Sandbox.workspace`; the agent continues or aborts it inside the sandbox.

### Index state

V1 normalizes the final index to `H`, leaving `W-H` unstaged. A later extension
may transport a semantic stage-zero index tree to preserve `git add`, soft
reset, and mixed reset distinctions. Raw index bytes are never portable.

### Additional refs and tags

V1 saves only final `HEAD` plus hidden worktree state. Branches and tags created
inside the sandbox but not selected by final `HEAD` are omitted. A future ref
manifest may transport explicit local branch/tag creates, moves, and deletions;
absence from a bundle is never interpreted as deletion.

### Ignored and untracked content

The initial checkpoint retains its approval boundary. Sandbox-created content
already exists in engine execution state, but compaction still enforces bundle
size/object limits and opaque binary rendering. The final worktree selection
policy must be explicit about ignored files, nested repositories, and special
files. V1 should reject unsupported nested Git state rather than copy `.git`
metadata recursively.

### Submodules

A sandbox rooted inside a submodule can be handled as its own repository. A
changed gitlink in a superproject does not imply recursive synchronization of
the nested worktree. Multi-repository orchestration is later work.

### Filters, LFS, and sparse checkout

Managed worktree creation may invoke checkout filters. V1 should define whether
it uses normal local filter configuration or disables networked smudge behavior.
Sparse-checkout configuration is worktree-local and should not be copied as raw
metadata; full checkout is the conservative initial behavior.

### Signatures

Exact sandbox commit objects preserve existing signatures. A subsequent rebase
or amend performed by the agent naturally invalidates and replaces signed
objects according to Git behavior. `GitRef.push` transfers exact source objects
and does not resign them.

## 16. Failure and recovery

- Bundle validation failure creates no visible ref or worktree.
- A branch lease failure preserves both the user branch and prior agent state.
- A modified managed worktree is never overwritten automatically.
- A crash after object import but before ref update leaves only unreachable
  objects or transaction refs.
- A crash after branch update remains recoverable from old/new head refs and
  the hidden synthetic worktree refs.
- A missing managed directory is recreated only when the registry and Git
  worktree metadata prove Dagger ownership.
- Registry disagreement fails closed and reports manual recovery commands.
- Remote push lease failure reports the observed remote object ID and never
  retries with force.

## 17. Verification

### Sandbox and compaction

- `git commit` in a sandbox produces the exact commit SHA in the compact
  Workspace.
- Amend, merge, cherry-pick, rebase, and reset preserve exact final topology.
- Final uncommitted content appears in `WorkspaceGit.worktree`, not history.
- `Sandbox.changes` refuses `.git` drift.
- `Sandbox.workspace` refuses unmerged or active sequencer state.
- The compact recipe contains no sandbox `withExec` calls.
- Cold replay succeeds with the original sandbox/container cache absent.

### Local save

- Saving creates the expected agent branch without moving the user's `HEAD`.
- Dirty, staged, untracked, and in-progress user checkout state is unchanged.
- Remaining agent work appears only in the managed worktree.
- The hidden `S` ref reconstructs a deleted managed directory.
- Repeated fast-forward and leased non-fast-forward agent saves succeed.
- An externally changed agent branch or worktree is refused.
- Two agents with the same display name receive distinct branches/worktrees.
- Linked worktree and submodule-root repositories resolve the correct common
  Git directory.
- Cleanup never removes dirty or non-Dagger-owned paths.

### Push

- Normal create and fast-forward pushes succeed.
- Non-fast-forward without a lease fails.
- A matching lease permits exact replacement.
- A stale lease fails and reports actual remote state.
- An empty expected value creates only when absent.
- HTTP token/header and SSH socket authentication work without credential
  exposure.
- SHA-1/SHA-256 mismatch is rejected.
- Re-evaluating a result does not repeat the push.

## 18. Rollout

1. Tighten `Sandbox.changes` so `.git` can never escape as file content.
2. Add compact checkpointing for replayable Git-backed Workspace values.
3. Add `WorkspaceGit.bundle` and `WorkspaceGit.worktree` projections.
4. Add `Sandbox.workspace` and focused cold-replay coverage.
5. Implement local agent branch naming, bundle import, and registry-only save
   without worktrees; committed results become mergeable first.
6. Add managed worktree materialization and hidden `S` retention for
   uncommitted state.
7. Switch Ctrl+S from current-checkout replay to agent branch/worktree save.
8. Add inspection, cleanup, and recovery CLI commands.
9. Add `GitRef.push` with fast-forward and force-with-lease coverage.
10. Deprecate and later remove `Workspace.export` and checkout-oriented
    `WorkspaceGit.push` after all callers migrate.

## 19. Open questions

1. Should the local branch be named only by instance ID, or include a mutable
   display-name slug?
2. Should agent branches remain indefinitely, or should merged clean branches
   be eligible for opt-in automatic pruning?
3. Should Ctrl+S create the managed worktree eagerly when `W == H`, or only
   when uncommitted content exists or the user asks to inspect it?
4. Should the hidden synthetic worktree ref remain after the managed worktree is
   clean or removed?
5. Should repeated save create generations by default after outside edits, or
   require an explicit flag?
6. Should V1 include ignored sandbox-created files in `W`, and what limits or
   warnings apply?
7. Should `GitRef.push.expectedRemoteHead` use empty-string semantics for
   "absent," or a structured lease input that distinguishes normal push,
   absent-only create, and exact-object replacement?
8. Should remote publication use `GitRef.push(to: GitRepository)` or a target-
   oriented `GitRepository.push(source: GitRef)` spelling?
9. How should push effects be pinned so loading a recorded result can never
   repeat the remote mutation?
10. Should optional integration attempts use merge or replay by default, and
    should conflicts always materialize into a second managed worktree?
