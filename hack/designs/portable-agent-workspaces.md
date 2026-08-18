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

At agent binding time, make the binding independent of live client state. For the
phase-1 case that actually needs capture—a client-local Git checkout—replace it
with a frozen Git workspace:

```text
R  latest ancestor of local HEAD proven present on a remote
 + commits after R that have not been pushed
 + approved tracked dirt and nonignored untracked files
 = frozen initial workspace
```

This is an agent-binding precondition, not a general Workspace mutation. A
replayable remote-Git or synthetic value contributes no new source bytes and is
reused, pinned, or pure-normalized as needed for agent mutations; an unsupported
host or non-replayable value is rejected. There is no public
`Workspace.checkpoint` in phase 1.

Agent overlays and `Workspace.withCommit` calls then build on the frozen value.
The trace recipe must reconstruct the same effective tree, captured local
history, and later engine-pending history from its own call payloads plus the
remote containing `R`. Persisted result records, Dagger snapshots, Git mirrors,
and any other state on the originating engine are cache accelerators only;
deleting all of them must not change correctness.

The design has the following settled constraints:

- use Dagger's core Git, File, Directory, Changeset, Workspace, DagQL call-data,
  and snapshot primitives; do not add a workspace storage service;
- do not add a public generic freeze/checkpoint operation: the agent binder
  passes replayable values through, captures only eligible client-local Git,
  and rejects everything else;
- never push a branch, hidden ref, or object as a side effect of agent startup;
- preserve local commits and approved dirty/nonignored-untracked state in one
  embedded Git bundle rooted at a remote prerequisite;
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

The implementation supplies most mechanics and a current spike carries local
bytes through Workspace-specific payload values. The target design below keeps
the proven reconstruction pieces while replacing that prototype API and format.

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
removing hooks, reflogs, and scratch files. Bundle reconstruction should reuse
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
`WorkspaceSourceGitRef`; therefore an agent-normalized Git workspace must use a
value-backed Directory/overlay source until that restriction changes. A remote
`GitRef` can supply the canonical repository without becoming the final source
kind.

Captured local commits are already real commits in the user's checkout. They
therefore belong to the frozen immutable source baseline: set logical `HEAD` to
`L`, and represent only approved worktree state `W` as pending on top. Captured
local commits must not appear in `Workspace.git.stagedCommits` or
`Workspace.changes`, whose save/upload semantics describe commits and changes
that exist only in the engine. Later `Workspace.withCommit` calls continue to use
the ordinary pending stack from captured `L`.

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
engine is gone. Every inline bundle/File call must therefore be a recipe-form
call whose complete payload reaches that closure.

### 2.4 Source applicability

Portability is a property of the complete recipe, not merely the Go source
variant or the snapshot currently cached for it. In particular, a
`WorkspaceSourceDirectory` can still have `host.directory` in its transitive
recipe. The agent binder must inspect the source and its recipe leaves before it
starts modules or the model:

| Workspace source at agent binding | Phase-1 behavior | Reason |
| --- | --- | --- |
| `WorkspaceSourceClientLocal` rooted at a Git checkout | Capture once into the bundle described below | This is the supported live-host case and the only one that needs new source bytes |
| `WorkspaceSourceGitRef` backed by a replayable remote recipe | Pin a mutable ref to its resolved commit, then reuse or pure-canonicalize it without client bytes | The remote recipe reconstructs the value, but agent commits currently require the value-backed Directory/overlay form |
| `WorkspaceSourceDirectory` whose full recipe has only replayable leaves | Reuse it, with pure normalization only if an agent mutation API requires it | Re-encoding source bytes would add cost and no portability |
| Overlay on a replayable GitRef/Directory whose overlay inputs are replayable | Reuse its recipe after pinning/normalizing the base as needed | The existing value recipe already describes the edits and adds no host bytes |
| Directory/overlay with a client-routed or otherwise non-replayable leaf | Reject and name the offending leaf | An engine snapshot does not make a host recipe portable |
| `WorkspaceSourceRootlessLocal`, client-local non-Git, or Git with no usable `HEAD`/remote prerequisite | Reject | Phase 1 has no truthful reconstruction contract for it |

Remote replay may require credentials supplied through the restoring session's
normal Git authentication path; those credentials never become recipe payload.

Phase 1 also rejects a client-local Git Workspace that already carries Dagger
pending commits, mounts, or a sparse Workspace overlay. The automatic capture
runs before the agent creates any of those states. Supporting a derived live
Workspace would require rebasing and re-approving that state; silently dropping
it or pretending that client Git can see it is not acceptable. Long-session
compaction is separate follow-up work.

This matrix is intentionally narrower than “any Workspace with a `.git`
directory.” The binder, not a public mutation field, owns the branch because it
knows why portability is required and can avoid doing anything to values that
already satisfy it.

### 2.5 Prototype API to retire

The current spike exposes `Workspace.checkpoint`,
`_workspaceCheckpointChunk`, `_workspaceFromGitCheckpoint`, and a versioned
`core.WorkspaceGitCheckpointManifest`. Those names and shapes proved cold
recipe reconstruction, but they are not the target API. `checkpoint` suggests a
generic Workspace lifecycle operation even though its resolver accepts only a
client-local Git source. The manifest also repeats facts already carried by the
remote `GitRef`, Git bundle, File identity, and commit objects, while positional
chunk descriptors expose telemetry payload sizing in the core schema.

The design below removes the public field and Workspace-specific chunks and
manifest. The only Git transport value is a standard bundle File. Any literal
opacity or segmentation needed to carry that File belongs to generic DagQL/File
call-data handling.

## 3. Portable Git model

Capture records four Git states:

```text
R  remotely reconstructible base commit
L  logical local HEAD, including commits not yet on the selected remote
W  approved effective worktree relative to L
S  private synthetic commit whose parent is L and whose tree is W
```

For phase 1, `R..L` must be linear. Merge commits are rejected until restore
supports and tests arbitrary graph topology. Capture creates `S` in a temporary
object database and index; it does not add a commit, object, or ref to the user's
repository. The temporary index starts from `L` and stages exactly the approved
tracked dirt and ordinary untracked files. Initially ignored content and every
rejected candidate are therefore absent from `S`.

The portable Git payload is one version-3 Git bundle:

```text
prerequisite: R
sole advertised ref: refs/dagger/agent-workspace/v1 -> S
objects: commits/trees/blobs needed for R..L and S, excluding R's reachable graph
```

The bundle header supplies the prerequisite, private ref, and object-format
capability. The commit objects supply the complete history and metadata. `S`'s
single parent identifies `L`, and its tree identifies `W`; no second worktree
patch, ordered commit list, or `WorkspaceGitCheckpointManifest` is needed. The
remote `GitRef` recipe identifies the sanitized remote URL/ref and exact `R`.
The inline File's normal call identity protects the bundle bytes. Workspace-only
state—cwd, config and lockfile paths, selected config environment, and author
defaults—is passed as typed scalar constructor arguments, not serialized into a
Git transport manifest.

`S` is transport plumbing, not user history. Restore imports the bundle, checks
that it has exactly the expected prerequisite and private ref, derives `L` from
`S`'s sole parent, validates `R..L`, sets logical `HEAD` and the index to `L`,
and materializes `S^{tree}` only as the dirty/untracked worktree. It then deletes
the private ref. `Workspace.git.head` reports `L`; captured commits are ordinary
baseline history; `Workspace.git.uncommitted` reports `L..W`; and `S` never
appears in staged commits, logs, save plans, or pushes.

Using one bundle does not weaken selection. The security boundary is the
temporary index used to make `S`, not whether selected objects occupy a separate
file. Capture constructs and verifies the bundle only after preflight approval
and re-hashing. It must enumerate the bundle's reachable objects before sending
bytes and prove that they are exactly the allowed local-history closure plus the
selected snapshot closure; a normal `git bundle create --all` is forbidden.

### 3.1 Bundle versus raw pack and `format-patch`

A bare pack is compact, but it has no advertised-ref/prerequisite envelope. We
would have to invent side metadata naming `R`, `L`/`S`, and the object format,
then keep that metadata synchronized with the pack. A version-3 Git bundle is
the standard Git container for that information and is verified/imported with
`git bundle verify` and `git fetch`. Prefer the bundle; do not wrap a raw pack in
a Workspace-specific manifest that recreates the bundle header.

`git format-patch --binary` is also an established transport and is attractive
for a linear series: mail headers carry much of the author/message information,
and `git am` naturally creates rewritten commits. It does not simplify the
portable-trace problem, however. The patch files still need inline,
reconstructible, opaque, size-bounded recipe carriage. It also:

- expands binary changes into textual patches instead of transferring Git's
  compressed object representation;
- replays path/context deltas one commit at a time, which can be larger and more
  failure-prone for repeated edits and large binary files;
- does not preserve commit objects, committer identity/date, signatures,
  encoding headers, or arbitrary topology with object-level fidelity; and
- still needs a separate representation of the final dirty/untracked worktree.

The bundle transfers exact commit/tree/blob objects, deduplicates and delta
compresses object content, verifies connectivity against `R`, retains mode and
binary data without contextual replay, and lets one synthetic commit carry `W`.
That fidelity and scale are worth the opaque binary payload. Phase 1 happens to
preserve local commit SHAs when importing the same objects over exact `R`, but
stable pre-publication SHAs remain outside the API contract: later filtering,
compaction, or save reconciliation may rewrite local history while preserving
its user-visible metadata and origin.

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
followed by remote object pruning can make an old frozen recipe unrestorable;
the constructor must report the missing prerequisite rather than importing the
bundle over another base. If capture cannot prove any remote-backed ancestor, it
fails. It does not publish local objects to manufacture one.

Private remotes require credentials in the restoring session through existing
Git authentication mechanisms. Credentials are not frozen-workspace payload.

## 5. Capture and approval

Capture is an effectful client operation invoked only by the agent binder. It
returns/selects a pure Workspace recipe and runs before module/tool derivation
and before the agent loop starts.

1. Confirm the source-matrix preconditions; read and revalidate local Git state;
   find `R`, `L`, and candidate worktree paths.
2. Exclude initially ignored, untracked paths using Git's own ignore rules,
   including `.gitignore`, `.git/info/exclude`, and configured global excludes.
   A tracked file remains tracked even if a later ignore rule matches it.
3. Locally classify candidate tracked dirt and ordinary untracked files by path,
   type, size, and secret heuristics. Committed blobs are bounded by type and
   size only; secret heuristics do not apply to them.
4. Present a local summary and obtain policy/user approval. Noninteractive use
   must supply an explicit policy; it must not imply approval of every
   nonignored file.
5. In a temporary object database and index, start from `L`, stage only approved
   paths, create `S` with parent `L`, and re-hash every selected path. If files or
   Git state changed after review, abort and repeat rather than capturing
   unreviewed bytes.
6. Ask client Git for one version-3 prerequisite bundle advertising only
   `refs/dagger/agent-workspace/v1 -> S` and excluding objects reachable from
   `R`. Verify its header, object format, sole ref, prerequisite, connectivity,
   object closure, linear `R..L`, and `S` tree locally before any bytes cross the
   client boundary.
7. Create a generic inline File recipe for the bundle and select the pure,
   typed Workspace constructor from the remote base, bundle File, and Workspace
   metadata. Only then bind and start the agent.

Phase 1 excludes initial Gitignored content unconditionally. It also rejects
special files, changed submodules, nested-repository contents, and unsupported
history rather than approximating them. Sockets, devices, FIFOs, ownership,
ACLs, xattrs, hardlink identity, stashes, reflogs, hooks, notes, replace refs,
credentials, cache mounts, and service state are not captured.

The index's staged-versus-unstaged distinction is not preserved: `S^{tree}` is
the approved uncommitted worktree relative to `L`. `S` itself is never exposed
as a Workspace pending commit.

## 6. Secret boundary

A nonignored untracked file is not thereby safe. `.env`, private keys, cloud
credentials, package-manager credentials, token files, and arbitrary
high-entropy blobs are common examples. Once such bytes become part of the
inline bundle File, they are in the raw trace and cannot be made safe by
changing how the TUI renders them.

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
- re-hash selected content during synthetic-commit and bundle creation to close
  the review/upload race.

Approval is meaningful only before payload construction. A design that first
uses today's `PackWorktree`, receives all ordinary untracked bytes in the
engine, and scans there is not sufficient for this boundary.

These mitigations are cumulative defense in depth, not alternatives. The single
bundle is safe only because `S` is built from a temporary index populated from
the approved path set and the resulting reachable-object closure is checked
before upload; using one bundle is not permission to run `git add -A` or bundle
all refs. Path rules, content scanning, explicit approval, size limits, opaque
rendering, and trace access control cover distinct failure modes. Tracked dirt
is scanned too; “tracked” does not mean “safe to upload.” Committed content is
the exception, and only because committing is already an explicit decision to
record content in shared history.

Raw-trace readers can recover every approved source byte. Compression, Git
object hashing, and opaque rendering are not encryption. Trace authorization,
retention, and deletion policy must treat the inline bundle like a source
archive. Cloud's existing **Delete trace** action is useful last-resort
containment, but it is not a pre-upload control. Before relying on it, verify
that deletion covers the complete call-payload closure and generic inline-File
payload frames, not only the user-visible span index.

## 7. Core API shape

Phase 1 adds no public Workspace operation. Portability is enforced where the
agent binder accepts its Workspace: replayable values take the non-capture path,
eligible client-local Git takes the capture path, and unsupported values fail
before tools or model execution. Capture policy and interactive approval remain
CLI/session concerns rather than options on a generic core mutation.

The effectful client capture must return the ObjectResult selected from a pure
internal constructor, not mint a result whose recipe contains the effectful
call. Conceptually the complete Git-specific pure API is:

```graphql
extend type Query {
  _workspaceFromGitBundle(
    base: GitRef!
    bundle: File!
    cwd: String!
    configFile: String
    lockFile: String
    environment: String
    gitAuthorName: String
    gitAuthorEmail: String
  ): Workspace!
}
```

`base` is an ordinary remote `GitRef` pinned to exact `R`. `bundle` is an inline,
recipe-backed File containing the standard bundle described in §3. The
constructor derives `S`, `L`, the object format, history, and final worktree from
Git data; it does not accept parallel claims for those facts. The remaining
arguments are typed Workspace state that Git does not know. Field versioning and
the private bundle ref version the reconstruction semantics. There is no
`WorkspaceGitCheckpointManifest`, commit metadata input, worktree-patch input,
or Workspace-specific chunk object.

The constructor imports with existing Git/snapshot machinery and returns the
value-backed Directory/overlay form that `Workspace.withCommit` supports. This
normalization also matters on the no-capture path: a mutable remote Git ref is
first pinned to its resolved commit, and a `WorkspaceSourceGitRef` is
pure-canonicalized to the equivalent value-backed form until `withCommit`
supports GitRef sources directly. That operation reuses the existing remote
recipe and transfers no client bytes. Replayable Directory/overlay values are
likewise reused, with only the minimum pure normalization needed by agent
mutation APIs.

The bundle is a `File`, not a new public `GitBundle` object. A generic inline
File/blob implementation owns embedding arbitrary bytes in a recipe. If a trace
backend requires payload segmentation, the call-data layer segments and
reassembles generic File literals below the core schema; segment size is not
part of the constructor signature or Git format. End-to-end probes must still
establish per-frame and aggregate limits, and the implementation must stream
rather than hold raw and encoded copies at once.

The constructor call digest—covering pinned base, bundle File identity, and all
typed metadata—is the frozen root's stable identity. The originating session
may key its ephemeral save route by that digest. No separately serialized
manifest digest is required.

## 8. Opaque inline File data

Sensitive arguments cannot carry reconstructible bytes. Current DagQL behavior
replaces a sensitive argument with the literal `"***"` in both the encoded call
ID and telemetry call protobuf, so a trace consumer cannot replay it. The bundle
File must therefore be opaque in normal presentation but present in raw recipe
call data.

`dagql.DigestedSerializedString` is one possible implementation detail for an
inline File literal: its value remains in the encoded recipe, its digest controls
identity, and its normal display can show only digest and size. It is not the Git
transport format and must not surface as `_workspaceCheckpointChunk` or as a
constructor `String`. Prefer a generic byte-File/blob path so other large inline
artifacts get the same framing and display behavior.

Before bundle data is emitted, every call renderer, grep/search path, error,
debug endpoint, and normal payload inspector must render only type, digest, and
size for generic inline bytes. The raw call protobuf retains the bytes so
`EncodedIDForCallDigest` can reconstruct the File. Do not mark the data
sensitive, and do not mistake display opacity for encryption.

Required tests put a canary in an approved file included in `S` and assert:

- it is present in raw call payload reconstruction and a cold recipe replay
  succeeds;
- it is absent from span names, log bodies, progress output, errors, TUI call
  rendering, search/grep output, and debug summaries; and
- a rejected untracked canary never appears in the bundle or any raw call
  payload.

## 9. Reconstruction

On a cold engine the internal constructor:

1. evaluates the pinned remote `GitRef` recipe and obtains exact `R`;
2. evaluates the inline File recipe and enforces generic File/frame and aggregate
   size bounds before importing bytes;
3. initializes a canonical scratch repository using existing snapshot APIs;
4. requires a version-3 bundle whose object-format capability matches the
   repository, whose sole prerequisite is `R`, and whose sole advertised ref is
   `refs/dagger/agent-workspace/v1`;
5. verifies/imports the bundle with Git, resolves that ref to `S`, requires
   exactly one parent `L`, and verifies that `R..L` is the supported linear local
   history and `S` introduces no second parent or unexpected reachable object;
6. makes the committed source baseline end at `L`, with logical `HEAD` and a
   normalized index at `L`;
7. materializes `S^{tree}` as worktree `W` without moving `HEAD`, computes the
   ordinary `L..W` Workspace overlay, and removes the private ref;
8. applies the typed cwd, config/lockfile, environment, and author defaults; and
9. returns a value-backed Workspace with no client ID, host path,
   `currentWorkspace`, `host.directory`, implicit export destination,
   synthetic-ref visibility, or mirror-generation dependency.

The constructor reads commit ordering and metadata directly from imported Git
objects; there is no second description to reconcile. Workspace reads then
resolve only against reconstructed snapshots. Existing agent overlays replay on
top, and later `withCommit` calls continue the pending stack from `L`. Restore
never examines a destination checkout and never creates conflict markers. A
missing remote prerequisite, malformed/ref-mismatched bundle, unsupported graph,
object-format mismatch, truncated inline File, or non-replayable typed input is
a hard and actionable restore error.

Normal evaluation may populate persisted result metadata, the snapshotter,
content store, or `RemoteGitMirror`. Those are disposable products of evaluating
the recipe. They are not additional restore inputs.

## 10. Long sessions and large monorepos

Initial capture must not use today's full `PackCheckout`. The remote supplies
all objects through `R`; client transfer contains only objects needed for
`R..L` and synthetic `S`. Git ignore traversal omits large ignored trees,
untracked limits prevent accidental asset/cache uploads, and Git pack
compression can delta repeated/binary content. Bundle creation and generic
inline-File framing stream through temporary files; implementations must not
hold raw, base64, and framed copies of a large bundle in memory.

`LLM.PortableRecipe` already drops superseded Workspace *bindings*, but it
carries the final Workspace recipe verbatim. It does not collapse that
Workspace's internal `withChanges`/`withCommit` ancestry. This is correct after
initial binding because those calls have portable leaves, but very long sessions
may restore slowly.

A later binder-internal compaction may encode the current pending graph and
effective worktree into a new bundle rooted at the same remote `R`. It must obey
the same approval/secret boundary before adding bytes to trace. Do not compact
after every edit: repeated full bundles would defeat trace deduplication.
Measure completed-turn or explicit publication boundaries first; add
incremental bundles only if full compaction is too costly. This does not imply a
public Workspace checkpoint API.

Measure at minimum:

- local graph, tracked worktree, and approved/skipped untracked bytes;
- compressed bundle size, generic payload-frame count, and largest call frame;
- client scan/bundle and cold remote-fetch/import time;
- recipe frame count before and after optional compaction; and
- secret, type, count, and size rejections.

Metrics contain no paths, contents, remote credentials, bundle bytes, or typed
Workspace metadata.

## 11. Save and publication

Agent-binding capture has no remote write. The pure Workspace recipe also has no
host path. In the originating live session only, the binder may retain the
source client and checkout as an ephemeral export target outside the recipe and
persisted payload, so explicit save can reconcile agent commits back to the
checkout that was captured. A cold-restored frozen Workspace has no such target,
and no-argument `Workspace.export` must fail rather than guess a destination.

That retention is session state keyed by the pure constructor call digest, which
the reconstructed Workspace and its derived values retain as internal origin
identity; it is not a destination field on the shared Workspace value. Storing a
target on that value would leak it into a warm cross-session restore. Cloning the
value after construction is worse: the effectful capture cannot be cached, so a
value it mints has no result identity and the composed agent cannot bind it by
ID. The binder must return the pure constructor result verbatim and hold the
route beside it for the session's lifetime. No manifest is involved.

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
It is never invoked by agent binding, capture, restore, or compaction.

## 12. Validation

The headline test must prove recipe-only recovery:

1. Create a remote-backed client-local repository with several linear unpushed
   commits, tracked text/binary dirt, approved ordinary untracked source, a very
   large ignored tree, and an untracked canary secret that is not ignored.
2. Bind an agent, assert that automatic capture creates a pure constructor recipe
   with one inline bundle File, then make more edits/pending commits and publish
   the trace without pushing local state.
3. Destroy the originating engine including DagQL persistence, snapshotter,
   content store, and Git mirrors; delete the host checkout.
4. On a fresh engine and machine, restore only from the trace and original
   remote.
5. Assert exact `R`, bundle-derived local commit order/metadata, logical `HEAD =
   L`, `S` absent from visible refs/history, approved worktree `W`, later agent
   commits/overlays, module compilation, and continued tool use.
6. Assert initial ignored content and the rejected canary are absent even from
   raw payloads, the old host is never read, and no remote or host ref changed
   during capture or restore.

Additional coverage:

- every source-matrix row, including mutable remote-ref pinning, pure
  GitRef-to-value normalization for `withCommit`, replayable Directory pass
  through, and rejection naming a non-replayable leaf;
- closest advertised remote-backed ancestor, competing remotes, stale
  remote-tracking refs, branch advancement retaining `R`, and force-push/GC
  removing it;
- detached HEAD, unborn HEAD, no remote-backed ancestor, pre-existing local
  Workspace overlay/pending state, and merge rejection;
- linked worktrees, submodule checkout roots, separate Git dirs, alternates,
  partial clones, and SHA-256/version-3 bundles where supported;
- binary, executable, symlink, deletion, and file/directory replacement dirt;
- ignore sources, nested repositories, changed submodules, and special files;
- approval race, untracked size/count limits, tracked-dirt secret refusal,
  committed content passing preflight unquestioned, and exact selected-object
  closure for `S`;
- wrong/multiple prerequisite or ref, wrong object format, malformed `S`, extra
  reachable objects, corrupt/truncated bundle File, and generic frame loss;
- payload-size behavior and cold restore on a representative large monorepo;
- canary opacity in every normal renderer while raw recipe replay succeeds;
- no-cache restore with no destination checkout; and
- explicit save to unchanged, advanced, dirty, and newly materialized targets.

## 13. Implementation order

1. **Binder and payload foundation:** implement the source/replayability matrix,
   mutable GitRef pinning and pure value normalization; add a generic inline
   binary File recipe with opaque rendering and transport-level framing; measure
   backend limits after deleting all engine state.
2. **Safe single-bundle capture:** add advertised-remote ancestor discovery and
   client-side approval/secret/size preflight; create `S` from a selected-path
   temporary index and emit one verified version-3 prerequisite bundle. Remove
   the public `Workspace.checkpoint`, Workspace-specific chunk values, and
   `WorkspaceGitCheckpointManifest` prototype surface.
3. **Pure reconstruction:** add the typed `_workspaceFromGitBundle` constructor;
   derive/validate `R`, `L`, `S`, history, and `W` from Git; verify `git.head`,
   empty initial `stagedCommits`, `uncommitted`, later `withCommit`, origin
   routing by constructor digest, and cold trace-only restore. Phase 1 rejects
   merges and pre-derived client-local Workspaces.
4. **Scale and compaction:** benchmark bundles against `format-patch` on
   representative text/binary monorepos and long edit chains; add deliberate
   internal full/incremental compaction only where measurements require it.
5. **Reconciliation and edge cases:** add explicit target planning, merge-graph,
   submodule/LFS, derived-live-Workspace, and non-Git policies.

Explicit engine-view pinning is not part of these phases. If long-term trace
compatibility later needs a frozen-workspace-wide view/version contract, design
it with the general recipe compatibility mechanism rather than adding a bespoke
Git manifest.

## 14. Code map

- Agent binding and current capture prototype: `internal/cmd/dagger/agent.go`,
  `core/schema/workspace.go`
- Workspace sources, overlays, persistence, and pending state: `core/workspace.go`
- `withCommit` and staged bundle export: `core/schema/workspace_commit.go`,
  `core/workspace_commit.go`
- Host Git discovery/capture: `engine/session/git/git_capture.go`,
  `engine/session/git/git_pack.go`, `engine/session/git/git_worktree.go`
- Canonical Git reconstruction and snapshots: `core/git_hostdir.go`
- Remote Git, ref pinning, and persisted mirror cache: `core/schema/git.go`,
  `core/git_remote_mirror.go`
- Inline File values: `core/schema/file.go`, `core/file.go`
- Portable LLM recipe flattening: `core/llm.go`
- Call-frame redaction and recipe rebuilding: `dagql/result_call_frame.go`,
  `dagql/call/id.go`
- Call-payload closure emission: `core/dag_call_telemetry.go`
- Opaque literal rendering: `dagql/call/literal.go`,
  `dagql/dagui/extract.go`, `dagql/dagui/grep.go`
- Trace restore: `internal/cmd/dagger/restore.go`
