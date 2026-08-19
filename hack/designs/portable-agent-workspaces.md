# Portable Agent Workspaces

Internal design for making an agent workspace reconstructible from its trace on
a cold engine, after the originating engine, its DagQL and filesystem caches,
and the host checkout are gone.

Builds on [host-git-reconstruction.md](host-git-reconstruction.md),
[shared-host-git-mirrors.md](shared-host-git-mirrors.md), and
[resume-from-trace.md](resume-from-trace.md).

Status: **implementation in progress**. Part I describes the end state; Part II
tracks the transition from the current implementation. Phases 1 through 3 are
complete; phase 4 is next.

---

# Part I — End state

## 1. Required outcome

Trace restore currently reconstructs a conversation recipe, not the filesystem
that the conversation observed. `LLM.PortableRecipe` retains the final
Workspace binding, but a host-backed binding is still rooted in
`currentWorkspace` and client-routed host reads. Restoring it can therefore read
a different checkout, and a running agent can observe host edits through paths
it has not overlaid.

Before an agent starts, its Workspace binding must become independent of live
client state. For the case that actually needs new source bytes—a client-local
Git checkout—that means capturing:

```text
R  latest ancestor of local HEAD proven present on a remote
 + commits after R that have not been pushed
 + approved tracked dirt and nonignored untracked files
 = frozen initial workspace
```

The conversion is a public operation, `Workspace.checkpoint`: ergonomic sugar
whose *output recipe is a composition of public core calls*. There is no
internal constructor, manifest, or Workspace-specific payload value. The
checkpoint call itself is effectful and never appears in any recipe; the value
it returns is the result of an ordinary chain of `git`, `GitBundle`, `File`,
`Directory`, `Changeset`, and `Workspace` fields that any trace consumer can
read, replay, or even assemble by hand.

Agent overlays and `Workspace.withCommit` calls then build on the frozen value.
The trace recipe must reconstruct the same effective tree, captured local
history, and later engine-pending history from its own call payloads plus the
remote containing `R`. Persisted result records, Dagger snapshots, Git mirrors,
and any other state on the originating engine are cache accelerators only;
deleting all of them must not change correctness.

Settled constraints:

- use Dagger's core Git, File, Directory, Changeset, Workspace, DagQL call-data,
  and snapshot primitives; do not add a workspace storage service;
- the frozen workspace's recipe consists solely of public core calls: no
  internal constructor may appear in it, and no fact needed for reconstruction
  may live outside it;
- `Workspace.checkpoint` is total over workspace sources: replayable values are
  returned as-is (or pinned/normalized), eligible client-local Git is captured,
  everything else is rejected with the offending recipe leaf named in the error;
- capture policy is expressed as scalar arguments; interactive approval is a
  CLI/session concern, never a core prompt;
- never push a branch, hidden ref, or object as a side effect of checkpoint,
  agent startup, restore, or compaction;
- preserve local commits and approved dirty/nonignored-untracked state in one
  embedded standard Git bundle rooted at a remote prerequisite;
- do not promise stable commit SHAs before publication; capture, replay, or
  reconciliation may rewrite local commits;
- exclude initially ignored, untracked host content rather than lazily reading
  it later;
- reject or explicitly omit state that cannot be captured safely; never create
  a restore that silently depends on the source engine or checkout;
- keep capture and restore proportional to local state so large monorepos are a
  supported case;
- public bundle ingestion needs cheap resource safeguards (size caps, object
  and ref count caps, timeouts), not an adversarial-hardening program; and
- defer an additional, explicit engine-view pin. Recorded calls continue to use
  the normal DagQL per-call view and compatibility machinery.

## 2. Portable Git model

Capture records four Git states:

```text
R  remotely reconstructible base commit
L  logical local HEAD, including commits not yet on the selected remote
W  approved effective worktree relative to L
S  synthetic commit whose parent is L and whose tree is W
```

Capture creates `S` in a temporary object database and index; it does not add a
commit, object, or ref to the user's repository. The temporary index starts
from `L` and stages exactly the approved tracked dirt and ordinary untracked
files. Initially ignored content and every rejected candidate are therefore
absent from `S`.

The portable Git payload is one version-3 Git bundle:

```text
prerequisite: R
refs: refs/dagger/checkpoint/head     -> L
      refs/dagger/checkpoint/worktree -> S   (present only when dirt was approved)
objects: commits/trees/blobs needed for R..L and S, excluding R's reachable graph
```

The bundle header supplies the prerequisites and object-format capability; the
commit objects supply the complete history and metadata. The ref names carry
no reconstruction semantics: the frozen recipe references `L` and `S` by SHA,
which Git content addressing makes self-verifying, and the refs exist only so
`git fetch` has something to advertise. Reconstruction semantics are versioned
the way every core field is versioned—by schema view—not by a private ref name.

`S` never needs hiding, because it is never *in* anything. The recipe builds
the Workspace from `L` and uses `S` only as an input to a `Changeset`
computation (§3.3). There is no ref to delete after import, no derivation of
`L` from `S`'s parent, and no possibility of `S` appearing in
`Workspace.git.stagedCommits`, logs, save plans, or pushes: the Workspace value
simply does not contain it. `Workspace.git.head` reports `L`; captured commits
are ordinary baseline history; `Workspace.git.uncommitted` reports `L..W`.

Restore imports the bundle with ordinary `git fetch` semantics, so restore has
no structural topology requirement. Phase 1 still *captures* only linear
`R..L`—merge commits are rejected at capture time—but lifting that is a
capture-side policy change, not a format or restore change.

Using one bundle does not weaken selection. The security boundary is the
temporary index used to make `S`, not whether selected objects occupy a
separate file. Capture constructs and verifies the bundle only after preflight
approval and re-hashing. It must enumerate the bundle's reachable objects
before sending bytes and prove that they are exactly the allowed local-history
closure plus the selected snapshot closure; a normal `git bundle create --all`
is forbidden.

### 2.1 Bundle versus raw pack and `format-patch`

A bare pack is compact, but it has no advertised-ref/prerequisite envelope. We
would have to invent side metadata naming `R`, `L`, `S`, and the object format,
then keep that metadata synchronized with the pack. A version-3 Git bundle is
the standard Git container for that information and is verified/imported with
`git bundle verify` and `git fetch`.

`git format-patch --binary` does not simplify the portable-trace problem: patch
files still need inline, opaque, size-bounded recipe carriage, expand binary
changes textually, replay deltas one commit at a time, lose commit-object
fidelity (committer identity/date, signatures, encoding, topology), and still
need a separate worktree representation. The bundle transfers exact objects
with delta compression and connectivity verification against `R`, and lets one
synthetic commit carry `W`.

Phase 1 happens to preserve local commit SHAs when importing the same objects
over exact `R`, but stable pre-publication SHAs remain outside the API
contract: later filtering, compaction, or save reconciliation may rewrite local
history while preserving its user-visible metadata and origin.

A further payoff of the standard container: the bundle File in a trace is
usable with stock Git. Extract it, `git clone <remote> && git fetch
<bundle-file> 'refs/*:refs/dagger/*'`, and the exact captured state is in an
ordinary repository with no Dagger involved.

## 3. Public API

### 3.1 Git bundle transport

The engine already speaks Git bundle internally in three places (host checkout
capture, staged-commit save, checkpoint import). This design names that
existing mechanic:

```graphql
"A Git bundle: a self-describing container of refs and the objects needed to
reconstruct them, optionally rooted at prerequisite commits."
type GitBundle {
  "Bundle format version (2 or 3)."
  version: Int!
  "Object format capability: sha1 or sha256."
  objectFormat: String!
  "Refs advertised by the bundle and the commits they resolve to."
  refs: [GitBundleRef!]!
  "Commits that must already exist wherever this bundle is applied."
  prerequisiteSHAs: [String!]!
  "Validate the bundle structure available from its bytes. For a
   prerequisite-free bundle this includes full Git connectivity. For a bundle
   with prerequisites, final connectivity is verified by withBundle after it
   fetches the required objects. Errors if the available structure is malformed."
  validate: GitBundle!
  "The bundle bytes as a File."
  asFile: File!
}

type GitBundleRef {
  name: String!
  sha: String!
}

extend type File {
  "Interpret this file as a Git bundle."
  asGitBundle: GitBundle!
}

extend type GitRepository {
  "Pack the given refs (and the objects needed to reconstruct them) into a
   bundle. With base, objects reachable from it are omitted and it is recorded
   as a prerequisite."
  bundle(refs: [String!]!, base: ID): GitBundle!         # base expects a GitRef

  "Import a bundle: verify it, require every prerequisite (fetching it from
   the remote by SHA, optionally guided by prerequisiteRef), fetch the
   advertised refs, and return the repository with those objects present."
  withBundle(bundle: ID!, prerequisiteRef: String): GitRepository!
}
```

Semantics:

- All of these are pure functions over engine values: cacheable by call
  identity, persistable, no client involvement. Object-valued arguments are
  the generic `ID` scalar with `@expectedType`, loaded via `node`, per current
  schema conventions.
- `GitRepository.bundle` requires explicit `refs`; there is no bundle-all
  default. A caller who can hold the `GitRepository` can already read its
  bytes, so these fields add convenience, not capability, and need no special
  gating.
- `withBundle` verification is Git's own (`git bundle verify`, fetch
  connectivity) plus bounded ingestion: 128 MiB per bundle, 1,024 combined
  refs/prerequisites, 1,000,000 pack objects, a 4 MiB header with 64 KiB lines,
  and a ten-minute Git-command timeout. These are initial operational limits,
  to be tuned by phase 7 rather than compatibility guarantees. Unknown or
  unsupported v3 capabilities, including filtered bundles, are rejected;
  hostile-input hardening beyond these bounds remains out of scope.
- `asGitBundle` performs a lazy header parse. `validate` performs the strongest
  repository-independent check: bounded header and pack-envelope parsing,
  checksum and object-count verification, plus a real Git import/connectivity
  check when there are no prerequisites. A prerequisite-bearing bundle cannot
  prove final connectivity without the repository that supplies those objects;
  `withBundle` performs that check after fetching every exact prerequisite.
- Follow-up, not phase 1: `GitBundle.asRepository` for prerequisite-free
  bundles, and converging staged-commit save (`WorkspaceStagedCommitsBundle`)
  onto this surface.

These fields stand alone: incremental backup, air-gapped transport, repro
attachments, and mirror seeding compose from them with no checkpoint involved.

### 3.2 `Workspace.checkpoint`

```graphql
extend type Workspace {
  "Return this workspace as a frozen, host-independent value whose recipe is
   replayable from trace data alone. Replayable sources are returned as-is
   (pinned or normalized as needed); an eligible client-local Git checkout is
   captured; anything else fails with the offending recipe leaf named."
  checkpoint(
    include: [String!]
    exclude: [String!]
    maxUntrackedFileBytes: Int
    maxUntrackedTotalBytes: Int
    maxUntrackedFiles: Int
  ): Workspace!
}
```

`checkpoint` is the one effectful entry point. It is `DoNotCache` (it inspects
live client state), and its capture path is owner-gated: only the session
client that owns the checkout may capture it (`clientMetadata.ClientID` must
match the workspace's client), so module code and nested clients cannot reach
host bytes through it—the same client-routing model host access already has.

It is total over workspace sources:

| Workspace source at checkpoint | Behavior | Reason |
| --- | --- | --- |
| `WorkspaceSourceClientLocal` rooted at a Git checkout | Capture once into the bundle of §2 | The only case that needs new source bytes |
| `WorkspaceSourceGitRef` backed by a replayable remote recipe | Pin a mutable ref to its resolved commit; pure-canonicalize to the value-backed form `withCommit` requires | The remote recipe reconstructs the value; no client bytes move |
| `WorkspaceSourceDirectory` whose full recipe has only replayable leaves | Return as-is, with pure normalization only if a mutation API requires it | Re-encoding source bytes would add cost and no portability |
| Overlay on a replayable base whose overlay inputs are replayable | Return as-is after pinning/normalizing the base as needed | The existing recipe already describes the edits |
| Directory/overlay with a client-routed or otherwise non-replayable leaf | Error naming the offending leaf (field, call digest, reason) | An engine snapshot does not make a host recipe portable |
| `WorkspaceSourceRootlessLocal`, client-local non-Git, or Git with no usable `HEAD`/remote prerequisite | Error, actionable message | No truthful reconstruction contract exists |

Totality is what makes the name honest: checkpoint's contract is *portability*,
not capture. It is idempotent—checkpointing an already-frozen workspace is a
pass-through—so callers may invoke it defensively. Phase 1 still rejects a
client-local Git workspace that already carries Dagger pending commits,
mounts, or a sparse overlay: capture runs before the agent creates those
states, and supporting a derived live workspace is follow-up work (§9).

The replayability judgment (which leaves are host-routed, engine-local,
mutable, or session-scoped) is internal machinery shared with trace restore;
it surfaces only as error quality, not as API.

The agent binder becomes an ordinary caller: bind-time freezing is
`currentWorkspace` → `checkpoint()` → module/agent derivation. Nothing the
binder does is privileged beyond being the session client.

### 3.3 The frozen recipe

The capture path's resolver does the client-side work (§5), then **selects the
following public chain and returns its result verbatim**. The effectful
`checkpoint` call never appears in any recipe; the chain is the checkpoint.
This is the same identity discipline the spike already implements: an
effectful, uncacheable field must return the `ObjectResult` of a pure
selection, or the value it mints has no addressable identity.

```text
git(url: <sanitized remote URL>)
  .withBundle(
     bundle: file(<name>, <inline bundle bytes>).asGitBundle,
     prerequisiteRef: <ref hint that proved R>)
  .commit(<L>)
  .asWorkspace(cwd: <cwd>, ...typed workspace metadata)
  .withChanges(                       # omitted when the worktree was clean
     <repo>.commit(<S>).tree
       .changes(from: <repo>.commit(<L>).tree))
```

Every link exists today except `withBundle`/`asGitBundle`: `Query.git`,
`Query.file`, `GitRepository.commit`, `GitRef.tree`, `GitRef.asWorkspace(cwd)`,
`Directory.changes`, and `Workspace.withChanges` are all current public API.
`GitRef.asWorkspace` grows optional typed scalars for the remaining
workspace-only state (config and lockfile paths, selected config environment,
author defaults) unless an existing public Workspace field already carries a
given item.

Properties of this shape:

- **Facts are derived, not declared.** `L` and `S` appear as literal SHAs, but
  content addressing makes them self-verifying: history, metadata, and the
  worktree tree come from the imported Git objects. There is no second
  description to reconcile and no manifest.
- **The recipe is legible.** A trace consumer, `dump-id`, or a human reads an
  ordinary chain of core calls instead of an opaque internal constructor.
- **Every step is independently cacheable and persistable**, and the chain's
  final call digest is the frozen workspace's stable identity (used by save
  routing, §8).
- **A hand-authored chain is legal.** Someone composing these fields directly
  can build states checkpoint would never emit; that is bounded by content
  addressing and Git's own verification, and there is no bespoke invariant
  left for them to violate—only Git-level ones, which `withBundle` checks for
  every caller.

### 3.4 Inline File carriage and opacity

The bundle bytes ride in the recipe as an inline `File` literal. Carrying
arbitrary binary bytes in a recipe is generic infrastructure, not a Workspace
concern:

- `Query.file` (or a blob-safe variant of it) must handle binary contents;
- if a trace backend requires payload segmentation, the call-data layer
  segments and reassembles generic File literals *below* the core schema;
  segment size is never part of any schema signature;
- per-frame and aggregate size limits must be established by an end-to-end
  trace probe, and capture must stream rather than hold raw, encoded, and
  framed copies of a large bundle simultaneously;
- normal presentation—call renderers, grep/search, errors, debug endpoints,
  progress—must render inline binary literals as type/digest/size only. The
  raw call protobuf retains the bytes so `EncodedIDForCallDigest` can
  reconstruct the File. Do not mark the data sensitive (that would redact it
  from the recipe), and do not mistake display opacity for encryption.

## 4. Choosing `R`

The client Git is authoritative for local graph and checkout layout. Capture
queries current advertised refs without updating or pushing any remote. A stale
local remote-tracking ref is not proof that an object is still remote-backed.
Credentials and URL rewrites stay in the normal client/remote Git path;
embedded credentials are removed from the recorded URL.

Candidate remotes are considered in this order:

1. the current branch's configured upstream remote;
2. `remote.pushDefault`;
3. `origin`;
4. remaining explicitly usable fetch remotes.

Across their advertised refs, choose the ancestor of `L` closest to `L` by
parent distance. For phase 1's linear history this is unambiguous; remote order
breaks a tie where the same commit is advertised by several remotes. Record the
remote and ref that proved reachability (the `prerequisiteRef` hint), the exact
prerequisite SHA `R`, and a sanitized fetch URL. Do not choose an older commit
merely because it belongs to a preferred remote.

On restore, `withBundle` requires exact `R`: a normally advanced branch still
contains it as an ancestor, and a missing prerequisite (force-push plus remote
pruning) is a hard error—the bundle is never imported over another base. If
capture cannot prove any remote-backed ancestor, it fails; it does not publish
local objects to manufacture one.

Private remotes require credentials in the restoring session through existing
Git authentication mechanisms. Credentials are never recipe payload.

## 5. Capture and approval

The capture path of `checkpoint` is an effectful client operation that runs
before module/tool derivation and before the agent loop starts:

1. Dispatch on the source matrix (§3.2); only the client-local Git row
   proceeds. Read and revalidate local Git state; find `R`, `L`, and candidate
   worktree paths.
2. Exclude initially ignored, untracked paths using Git's own ignore rules
   (`.gitignore`, `.git/info/exclude`, configured global excludes). A tracked
   file remains tracked even if a later ignore rule matches it.
3. Scan remaining candidates in the client process: path rules, content
   scanning, size/count limits from the `max*` arguments.
4. Present a local summary and obtain policy/user approval. Interactive
   approval is the CLI's prompt loop; noninteractive use must supply an
   explicit `include` policy—absence of arguments is absence of approval, not
   approval of every nonignored file.
5. In a temporary object database and index, start from `L`, stage only
   approved paths, create `S` with parent `L`, and re-hash every selected
   path. If files or Git state changed after review, abort and repeat rather
   than capturing unreviewed bytes.
6. Ask client Git for one version-3 bundle advertising
   `refs/dagger/checkpoint/head -> L` (and `.../worktree -> S` when dirt was
   approved), excluding objects reachable from `R`. Verify its header, object
   format, refs, prerequisite, connectivity, and object closure locally before
   any bytes cross the client boundary.
7. Select the public chain of §3.3 and return its result verbatim. Retain the
   save route (§8). Only then bind and start the agent.

Phase 1 excludes initially Gitignored content unconditionally. It also rejects
special files, changed submodules, nested-repository contents, and unsupported
history rather than approximating them. Sockets, devices, FIFOs, ownership,
ACLs, xattrs, hardlink identity, stashes, reflogs, hooks, notes, replace refs,
credentials, cache mounts, and service state are not captured.

The index's staged-versus-unstaged distinction is not preserved: `S^{tree}` is
the approved uncommitted worktree relative to `L`, surfaced through an ordinary
`Changeset`. It does not interact with the separate Workspace pending-commit
stack.

## 6. Secret boundary

A nonignored untracked file is not thereby safe. `.env`, private keys, cloud
credentials, package-manager credentials, token files, and arbitrary
high-entropy blobs are common examples. Once such bytes become part of the
inline bundle File, they are in the raw trace and cannot be made safe by
changing how the TUI renders them.

Preflight therefore runs in the client process before candidate bytes are sent
to the engine or used as DagQL arguments. It must:

- run entirely client-side, before payload construction;
- classify candidates with path rules and content scanning;
- require explicit approval for flagged content rather than silently
  including it;
- skip content scanning for committed history—committing is already an
  explicit decision to record content in shared history—rather than
  classifying it, since heuristics over ordinary source produce a prompt per
  revision of every file whose text resembles a token, and burying the real
  question in that noise is itself a failure of the boundary; and
- re-hash selected content during synthetic-commit and bundle creation to
  close the review/upload race.

Approval is meaningful only before payload construction. A design that first
ships all ordinary untracked bytes to the engine and scans there is not
sufficient for this boundary.

These mitigations are cumulative defense in depth. The single bundle is safe
only because `S` is built from a temporary index populated from the approved
path set and the resulting reachable-object closure is checked before upload;
one bundle is not permission to run `git add -A` or bundle all refs. Tracked
dirt is scanned too; "tracked" does not mean "safe to upload."

Raw-trace readers can recover every approved source byte. Compression, Git
object hashing, and opaque rendering are not encryption. Trace authorization,
retention, and deletion policy must treat the inline bundle like a source
archive. Cloud's existing **Delete trace** action is last-resort containment,
not a pre-upload control; before relying on it, verify that deletion covers
the complete call-payload closure including generic inline-File frames, not
only the user-visible span index.

Required tests put a canary in an approved file included in `S` and assert:

- it is present in raw call payload reconstruction and a cold recipe replay
  succeeds;
- it is absent from span names, log bodies, progress output, errors, TUI call
  rendering, search/grep output, and debug summaries; and
- a rejected untracked canary never appears in the bundle or any raw call
  payload.

## 7. Reconstruction

Restore is nothing but evaluating the chain of §3.3 on a cold engine. Per
link:

1. `git(url)` resolves the remote; `withBundle` evaluates the inline File
   (enforcing generic frame/aggregate size bounds), verifies the bundle,
   fetches exact `R` from the remote (guided by `prerequisiteRef`), errors if
   `R` is absent, and imports the bundle's refs into the canonical scratch
   repository using the existing snapshot machinery;
2. `commit(L)` selects the imported logical HEAD; a corrupt or truncated
   bundle cannot produce it, by content addressing;
3. `asWorkspace(cwd, ...)` builds the value-backed Directory/overlay workspace
   whose committed baseline ends at `L`, with a normalized index at `L` and
   the typed metadata applied;
4. `commit(S).tree.changes(from: commit(L).tree)` computes the approved
   worktree delta as an ordinary Changeset, and `withChanges` applies it as
   the workspace's uncommitted state.

The result is a Workspace with no client ID, host path, `currentWorkspace`,
`host.directory`, implicit export destination, or mirror-generation
dependency. Workspace reads resolve only against reconstructed snapshots;
existing agent overlays replay on top; later `withCommit` calls continue the
pending stack from `L`. Restore never examines a destination checkout and
never creates conflict markers. A missing remote prerequisite, malformed
bundle, truncated inline File, or non-replayable leaf is a hard and actionable
error surfaced by the specific link that owns it.

Normal evaluation may populate persisted result metadata, the snapshotter,
content store, or `RemoteGitMirror`. Those are disposable products of
evaluating the recipe, never additional restore inputs.

## 8. Save and publication

Checkpoint has no remote write, and the frozen recipe has no host path. In the
originating live session only, the capture path retains the source client and
checkout as an ephemeral export target *outside* the recipe and persisted
payload, so explicit save can reconcile agent commits back to the checkout
that was captured.

That retention is session state keyed by the frozen chain's final call digest,
which the reconstructed Workspace and its derived values already carry as
ordinary recipe identity. It is not a field on the Workspace value: the value
belongs to a pure recipe shared by every session that resolves it, so a target
stored on it would leak into warm cross-session restores. A cold-restored
workspace has no retained route, and no-argument `Workspace.export` must fail
rather than guess a destination.

Saving is a separate, explicit reconciliation with a selected target:

```text
saved live checkout = selected target checkout
                    + captured local history reconciliation
                    + engine pending commits
                    + approved uncommitted changes
```

Existing staged-commit bundle export and moved-HEAD detection are the building
blocks. If the target's HEAD is still at the expected base, commits can
fast-forward; if it advanced, an explicit plan replays commits and reports
conflicts. Replay may rewrite SHAs while preserving metadata and origin.
Uncommitted content is applied only after commit planning. Materializing a new
checkout is the safe phase-1 fallback; in-place transactional reconciliation
is follow-up work.

Pushing is likewise an explicit `Workspace.git.push`-style action after
review. It is never invoked by checkpoint, agent spawn, restore, or
compaction.

## 9. Long sessions and large monorepos

Initial capture must not use today's full `PackCheckout`. The remote supplies
all objects through `R`; client transfer contains only objects needed for
`R..L` and `S`. Git ignore traversal omits large ignored trees, untracked
limits prevent accidental asset/cache uploads, and Git pack compression deltas
repeated/binary content. Bundle creation and inline-File framing stream
through temporary files.

`LLM.PortableRecipe` drops superseded Workspace bindings but carries the final
Workspace recipe verbatim, without collapsing its internal
`withChanges`/`withCommit` ancestry. That is correct once the leaves are
portable, but very long sessions may restore slowly. A later compaction may
encode the current pending graph and effective worktree into a new bundle
rooted at the same remote `R`—invoked deliberately (completed-turn or explicit
publication boundaries), obeying the same approval/secret boundary, never
after every edit: repeated full payloads would defeat trace deduplication.
Measure first; add incremental bundles only if full compaction is too costly.

Measure at minimum:

- local graph, tracked worktree, and approved/skipped untracked bytes;
- compressed bundle size, inline-frame count, and largest call frame;
- client scan/bundle time and cold remote-fetch/import time;
- recipe frame count before and after optional compaction; and
- secret, type, count, and size rejections.

Metrics contain no paths, contents, remote credentials, or bundle bytes.

## 10. Validation

The headline test proves recipe-only recovery:

1. Create a remote-backed client-local repository with several linear unpushed
   commits, tracked text/binary dirt, approved ordinary untracked source, a
   very large ignored tree, and an untracked canary secret that is not
   ignored.
2. Call `checkpoint`, assert the returned workspace's recipe is exactly the
   public chain of §3.3 (no internal fields), then bind an agent, make more
   edits and pending commits, and publish the trace without pushing local
   state.
3. Destroy the originating engine including DagQL persistence, snapshotter,
   content store, and Git mirrors; delete the host checkout.
4. On a fresh engine and machine, restore only from the trace and the original
   remote.
5. Assert exact `R`, bundle-derived local commit order/metadata, logical
   `HEAD = L`, `S` absent from all Workspace-visible state, approved worktree
   `W`, later agent commits/overlays, module compilation, and continued tool
   use.
6. Assert initially ignored content and the rejected canary are absent even
   from raw payloads, the old host is never read, and no remote or host ref
   changed during capture or restore.

Additional coverage:

- every row of the source matrix, including pass-through idempotence
  (`checkpoint` of a frozen workspace), mutable-ref pinning, GitRef-to-value
  normalization, and rejection naming a non-replayable leaf;
- `GitBundle` round trips: `bundle`/`withBundle` over the canonical repo,
  header fields, `validate` on truncated/corrupt input, prerequisite-missing
  and wrong-object-format errors, resource-cap enforcement;
- stock-Git interop: the exported bundle File fetches into a plain clone of
  the remote;
- closest advertised remote-backed ancestor, competing remotes, stale
  remote-tracking refs, branch advancement retaining `R`, force-push/GC
  removing it;
- detached HEAD, unborn HEAD, no remote-backed ancestor, pre-existing local
  Workspace overlay/pending state, merge rejection at capture;
- linked worktrees, submodule checkout roots, separate Git dirs, alternates,
  partial clones, SHA-256/version-3 bundles where supported;
- binary, executable, symlink, deletion, and file/directory replacement dirt;
- clean-worktree capture (no worktree ref, no `withChanges` link);
- ignore sources, nested repositories, changed submodules, special files;
- approval race, untracked size/count limits, tracked-dirt secret refusal,
  committed content passing preflight unquestioned, exact selected-object
  closure for the bundle;
- canary opacity in every normal renderer while raw recipe replay succeeds;
- payload-size behavior and cold restore on a representative large monorepo;
- no-cache restore with no destination checkout; and
- explicit save to unchanged, advanced, dirty, and newly materialized targets.

---

# Part II — Transition from the current state

## 11. What exists now

The tree contains a working spike that proved cold recipe reconstruction with
a different payload format and API (the intermediate single-bundle/sole-ref
redesign was documented but never implemented; the transition below starts
from the code, not from that document):

- **Public:** `Workspace.checkpoint(include, exclude, max*)` — the right
  entry-point shape, kept by this design. Its resolver already implements the
  return-pure-selection-verbatim identity discipline and already builds its
  base by selecting public `git(url)`.
- **Internal API to be replaced:** `_workspaceCheckpointChunk` (chunked
  `DigestedSerializedString` payload carriage), `_workspaceFromGitCheckpoint`
  (internal constructor taking base + JSON manifest + chunk IDs),
  `Directory.__withWorkspaceCheckpointBundle` and
  `Directory.__withWorkspaceCheckpointWorktree` (two-payload import: commit
  bundle plus separate worktree delta), `core.WorkspaceCheckpointChunk` (+ its
  ID in `core/ids.go`), and the versioned
  `core.WorkspaceGitCheckpointManifest`.
- **Mechanics to keep and refactor:** `MaterializeGitCheckpointBundle` and the
  canonical-repository patterns in `core/git_hostdir.go` (they become
  `withBundle`'s implementation); the client-side capture, preflight, and
  approval loop in `engine/session/git/` and the CLI
  (`GitCaptureApprovalError` → prompt → retry); origin retention
  (`RetainWorkspaceCheckpointOrigin`, `engine/server/session_workspaces.go`);
  `ValidateWorkspaceGitCheckpointHistory` (its graph checks survive; its
  manifest inputs do not).
- **Known gaps the spike carries:** telemetry renderers
  (`dagql/dagui/extract.go`, `grep.go`) still display embedded
  `DigestedSerializedString` values; chunk sizing lives in the core schema;
  the worktree delta is a bespoke patch payload rather than Git objects.

## 12. Transition steps

Each step leaves the tree working; the spike keeps functioning until step 4
deletes it.

1. **Generic inline File carriage.** Make `Query.file` (or a blob variant)
   binary-safe; move payload segmentation below the schema into generic
   call-data framing; render inline binary literals as type/digest/size in
   every normal presentation path (`extract.go`, `grep.go`, errors, debug
   endpoints) while raw call protobufs retain the bytes; establish frame and
   aggregate limits with an end-to-end trace probe. This replaces the chunk
   mechanism wholesale and is the only piece other large inline artifacts will
   also want.
2. **Public bundle surface.** Introduce `GitBundle`, `GitBundleRef`,
   `File.asGitBundle`, `GitRepository.bundle`, and `GitRepository.withBundle`,
   implementing import via the existing `MaterializeGitCheckpointBundle` /
   canonical-repo machinery and creation via the canonical repo's own Git.
   Cheap resource safeguards only. Round-trip and stock-Git interop tests.
   This step is independently shippable and useful.
3. **Rework capture output.** Change client-side capture to emit the two-ref
   bundle of §2 (head → `L`, worktree → `S` from the temporary index),
   dropping the separate worktree-delta payload. Extend `GitRef.asWorkspace`
   with the remaining typed metadata scalars. Rewrite the `checkpoint`
   resolver to select the public chain of §3.3 and return it verbatim; key
   origin retention by the chain's final call digest. Move the source-matrix
   dispatch from the agent binder into the resolver so `checkpoint` is total,
   idempotent, and owner-gated; reduce the binder to an ordinary
   `checkpoint()` caller.
4. **Delete the spike surface.** Remove `_workspaceCheckpointChunk`,
   `_workspaceFromGitCheckpoint`, `__withWorkspaceCheckpointBundle`,
   `__withWorkspaceCheckpointWorktree`, `WorkspaceCheckpointChunk` and its ID
   type, `WorkspaceGitCheckpointManifest`, and the manifest halves of
   `ValidateWorkspaceGitCheckpointHistory`. Regenerate SDKs and docs. No
   deprecation window is needed: the spike shipped behind a dev view and the
   checkpoint field's signature does not change.
5. **Validation and scale.** Land the §10 suite (headline cold-restore test
   first), then the monorepo measurements of §9. Compaction and the remaining
   §9 follow-ups stay deferred until measurements demand them.

Step 1 gates step 3 (the chain embeds an inline File) but not step 2
(engine-side bundles never inline client bytes). Steps 2 and 3 together are
the bulk; step 4 is mechanical.

## 13. Code map

- Checkpoint field and resolver: `core/schema/workspace.go`
- Agent binding: `internal/cmd/dagger/agent.go`
- Workspace sources, overlays, persistence, pending state: `core/workspace.go`
- `withCommit` and staged bundle export: `core/schema/workspace_commit.go`,
  `core/workspace_commit.go`
- Host Git discovery/capture/approval: `engine/session/git/git_capture.go`,
  `engine/session/git/git_pack.go`, `engine/session/git/git_worktree.go`
- Canonical host-Git and legacy checkpoint reconstruction: `core/git_hostdir.go`
- Public bundle parsing, validation, creation, and import: `core/git_bundle.go`
- Public bundle schema: `core/schema/git.go`, `core/schema/file.go`
- Remote Git backing and mirrors: `core/git_remote.go`,
  `core/git_remote_mirror.go`
- Inline File values: `core/schema/file.go`, `core/file.go`
- Origin retention: `engine/server/session_workspaces.go`
- Portable LLM recipe flattening: `core/llm.go`
- Call-frame redaction and recipe rebuilding: `dagql/result_call_frame.go`,
  `dagql/call/id.go`
- Call-payload closure emission: `core/dag_call_telemetry.go`
- Opaque literal rendering: `dagql/call/literal.go`,
  `dagql/dagui/extract.go`, `dagql/dagui/grep.go`
- Trace restore: `internal/cmd/dagger/restore.go`

## 14. Implementation checklist

This is the handoff point for the in-progress implementation. Each phase should
leave the tree working and land as its own scoped commit(s).

- [x] **Phase 1 — binary File recipes and call-payload transport.** Commits
  `127fe46` and `9e2d1aa` add the public `Query.blob` API, a base64 GraphQL
  `Bytes` scalar backed by raw byte literals in recipes, opaque byte rendering,
  and a hard cut-over that emits root and transitive call payloads through the
  existing log side channel instead of span attributes. Focused unit tests and
  a live binary-blob GraphQL probe passed; the full integration package was
  blocked by an unrelated pre-existing compile error in
  `core/integration/llm_resume_test.go`.
- [x] **Phase 2 — public Git bundle surface.** Commits `187046f` and
  `484a70b` add `GitBundle`, `GitBundleRef`, `File.asGitBundle`,
  `GitRepository.bundle`, `GitRepository.withBundle`, `GitBundle.validate`, and
  `GitBundle.asFile`. Creation and import use canonical snapshot-backed Git,
  exact prerequisite fetching with an optional ref hint, bounded ingestion,
  and cleanup of transport-only prerequisite refs. Unit coverage, focused
  round trips and failures, and stock-Git interoperability passed. SDK
  generation produced no checked-in diff because the fields are gated after
  `v1.0.0-beta.10`. The full integration package remains blocked by the
  unrelated pre-existing compile error in `core/integration/llm_resume_test.go`.
  Repeated Go module re-downloads during test invocations are a separate P0
  developer-infrastructure follow-up; do not conflate them with bundle behavior.
- [x] **Phase 3 — secure two-ref client capture.** Commits `4712bee`
  through `3bb1d9e` build synthetic `S` in a temporary object database/index,
  emit one verified version-3 bundle advertising `L` and optional `S`, enforce
  exact selected-object closure, bind approval retries and staged objects to the
  reviewed bytes, replace the separate worktree patch stream, and require one
  spoof-safe approval for the complete selected dirty set. Focused Git,
  engine-client, core, schema, and checkpoint reconstruction tests passed;
  `83501c4` also removed the pre-existing integration compile blocker.
- [ ] **Phase 4 — total public checkpoint composition.** Implement the source
  matrix and owner gate, use transitive `NotReplayable` classification, pin
  mutable refs, return replayable values as public compositions, make
  GitRef-backed synthetic workspaces module-bearing, and reduce the agent
  binder to an ordinary `checkpoint()` caller.
- [ ] **Phase 5 — explicit export target and spike deletion.** Add an optional
  `Workspace.export(to: Workspace)` target: local workspaces may still export
  to themselves, while frozen/value workspaces require a target. Make the agent
  CLI pass `currentWorkspace`, remove checkpoint origin retention and the
  private checkpoint ID, delete the chunk/manifest/internal-constructor surface,
  and regenerate affected SDK/schema artifacts.
- [ ] **Phase 6 — cold-restore and security validation.** Land the headline
  fresh-engine/no-checkout restore, source-matrix, bundle failure, canary
  opacity, rejected-byte absence, and no-remote-write coverage.
- [ ] **Phase 7 — scale and transport validation.** Probe representative large
  repositories and the live trace backend where available, then tune limits.
  Compaction remains deferred unless measurements demand it.

Ratified implementation adjustments: a completely clean `L = R` workspace
omits the empty bundle and `withBundle`; the frozen chain uses
`ref(name: L).asWorkspace(cwd: ...)` because `commit(L)` currently returns a
`GitCommit`; workspace-only metadata is expressed through public
`Workspace.withFoo` fields rather than adding it to every `asWorkspace` call
(`cwd` remains); and public bundle ingestion is not intentionally restricted to
checkpoint-generated, single-prerequisite bundles.

Phase 2 further settles that `withBundle` returns a canonical, locally backed
repository while preserving the source repository URL through persisted object
encoding. Refs used only to stage prerequisite objects are deleted before the
result is exposed. The split `validate` behavior in §3.1 is intentional: bytes
alone cannot prove connectivity through omitted prerequisite objects, so
`withBundle` owns that final check.
