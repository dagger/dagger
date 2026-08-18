# Portable Agent Workspaces

Internal design for making an agent workspace reconstructible from its trace on
a cold engine, after the originating engine, its DagQL and filesystem caches,
and the host checkout are gone.

Builds on [host-git-reconstruction.md](host-git-reconstruction.md),
[shared-host-git-mirrors.md](shared-host-git-mirrors.md), and
[resume-from-trace.md](resume-from-trace.md).

Status: **proposed**.

## 1. Required outcome

Trace restore currently reconstructs a conversation recipe, not the filesystem
that the conversation observed. `LLM.PortableRecipe` retains the final
Workspace binding, but a host-backed binding is still rooted in
`currentWorkspace` and client-routed host reads. Restoring it can therefore read
a different checkout, and a running agent can observe host edits through paths
it has not overlaid.

Before an agent starts, replace that binding with a frozen Git workspace:

```text
R  latest ancestor of local HEAD proven present on a remote
 + commits after R that have not been pushed
 + approved tracked dirt and nonignored untracked files
 = frozen initial workspace
```

Agent overlays and `Workspace.withCommit` calls then build on that value. The
trace recipe must reconstruct the same effective tree, captured local history,
and later engine-pending history from its own call payloads plus the remote
containing `R`. Persisted result records, Dagger snapshots, Git mirrors, and any
other state on the originating engine are cache accelerators only; deleting all
of them must not change correctness.

The design has the following settled constraints:

- use Dagger's core Git, Directory, Changeset, Workspace, DagQL call-data, and
  snapshot primitives; do not add a workspace storage service;
- never push a branch, hidden ref, or object as a side effect of agent startup;
- preserve local commits and approved dirty/nonignored-untracked state in an
  embedded Git prerequisite bundle or pack;
- do not promise stable commit SHAs before publication; capture, replay, or
  reconciliation may rewrite local commits;
- exclude initially ignored, untracked host content rather than lazily reading
  it later;
- reject or explicitly omit state that cannot be captured safely; never create
  a restore that silently depends on the source engine or checkout;
- keep capture and restore proportional to local state so large monorepos are a
  supported case; and
- defer an additional, explicit engine-view pin. Recorded calls continue to use
  the normal DagQL per-call view and compatibility machinery.

## 2. What exists today

The implementation already supplies most of the mechanics, but not a portable
leaf containing the local bytes.

### 2.1 Host Git transport

`engine/session/git/git_pack.go` asks the client's own Git to create a bundle of
`HEAD`, all local branches, and tags. This correctly handles linked worktrees,
submodules, separate Git directories, alternates, and partial clones without the
engine parsing a host `.git` layout. It is not the portable encoding: it sends
the complete reachable graph, depends on the client RPC, and is cached as an
engine snapshot.

`engine/session/git/git_worktree.go` builds a binary patch relative to `HEAD`.
It selects tracked changes with `git diff` and ordinary untracked files with
`git ls-files --others --exclude-standard`; ignored paths are absent, untracked
nested repositories are represented only by boundary markers, and changed
submodules are rejected. This is a useful basis for worktree selection, but it
currently streams every selected untracked file to the engine without size,
secret, or user-approval preflight. Portable capture must not call this path and
then try to filter the result.

`core/git_hostdir.go` reconstructs a canonical repository in a scratch
Dagger snapshot by fetching the client bundle, setting symbolic or detached
`HEAD`, rebuilding the index with `git read-tree HEAD`, packing refs, and
removing hooks, reflogs, and scratch files. The checkpoint importer should reuse
these patterns. A canonical snapshot is still only an evaluation result, not a
portable payload.

### 2.2 Workspace overlays and pending commits

A Workspace has a private source kind. Host overlays deliberately remain sparse:
their changeset carries the touched delta while untouched reads route to the
client. Directory- and Git-backed sources are engine values and do not read
through to a host.

`Workspace.withCommit` stores each pending commit's repository tree and metadata
inside the returned Workspace. `Workspace.git.head` reads the latest pending
repository, and `Workspace.git.uncommitted` reapplies the overlay remainder on
that repository before diffing it. `Workspace.withCommit` currently rejects a
`WorkspaceSourceGitRef`; therefore a restored checkpoint must be a value-backed
Directory/overlay workspace. The remote `GitRef` is an input used to rebuild the
canonical repository, not the Workspace's final source kind.

Captured local commits are already real commits in the user's checkout. They
therefore belong to the checkpoint's immutable source baseline: set the restored
repository's logical `HEAD` to `L`, and make only the approved worktree delta
pending on top. They must not appear in `Workspace.git.stagedCommits` or
`Workspace.changes`, whose save/upload semantics describe commits and changes
that exist only in the engine. Later `Workspace.withCommit` calls continue to use
the ordinary pending stack from the captured `L`.

### 2.3 Recipes, persistence, and trace reconstruction

Directories are backed by Dagger snapshots. Persisted objects retain result
metadata and local snapshot links; for example, `RemoteGitMirror` persists a
mutable snapshot ID. Neither a Directory ID nor a persisted snapshot link puts
filesystem bytes in an exported trace. They only work while the relevant engine
content still exists, unless replaying the Directory's recipe can recreate it
from portable leaves.

Trace restoration instead rebuilds call IDs from `dagger.io/dag.call` payloads.
The call's own span carries one frame, and `recordCallPayloads` emits the rest of
its transitive recipe closure over the log channel. The restore frontend joins
those frames by digest. An engine-local result handle, a missing frame, or a
host-only leaf cannot be repaired from snapshot persistence after the source
engine is gone. Every checkpoint-data call must therefore be a recipe-form call
whose complete payload reaches that closure.

## 3. Portable Git model

Capture records three states:

```text
R  remotely reconstructible base commit
L  logical local HEAD, including commits not yet on the selected remote
W  approved effective worktree relative to L
```

For phase 1, `R..L` is restored as linear committed local history in the
checkpoint's source baseline. Preserving the exact commit graph is ideal but
not required; merge commits are rejected until the checkpoint model either
supports a graph or defines an explicit flattening policy. Messages, authors,
dates, order, and origin matter; topology and SHAs may change.

The portable state has two separately approved payloads:

- a prerequisite Git bundle/pack containing committed objects reachable from
  `L` but not `R`;
- one selected worktree delta from `L` to `W`, containing approved tracked dirt
  and ordinary untracked files but no initially ignored content;
- a manifest with format version, object format, sanitized remote URL, remote
  ref hint, `R`, `L`, the ordered captured-commit metadata, and worktree-delta
  digest; and
- Workspace metadata needed independently of Git: cwd, config and lockfile
  paths, author defaults, and capture-policy version.

Keeping committed history and the worktree delta separate is an important safety
boundary: an untracked file cannot enter the commit bundle accidentally. The
bundle is compact for a large repository because unchanged commits, blobs, and
trees are supplied by `R`; it adds only local committed objects. Restore imports
the commit bundle into the frozen baseline and applies the approved worktree
delta once.

A later compaction may replace the worktree delta with a private snapshot
commit/ref whose tree is `W` and whose parent is `L`. That can be more efficient
for very large or deeply edited worktrees. The private commit remains transport
plumbing: after import its ref is removed, `HEAD` and the index describe `L`, and
its tree only populates the dirty/untracked worktree.

The implementation may preserve the original objects and SHAs when no rewrite
is needed, but that is not an API guarantee. The manifest preserves ordering,
messages, author/committer metadata, and origin provenance needed to rebuild the
pending stack. Filtering a committed path, normalizing unsupported metadata, or
replaying onto a moved target may produce different SHAs. This is acceptable
only while the commits are local mutable state; already-published history is the
remote base, not rewriteable checkpoint state.

## 4. Choosing `R`

The client Git is authoritative for local graph and checkout layout. Capture
queries current advertised refs without updating or pushing any remote. A stale
local remote-tracking ref is not proof that an object is still remote-backed.
Credentials and URL rewrites stay in the normal client/remote Git path; embedded
credentials are removed from the recorded URL.

Candidate remotes are considered in this order:

1. the current branch's configured upstream remote;
2. `remote.pushDefault`;
3. `origin`;
4. remaining explicitly usable fetch remotes.

Across their advertised refs, choose the ancestor of `L` closest to `L` by
parent distance. For phase 1's linear history this is unambiguous; remote order
breaks a tie where the same commit is advertised by several remotes. Record the
remote and ref that proved reachability, the exact prerequisite SHA `R`, and a
sanitized fetch URL. Do not choose an older commit merely because it belongs to
a preferred remote.

The restore recipe fetches the ref hint and verifies that exact `R` is present.
A normally advanced branch still contains `R` as an ancestor. A force-push
followed by remote object pruning can make an old checkpoint unrestorable; the
constructor must report the missing prerequisite rather than applying the pack
to another base. If capture cannot prove any remote-backed ancestor, it fails.
It does not publish local objects to manufacture one.

Private remotes require credentials in the restoring session through existing
Git authentication mechanisms. Credentials are not checkpoint payload.

## 5. Capture and approval

Capture is an effectful client operation that returns a pure Workspace recipe.
It runs before module/tool derivation and before the agent loop starts.

1. Read and revalidate local Git state; find `R`, `L`, and candidate worktree
   paths.
2. Exclude initially ignored, untracked paths using Git's own ignore rules,
   including `.gitignore`, `.git/info/exclude`, and configured global excludes.
   A tracked file remains tracked even if a later ignore rule matches it.
3. Locally classify candidate tracked dirt and ordinary untracked files by path,
   type, size, and secret heuristics. Committed blobs are bounded by type and
   size only; secret heuristics do not apply to them.
4. Present a local summary and obtain policy/user approval. Noninteractive use
   must supply an explicit policy; it must not imply approval of every
   nonignored file.
5. Revalidate the approved paths and content digests while packing. If files
   changed after review, abort and repeat rather than capturing unreviewed
   bytes.
6. Ask client Git to create the prerequisite commit bundle and the selected
   `L..W` worktree delta as separate payloads. Validate bundle headers,
   prerequisites, object format, connectivity, patch paths, and final worktree
   digest locally and again on import.
7. Construct bounded checkpoint-data calls and select the pure Workspace
   constructor from them. Only then bind and start the agent.

Phase 1 excludes initial Gitignored content unconditionally. It also rejects
special files, changed submodules, nested-repository contents, and unsupported
history rather than approximating them. Sockets, devices, FIFOs, ownership,
ACLs, xattrs, hardlink identity, stashes, reflogs, hooks, notes, replace refs,
credentials, cache mounts, and service state are not captured.

The index's staged-versus-unstaged distinction is not preserved: approved dirty
content is an uncommitted worktree relative to `L`. This does not affect the
separate Workspace pending-commit stack.

## 6. Secret boundary

A nonignored untracked file is not thereby safe. `.env`, private keys, cloud
credentials, package-manager credentials, token files, and arbitrary
high-entropy blobs are common examples. Once such bytes become a checkpoint
call argument, they are in the raw trace and cannot be made safe by changing how
the TUI renders them.

Preflight therefore runs in the client process before candidate bytes are sent
to the engine or used as DagQL arguments. It must:

- use path deny/flag rules for common credential locations and names;
- scan contents for private-key blocks and supported token/credential formats;
- enforce per-file, aggregate-byte, and file-count limits for untracked data;
- avoid following symlinks or entering ignored/nested repositories;
- log only counts, sizes, and classifications, never candidate paths or bytes;
- skip suspicious untracked files unless the user explicitly approves each
  one, and fail closed for suspicious tracked dirt unless the user explicitly
  approves or rewrites it;
- leave committed content to the author's own commit decision rather than
  classifying it, since heuristics over ordinary source produce a prompt per
  revision of every file whose text resembles a token, and burying the real
  question in that noise is itself a failure of the boundary; and
- re-hash selected content during pack creation to close the review/pack race.

Approval is meaningful only before payload construction. A design that first
uses today's `PackWorktree`, receives all ordinary untracked bytes in the
engine, and scans there is not sufficient for this boundary.

These mitigations are cumulative defense in depth, not alternatives. In
particular, keeping committed history and untracked worktree data in separate
payloads prevents the commit bundle from sweeping in an untracked secret, while
path rules, content scanning, explicit approval, size limits, opaque rendering,
and trace access control each cover a different failure mode. Tracked dirt is
scanned too; “tracked” does not mean “safe to upload.” Committed content is the
exception, and only because committing is already an explicit decision to record
content in shared history.

Raw-trace readers can recover every approved source byte. Compression, Git
object hashing, and opaque rendering are not encryption. Trace authorization,
retention, and deletion policy must treat a checkpoint like a source archive.
Cloud's existing **Delete trace** action is useful last-resort containment, but
it is not a pre-upload control. Before relying on it, verify that deletion covers
the call-payload closure and its retained checkpoint chunks, not only the
user-visible span index.

## 7. Core API shape

The user-facing operation can remain experimental or internal in phase 1:

```graphql
extend type Workspace {
  """Return a frozen, host-independent Git workspace recipe."""
  checkpoint(options: WorkspaceCheckpointOptions): Workspace!
}

input WorkspaceCheckpointOptions {
  include: [String!]
  exclude: [String!]
  maxUntrackedFileBytes: Int
  maxUntrackedTotalBytes: Int
  maxUntrackedFiles: Int
}
```

The CLI owns interactive approval and passes the resulting policy/selection to
capture. `checkpoint` is non-cacheable because it inspects live client state,
but its resolver returns the ObjectResult selected from a pure internal
constructor, rather than minting a result whose recipe contains the effectful
`checkpoint` call. This is the same identity-preserving technique used when an
effectful Workspace operation returns an existing/pure result.

Semantically, restoration imports captured committed history into the frozen
source baseline; it does not expose arbitrary pack manipulation or stage that
history as engine-pending commits. The commit half is conceptually:

```graphql
input WorkspaceBundledCommit {
  sha: String!
  origin: String
  message: String!
  date: String!
  authorName: String!
  authorEmail: String!
  paths: [String!]!
}

extend type Workspace {
  """Restore bundled local history into the frozen source baseline."""
  withCommitBundle(
    base: GitRef!
    manifest: String!
    chunks: [WorkspaceCheckpointChunkID!]!
    commits: [WorkspaceBundledCommit!]!
  ): Workspace!
}
```

The initial worktree delta is then applied once with ordinary Workspace or
Changeset semantics. The implementation may fuse both steps into one internal
constructor, but the observable model remains “import captured commits into the
frozen baseline, then apply selected uncommitted state.”

The pure side is conceptually:

```graphql
extend type Query {
  _workspaceCheckpointChunk(data: String!): WorkspaceCheckpointChunk!

  _workspaceFromGitCheckpoint(
    base: GitRef!
    manifest: String!
    chunks: [WorkspaceCheckpointChunkID!]!
  ): Workspace!
}
```

These are internal core values, not a storage API. `base` is an ordinary remote
core `GitRef` pinned to `R`. The constructor reassembles and validates bounded
chunks, imports them using existing Git/snapshot machinery, and returns a
Directory/overlay-backed Workspace. Chunking keeps individual OTel call payloads
below measured backend limits and lets call-payload deduplication operate per
chunk; one unbounded base64 argument is not acceptable for large repositories.
Chunk size and aggregate limits must be established by an end-to-end trace
probe, not assumed from gRPC's existing Git stream limit.

Chunk `data` and the manifest use the existing
`dagql.DigestedSerializedString` input mechanism: the full runtime value is in
the encoded recipe, a separate digest controls identity, and ordinary call-ID
formatting renders a placeholder. Do not mark either argument sensitive.

A public `GitBundle` object with `bundle`/`withBundle` methods is unnecessary.
It would expose transport plumbing without solving payload durability or
chunking. The durable unit is the constructor recipe and its embedded chunk
calls; normal persisted values and snapshots may cache their evaluated results.

## 8. Opaque call data and current redaction behavior

Sensitive arguments cannot carry reconstructible bytes. Current DagQL behavior
replaces a sensitive argument with the literal `"***"` when building both the
encoded call ID and the telemetry call protobuf. The resolver may have seen the
real value during the original request, but a trace consumer sees the redacted
recipe and cannot replay it.

`DigestedSerializedString` is closer to the needed behavior but needs hardening.
Its `call.Literal` retains the value and digest, while `Display` and AST output
show only a digested-string placeholder. However, current telemetry renderers
in `dagql/dagui/extract.go` and `dagql/dagui/grep.go` read and display the
embedded value. Before checkpoint data is emitted, every call renderer, grep,
error, debug endpoint, and payload inspection path used by normal UI must render
only type/digest/size. The raw call protobuf must retain the value so
`EncodedIDForCallDigest` can reconstruct it.

Required tests put a canary in an approved chunk and assert:

- it is present in raw call payload reconstruction and a cold recipe replay
  succeeds;
- it is absent from span names, log bodies, progress output, errors, TUI call
  rendering, search/grep output, and debug summaries; and
- a rejected untracked canary never appears even in raw call payloads.

This is display opacity, not a new secret or storage abstraction.

## 9. Reconstruction

On a cold engine the constructor:

1. evaluates the remote `GitRef` recipe and obtains exact `R`;
2. reassembles chunks and verifies manifest version, digests, sizes, and object
   format;
3. initializes a canonical scratch repository using existing snapshot APIs;
4. imports the prerequisite commit bundle, requiring `R`, and verifies
   connectivity and logical tip `L`;
5. validates the ordered captured commits after `R` and keeps them as ordinary
   committed history in the source baseline;
6. sets logical `HEAD` and a normalized index to `L`;
7. applies the selected worktree delta once and verifies that the result is `W`;
8. constructs a value-backed Workspace source plus overlay remainder, cwd,
   config/lockfile, and author defaults; and
9. returns a Workspace with no client ID, host path, `currentWorkspace`,
   `host.directory`, implicit export destination, or mirror-generation
   dependency.

Workspace reads then resolve only against the reconstructed snapshots. Existing
agent overlays replay on top, and later `withCommit` calls continue the pending
stack. Restore never examines a destination checkout and never creates conflict
markers. A missing remote prerequisite, corrupt chunk, unsupported manifest, or
object mismatch is a hard and actionable restore error.

Normal evaluation may populate persisted result metadata, the snapshotter,
content store, or `RemoteGitMirror`. Those are disposable products of evaluating
the recipe. They are not additional restore inputs.

## 10. Long sessions and large monorepos

Initial capture must not use today's full `PackCheckout`. The remote supplies
all objects through `R`; client transfer contains only `R..L`, the approved
worktree delta, and bounded metadata. Git ignore traversal omits large ignored
trees, and untracked limits prevent accidental asset/cache uploads. Object
packing and chunking stream to temporary files; implementations must not hold
both raw and base64 copies of a large pack in memory.

`LLM.PortableRecipe` already drops superseded Workspace *bindings*, but it
carries the final Workspace recipe verbatim. It does not collapse that
Workspace's internal `withChanges`/`withCommit` ancestry. This is correct after
initial freezing, because those calls now have portable leaves, but very long
sessions may restore slowly.

A later compaction operation may encode the current pending graph and effective
worktree into a new checkpoint rooted at the same remote `R`. It must obey the
same approval/secret boundary before adding bytes to trace. Do not checkpoint
after every edit: repeated full payloads would defeat trace deduplication.
Measure completed-turn or explicit publication boundaries first; add
incremental packs only if full compaction is too costly.

Measure at minimum:

- local graph, tracked worktree, and approved/skipped untracked bytes;
- compressed bundle size, chunk count, and largest call payload;
- client scan/pack and cold remote-fetch/import time;
- recipe frame count before and after optional compaction; and
- secret, type, count, and size rejections.

Metrics contain no paths, contents, remote credentials, manifests, or chunks.

## 11. Save and publication

Checkpoint construction has no remote write. The pure Workspace recipe also has
no host path. In the originating live session only, the effectful checkpoint
operation may retain its source client and checkout as an ephemeral export target
outside the recipe and persisted payload, so explicit save can reconcile agent
commits back to the checkout that was just captured. A cold-restored checkpoint
has no such target, and no-argument `Workspace.export` must fail rather than
guess a destination.

That retention is session state keyed by the checkpoint's own identity — its
manifest digest, which the reconstructed Workspace carries and every workspace
derived from it inherits — and not a field on the Workspace value. The value is
the value of a pure recipe: it is shared by every session that resolves that
recipe, so a target stored on it would leak into a warm cross-session restore.
Keeping the target off the value by cloning it after construction is worse: the
effectful call cannot be cached, so a value it mints has no result identity, and
the composed agent cannot bind a workspace it cannot address by ID. The pure
constructor's result must therefore be returned verbatim, with the route to the
live checkout held beside it for the session's lifetime.

Saving is a separate, explicit reconciliation with a selected target:

```text
R   remote-backed base
L/W captured committed local history and worktree
C   current agent pending history and worktree
H   explicitly selected destination checkout
```

Existing staged-commit bundle export and moved-HEAD detection are useful
building blocks. If `H` is still at the expected base, commits can fast-forward;
if it advanced, an explicit plan can replay commits and report conflicts. Replay
may rewrite SHAs while preserving metadata and origin provenance. Uncommitted
content is applied only after commit planning. Materializing a new checkout is
the safe phase-1 fallback; in-place transactional reconciliation is follow-up
work.

Pushing is likewise an explicit `Workspace.git.push`-style action after review.
It is never invoked by checkpoint, agent spawn, restore, or compaction.

## 12. Validation

The headline test must prove recipe-only recovery:

1. Create a remote-backed repository with several linear unpushed commits,
   tracked text/binary dirt, approved ordinary untracked source, a very large
   ignored tree, and an untracked canary secret that is not ignored.
2. Capture, start an agent, make more edits and pending commits, and publish the
   trace without pushing any local state.
3. Destroy the originating engine including DagQL persistence, snapshotter,
   content store, and Git mirrors; delete the host checkout.
4. On a fresh engine and machine, restore only from the trace and the original
   remote.
5. Assert `R`, captured local commit order/metadata, logical `HEAD`, approved
   worktree, later agent-pending commits, agent overlays, module compilation, and
   continued tool use.
6. Assert initial ignored content and the rejected canary are absent, the old
   host is never read, and no remote ref changed during capture or restore.

Additional coverage:

- choose the closest advertised remote-backed ancestor, including competing
  remotes and stale remote-tracking refs;
- branch advancement retaining `R`, and force-push/GC removing it;
- detached HEAD, unborn HEAD, no remote-backed ancestor, and merge rejection;
- optional commit SHA rewriting with metadata/order preservation;
- linked worktrees, submodule checkout roots, separate Git dirs, alternates,
  partial clones, and SHA-256 object format where supported;
- binary, executable, symlink, deletion, and file/directory replacement dirt;
- ignore sources, nested repositories, changed submodules, and special files;
- approval race, untracked size/count limits, tracked-dirt secret refusal, and
  committed content passing the preflight unquestioned;
- truncated/reordered chunks, wrong digest/object format/prerequisite, and
  corrupt bundles;
- payload-size behavior and cold restore on a representative large monorepo;
- no-cache restore with no destination checkout; and
- explicit save to unchanged, advanced, dirty, and newly materialized targets.

## 13. Implementation order

1. **Trace/pack spike:** create bounded digested payload calls, fix every normal
   renderer, reconstruct a prerequisite bundle and dirty worktree after deleting
   all engine state, and measure actual backend payload limits.
2. **Safe initial capture:** add advertised-remote ancestor discovery,
   client-side approval/secret/size preflight, selected-path packing, and the
   pure Workspace constructor. Freeze before agent/tool startup.
3. **Local-history fidelity:** seed `R..L` into the frozen source baseline and
   verify `git.head`, empty initial `stagedCommits`, `uncommitted`, later
   `withCommit`, and explicit export/push planning. Phase 1 rejects merges.
4. **Scale and compaction:** benchmark monorepos and long edit chains; add
   deliberate full/incremental compaction only where measurements require it.
5. **Reconciliation and edge cases:** add explicit target planning, merge-graph,
   submodule/LFS, and non-Git policies.

Explicit engine-view pinning is not part of these phases. If long-term trace
compatibility later needs a checkpoint-wide view/version contract, design it
with the general recipe compatibility mechanism rather than adding a one-off
field here.

## 14. Code map

- Workspace sources, overlays, and pending state: `core/workspace.go`
- Workspace reads and Git assembly: `core/schema/workspace.go`
- `withCommit` and staged bundle export: `core/schema/workspace_commit.go`,
  `core/workspace_commit.go`
- Host repository/worktree packing: `engine/session/git/git_pack.go`,
  `engine/session/git/git_worktree.go`
- Canonical Git reconstruction and snapshots: `core/git_hostdir.go`
- Remote Git and persisted mirror cache: `core/schema/git.go`,
  `core/git_remote_mirror.go`
- Portable LLM recipe flattening: `core/llm.go`
- Call-frame redaction and recipe rebuilding: `dagql/result_call_frame.go`,
  `dagql/call/id.go`
- Call-payload closure emission: `core/dag_call_telemetry.go`
- Digested-string rendering: `dagql/call/literal.go`,
  `dagql/dagui/extract.go`, `dagql/dagui/grep.go`
- Trace restore: `internal/cmd/dagger/restore.go`
