# Shared Host Git Mirrors

Internal implementation design for making host-backed Git workspaces cheap over
slow client links and cheap to reconstruct inside the engine. Builds on
[host-git-reconstruction.md](host-git-reconstruction.md) and the Git workspace
assembly in [sandboxes.md](sandboxes.md).

Status: **proposed**.

## 1. Problem

Host Git reconstruction is correct but sends and transforms too much:

1. **Cold checkouts upload their whole reachable graph.** `PackCheckout`
   creates a bundle of HEAD plus every local branch and tag. A new cache key,
   workspace epoch, client, or engine may send the full history over the
   client's connection even when the same objects are already on a remote the
   engine can reach.
2. **The cache is state-shaped, not mirror-shaped.**
   `Host.__gitDir(path, stateDigest)` caches one immutable reconstruction. A ref
   move creates another full reconstruction instead of advancing a persistent
   object graph with only the new objects.
3. **Equivalent clients do not help each other.** Two machines using the same
   remote independently upload the same history. This is particularly costly
   for remote engines and poor Wi-Fi.
4. **The warm worktree path is redundant.** The engine constructs `HEAD + host
   worktree patch`, runs Git over that tree to derive `WorkspaceGit.uncommitted`,
   returns a `Changeset`, and the sandbox module reapplies it to `head.tree`.
   The effective directory existed in the middle and was thrown away.
5. **Remote and local transport are disconnected.** `RemoteGitMirror` already
   persists a bare repository per remote URL and fetches missing SHAs, but host
   checkout reconstruction does not use it as a seed.

The current implementation remains the compatibility fallback. This design
changes the fast path, not the invariant that the client's own Git interprets
its checkout layout.

## 2. Evidence

A benchmark used a host Git checkout with 8,000 tracked 4 KiB files, 8,000
ignored 4 KiB files, one tracked modification, and one ordinary untracked file.
The module and base container were resolved before timing; each sample ran a
fresh trivial sandbox exec and forced change extraction.

| Workspace assembly | Cold sample | Warm median |
|---|---:|---:|
| `Workspace.directory("/")` | 2.38s | 365ms |
| Git flow before packed worktree delta | 7.82s | 1.54s |
| Current Git flow | 4.47s | 1.39s |

Cold samples were noisy; repeated warm results were stable enough to show the
shape: the directory path took 329-385ms, while the Git path took 1.39-1.94s.
The packed `uncommitted` optimization itself was effective: a direct
`Workspace.git.uncommitted` query improved from a 1.27s median to 519ms. It
removed a full host sync, but did not remove the rest of the Git round trip.

A representative warm trace attributed a 1.2s sandbox call approximately as:

```text
Sandbox.exec                         1.2s
├─ WorkspaceGit.uncommitted         0.3-0.8s
├─ Directory.withChanges            0.6s
├─ Container.withDirectory          0.1s
└─ exec true                        0.2s
```

`WorkspaceGit.head` was already effectively free: the immutable canonical Git
directory was a cache hit. A persistent graph therefore targets cold and
cross-state bandwidth; returning the effective directory directly targets the
warm latency.

## 3. Goals

1. A clean checkout whose HEAD is available from a reachable remote uploads no
   repository graph from the client.
2. A checkout ahead of its remote uploads only locally unique Git objects.
3. Dirty worktree transfer is proportional to modified and ordinary untracked
   content, not checkout size.
4. Git objects and remote fetches are reused across clients and engine restarts.
5. Warm workspace assembly applies the worktree delta once and returns the
   resulting immutable `Directory` directly.
6. Remote hydration is automatic and best-effort. Every failure falls back to
   client transfer without changing workspace semantics.
7. Existing host layouts continue to work: plain clones, linked worktrees,
   submodules, separate Git directories, alternates, partial clones, unborn
   repositories, and SHA-256 repositories.
8. Workspace read epochs, staged commits, and export's moved-HEAD guard retain
   their current coherence guarantees.

## 4. Non-goals

- Adding broad public Git APIs. One narrow, version-gated
  `WorkspaceGit.directory` field is required so ordinary modules such as
  `modules/sandbox` can consume the optimized result.
- Making the engine interpret raw host `.git` layouts.
- Replacing Git's object validation or hash model.
- Synchronizing ignored host files. Workspace overlay edits to ignored or nested
  paths remain the separate `WorkspaceGit.unmanaged` concern.
- Guaranteeing that the engine can reach every configured remote.
- Treating remote state as authoritative over the client's HEAD or refs.
- Establishing multi-tenant object isolation. Engines are single-tenant; broad
  reuse inside one engine is an explicit optimization boundary.
- Solving Git LFS as a separate transport. LFS pointer files remain Git objects;
  materialized worktree content follows ordinary worktree-delta behavior.

## 5. Invariants

### 5.1 The client Git remains the host-layout oracle

The client resolves:

- the checkout's Git common directory and object format;
- HEAD, symbolic HEAD, local branches, tags, and tracking configuration;
- configured remotes after client-side Git URL rewriting;
- which engine "have" objects are valid prerequisites locally;
- the Git-visible worktree delta.

The engine consumes refs, packs, and plain Git repositories it built itself. It
never parses a host gitfile, `commondir`, alternates, partial-clone config, or
other raw layout.

### 5.2 Mirrors are mutable backing state; results are immutable

A shared mirror may gain objects and internal refs at any time. A `GitRef`,
`Changeset`, or `Directory` returned for workspace read epoch N must not change
when the mirror advances to epoch N+1.

Every outward result is pinned by immutable metadata:

```text
object format
checkout ref-state digest
HEAD SHA and symbolic ref
visible local refs
worktree-delta digest
workspace overlay ID
workspace read epoch
```

### 5.3 The client's state wins

Remote fetches provide objects, not checkout truth. The visible HEAD, branches,
tags, index baseline, and worktree come from the client's state descriptor. A
remote may contain extra or newer refs without changing the workspace.

### 5.4 Fast-path failure is not semantic failure

Remote DNS, auth, SSH, service networking, SHA fetch, thin-pack negotiation,
mirror persistence, or pack import may fail. The engine retries with less
incremental client transfer and ultimately the existing full `PackCheckout`
flow. A broken checkout still fails loudly as it does today.

## 6. End-state architecture

```text
                         engine network
Remote A ───────────────► RemoteGitMirror(A) ──┐
Remote B ───────────────► RemoteGitMirror(B) ──┤ engine-local seeds
                                               ▼
Client checkout ─ describe ───────────► SharedCheckoutGitMirror
       │                                       │
       ├─ thin pack of local-only objects ─────┤
       └─ worktree patch ──────────────────────┤
                                               ▼
                                  immutable effective Directory
                                  (HEAD + patch + overlay + .git)
```

There are three different kinds of state.

### 6.1 `RemoteGitMirror`: remotely obtainable objects

The existing persisted mutable bare mirror remains keyed by normalized remote
URL. It is extended to record object format and enough fetch metadata to seed
host checkout mirrors without exposing its mutable refs as checkout refs.

Properties:

- persisted and visible to cache accounting/pruning;
- reused across clients and sessions;
- updated under a per-mirror lock;
- credentials and SSH sockets are call-scoped and never persisted;
- remote refs are fetch metadata, not a local checkout's visible refs.

For public remotes, the engine fetches directly without client credentials. For
private remotes, it uses the current session's explicit auth or existing client
credential-helper/SSH-agent bridges. A warm mirror may be used across clients
inside the single engine tenant even when the remote is temporarily
unreachable.

### 6.2 `SharedCheckoutGitMirror`: the checkout graph superset

A new persisted mutable bare mirror accumulates the objects needed by host
checkouts. Its preferred cache identity is:

```text
object format
sorted canonical fetch-remote URLs
```

This deliberately omits client and credential identity so equivalent checkouts
share graph state broadly. URLs are stripped of embedded credentials before
leaving the client. Remotes that cannot be represented safely (`file://`, local
paths, client-only service addresses) are not mirror keys.

When there is no usable remote, the fallback identity is:

```text
object format
stable client ID
opaque checkout identity
```

The opaque identity is maintained in client state and associated with the
canonical Git common directory. It is shared by linked worktrees but changes if
the repository at that location is replaced.

The mirror contains a superset:

- objects copied or fetched from one or more `RemoteGitMirror`s;
- objects uploaded by clients because they were not remotely obtainable;
- objects created by engine-side staged commits;
- internal namespaced refs used for validation and retention.

It does **not** have one authoritative HEAD or user-visible ref namespace.
Per-read visible refs live in immutable checkout-state metadata. Ref deletion or
force-push therefore does not destructively remove objects needed by another
pinned result.

Broad object reuse is intentional because the engine is single-tenant. Object
presence is not exposed as a public cache oracle, and credentials are never
stored in the mirror.

### 6.3 Immutable checkout state and effective directory

A successful synchronization produces an immutable checkout-state record:

```text
mirror result ID and generation token
object format
HEAD SHA / symbolic HEAD
visible branches and tags
nested repository boundaries
worktree patch digest
```

The engine derives two immutable snapshots from it:

1. **Canonical checkout** — plain `.git`, normalized index, visible refs only,
   and a clean HEAD worktree.
2. **Effective directory** — canonical checkout plus the host worktree patch and
   workspace overlay.

The effective directory is the direct input to sandbox/container composition.
`WorkspaceGit.uncommitted` may still derive a `Changeset` for callers that ask
for status/diff/commit, but workspace assembly no longer needs to derive and
reapply it.

## 7. Client protocol

Replace the cold path's independent `CheckoutState`, `PackCheckout`, and
`PackWorktree` sequence with a negotiated checkout synchronization. Keep the old
RPCs for compatibility and fallback.

### 7.1 `DescribeCheckout`

The client returns a small descriptor:

```protobuf
message CheckoutDescriptor {
  string state_digest = 1;
  string object_format = 2;
  string head_sha = 3;
  string head_ref = 4;
  repeated GitRef refs = 5;
  repeated CheckoutRemote remotes = 6;
  string opaque_checkout_id = 7;
}

message CheckoutRemote {
  string name = 1;
  string fetch_url = 2; // rewritten, normalized, credentials stripped
  bool current_branch_remote = 3;
}
```

The descriptor includes branches and tags required by current workspace
semantics. Remote priority follows:

1. current branch's configured remote;
2. `remote.pushDefault`;
3. `origin`;
4. remaining fetch remotes.

The engine uses the descriptor to select mirrors and attempts remote hydration
without holding the client Git lock.

### 7.2 Remote hydration

For each required local tip, the engine checks its candidate mirrors, then
best-effort fetches missing objects:

1. exact SHA, avoiding broad ref mutation;
2. the descriptor's matching named ref when the server rejects SHA fetch;
3. the next candidate remote;
4. client transfer when none succeeds.

Fetching an exact object does not make the remote's ref visible in the local
checkout. Tags are hydrated only when the descriptor says they are visible.
Partial-clone or shallow differences affect where bytes come from, not the
visible result.

### 7.3 `PackCheckoutDelta`

The engine sends:

```protobuf
message PackCheckoutDeltaRequest {
  string checkout_path = 1;
  string expected_state_digest = 2;
  repeated string have_shas = 3;
}
```

The client re-reads state and rejects the request with `STATE_CHANGED` if the
digest moved since `DescribeCheckout`. It verifies each proposed prerequisite
with its own Git, with lazy promisor fetching disabled, and creates a pack
containing the desired local refs minus the valid haves.

The response streams:

- the revalidated checkout metadata;
- the accepted prerequisites;
- a thin Git pack or bundle with prerequisites;
- the worktree patch relative to the same HEAD;
- untracked nested-repository boundaries;
- a digest of the worktree payload.

Graph pack generation and the final HEAD check run under the client's checkout
Git lock. The worktree patch is anchored to that HEAD, but this does not freeze
arbitrary filesystem writes by the user or another process; worktree consistency
is no stronger than today's `PackWorktree`. The engine imports into
scratch/private refs, verifies pack checksums and graph connectivity, verifies
HEAD, and only then advances mirror metadata.

If the client cannot use one or more haves, it omits them and sends more data.
If thin import or prerequisite validation fails, the engine retries without
haves. The final retry is the current full bundle.

### 7.4 Why two client round trips

The engine cannot know useful haves until it has attempted remote hydration.
Holding the client Git lock while the engine accesses the remote would block
local Git for an unbounded network operation. The two-phase protocol keeps the
first exchange small, performs remote work independently, and revalidates state
before transfer.

Poor Wi-Fi pays extra latency for one small round trip but avoids bulk upload.
A bidirectional streaming RPC may carry both phases on one connection without
changing the state machine.

## 8. Engine materialization

### 8.1 Mirror lifecycle

`SharedCheckoutGitMirror` follows `RemoteGitMirror` and
`ClientFilesyncMirror`:

- owns a mutable BuildKit snapshot;
- implements `PersistedObject`, `PersistedObjectDecoder`, and `OnRelease`;
- persists a snapshot link and mirror-key metadata;
- reports mutable usage through cache accounting hooks;
- lazily mounts while acquired;
- serializes mutation with a per-mirror mutex;
- is pruned through ordinary dagql cache lifecycle.

An internal persistable query field selects it:

```graphql
extend type Query {
  """Internal persistent object graph for equivalent host checkouts."""
  _sharedCheckoutGitMirror(
    key: String!
    objectFormat: String!
  ): SharedCheckoutGitMirror!
}
```

This is engine-internal and does not require a public API version gate.

### 8.2 Import transaction

A synchronization acquires the mirror and:

1. copies/fetches remote seed objects engine-locally;
2. imports the client pack under temporary internal refs;
3. validates every advertised visible tip and expected HEAD;
4. records the immutable checkout-state metadata;
5. atomically promotes internal retention refs;
6. releases temporary refs after the immutable result retains its dependencies.

Object writes may remain after a failed transaction; immutable Git objects are
safe garbage. Ref/state publication is atomic.

### 8.3 Repacking and GC

Incremental packs accumulate. Repack asynchronously when either threshold is
crossed:

- pack count;
- loose-object count;
- reclaimable size ratio.

Repack runs under the mirror mutation lock and must not prune objects retained by
pinned checkout-state records. Initial implementation may retain all objects
until the whole mirror is pruned; single-tenant disk growth is preferable to
incorrect early deletion. Reachability-aware pruning is a later optimization.

### 8.4 Canonical visible `.git`

The mutable superset mirror must not become a caller's `.git` directly. Build a
plain immutable repository view containing:

- the descriptor's HEAD and visible refs;
- an immutable snapshot of the mirror's object superset, which may include
  unreachable objects;
- a normalized stat-zeroed index;
- no remotes, credentials, hooks, reflogs, worktree administration, alternates,
  or scratch refs.

The backing mirror can contain extra objects without making its mutable refs or
configuration visible. Retaining those objects in the immutable view is
intentional in the single-tenant engine: filtering or repacking the graph on
every result would recreate the cost this design removes. Build the view as a
copy-on-write snapshot, normalize refs/config/index in a child layer, and never
leave a live path back to mutable mirror storage.

### 8.5 Effective-directory API

An ordinary Dang module cannot select internal core fields. Add the narrow,
version-gated public field that workspace-aware modules need:

```graphql
extend type WorkspaceGit {
  """
  The effective Git checkout for this workspace, including its canonical .git,
  host worktree changes, and pending workspace overlay.
  """
  directory: Directory!
}
```

Its implementation returns:

```text
canonical clean checkout
  + packed host worktree delta
  + workspace overlay
```

The sandbox module then consumes one directory rather than decomposing and
reassembling the checkout:

```dang
let current = ws.git.directory.sync
```

`WorkspaceGit` is already v1-only; register the new field with the repository's
current unreleased-v1 `AfterVersion` gate and cover the base-schema allowlist.
The field is useful beyond sandbox without exposing mirror or transport details.

`WorkspaceGit.uncommitted` remains available and continues to compare the
effective worktree against the pinned/staged HEAD for status, diff, and commit.
`WorkspaceGit.unmanaged` remains the overlay-path set difference used when
exporting or reporting edits Git cannot see.

## 9. Remote access

Remote hydration is automatic and best-effort.

### HTTP(S)

Reuse the existing resolution order:

1. explicit token/header on the operation;
2. trusted parent client credential helper via `GetCredential`;
3. unauthenticated access for public remotes.

Credential values are call-scoped secrets. They are not included in mirror keys
or persisted payloads.

### SSH

Reuse the scoped SSH agent socket and known-hosts machinery. Client SSH aliases,
proxy commands, and VPN-only DNS may not exist in the engine environment; such
failures immediately fall back to client packing. Host-key checking is never
disabled merely to make remote hydration succeed.

### Services and private networks

A remote reached through an explicit Dagger service binding may hydrate through
that binding. A remote that is only reachable from the client's LAN/VPN cannot;
client transfer is the supported path.

### URL handling

The client applies Git URL rewriting before reporting a fetch URL. It strips
userinfo and never reports local filesystem remotes. The engine normalizes URL
syntax before keying so routine spelling differences converge where existing
`gitutil.GitURL` semantics allow it. Distinct aliases that cannot be proven
equivalent remain distinct mirrors.

## 10. Consistency and races

### Checkout changes during synchronization

`DescribeCheckout` returns a ref-state digest. `PackCheckoutDelta` revalidates
it; `STATE_CHANGED` restarts from description, so a pack is never applied to the
wrong HEAD. Arbitrary worktree writes can still race patch generation exactly as
they can today; this design does not claim a filesystem snapshot the client
cannot provide.

### Workspace epoch pinning

The first successful checkout state for a workspace read epoch is retained.
Later host commits do not silently advance that epoch. Export/reload advances the
epoch exactly as today, preserving staged commit and moved-branch checks.

### Force pushes and ref deletion

The mirror only grows objects. New visible-ref metadata can point elsewhere or
omit deleted refs without mutating earlier immutable states. Reflogs are not
part of visible state.

### Concurrent clients

Equivalent clients may update the same shared mirror. Mutations serialize per
mirror, while description, remote probing, and client packing run concurrently.
After acquiring the lock, an importer rechecks whether another client already
supplied its desired objects before writing the pack.

### Object formats

SHA-1 and SHA-256 repositories never share mirrors. Every pack import verifies
that its object format matches the mirror.

## 11. Fallback matrix

| Condition | Behavior |
|---|---|
| no `.git` at checkout root | existing `NOT_A_REPO` behavior |
| unusable `.git` | hard error; do not hide a broken checkout |
| old client lacks negotiation RPCs | current `CheckoutState` + full `PackCheckout` |
| engine cannot reach remote | client delta/full pack |
| remote auth unavailable or rejected | client delta/full pack |
| SHA fetch rejected | named-ref fetch, then client pack |
| local HEAD is unpushed | client thin pack against engine haves |
| client lacks an advertised have | omit it and send a larger pack |
| thin-pack import fails | retry without haves, then full bundle |
| checkout changes between phases | restart from `DescribeCheckout` |
| unborn repository | empty graph + symbolic HEAD; worktree path as today |
| changed submodule cannot be encoded | existing directory/full fallback |
| mirror evicted or corrupt | recreate and retry from remote/client |

## 12. Cache identity and trust boundary

Engines are single-tenant. This design deliberately maximizes reuse inside that
tenant:

- shared mirror identity does not include client ID;
- credentials do not partition object storage;
- local-only objects may remain in a shared graph superset;
- a warm mirror may satisfy a checkout while its remote is offline.

This is broader than a multi-tenant service could safely expose. The engine does
not expose mirror object lookup as a public API, and outward Git refs/directories
remain scoped to explicit checkout state. If engines become multi-tenant, mirror
identity or object authorization must gain a tenant/principal boundary before
this optimization is enabled there.

Secrets, auth headers, SSH sockets, remote configs containing credentials, and
client absolute paths are never persisted in shared mirror state.

## 13. Observability

Add spans and metrics at the transport boundaries:

```text
git.checkout.describe
  refs, remotes, state digest (no raw credentials/paths)
git.checkout.remote_hydrate
  remote, requested tips, hit/miss, transferred bytes
git.checkout.pack_delta
  wants, accepted haves, client bytes, fallback level
git.checkout.worktree_delta
  changed paths, patch bytes
git.checkout.materialize
  mirror hit, checkout snapshot hit, effective-directory time
git.checkout.mirror
  object/pack counts, size, repack/prune time
```

Required counters/histograms:

- client-to-engine Git graph bytes;
- client-to-engine worktree bytes;
- remote-to-engine Git bytes;
- full-pack, thin-pack, remote-only, and fallback counts;
- mirror and immutable checkout hit rates;
- checkout synchronization and materialization latency;
- state-race retries;
- mirror disk usage and repack duration.

Do not attach raw URLs containing credentials, local paths, ref contents, or
pack data to telemetry.

## 14. Validation

### Protocol and Git unit tests

Cover:

- full initialization, no-op update, one-commit update, branch deletion, force
  push, tag changes, detached and unborn HEAD;
- prerequisite acceptance/rejection and full-pack retry;
- corrupt/truncated packs and object-format mismatch;
- graph/worktree state digest race;
- nested repositories and changed-submodule fallback.

### Integration parity

Run the existing host reconstruction and workspace suites through both old and
new clients:

- plain clone and linked worktree parity;
- submodule and `--separate-git-dir`;
- alternates and partial clone;
- staged commit, export, reload, and moved-local-HEAD guard;
- ignored FIFO regression: no whole-root read;
- engine restart persistence and cache pruning;
- public HTTP, private HTTP credentials, SSH agent/known hosts, unreachable
  remote, and service-bound remote;
- two clients with equivalent remotes sharing one mirror;
- a remote-less repository using the opaque checkout fallback key.

### Network benchmark

Shape the client attachable connection and record bytes as well as wall time:

| Case | Link | Expected client graph upload |
|---|---|---|
| clean pushed checkout, cold mirror | 10 Mbps / 80ms | metadata only; graph fetched remotely |
| clean pushed checkout, warm mirror | 1 Mbps / 150ms | metadata only |
| five local commits ahead | 1 Mbps / 150ms | thin pack of local objects |
| 1 MiB dirty worktree | 1 Mbps / 150ms | worktree patch plus local-only objects |
| private reachable remote | 10 Mbps / 80ms | metadata/auth exchange only when pushed |
| engine-unreachable remote | 1 Mbps / 150ms | negotiated client pack; correct fallback |
| no remote | 1 Mbps / 150ms | negotiated/full client pack |

Acceptance targets:

1. A clean pushed checkout with a usable remote sends at most descriptor and
   protocol framing bytes from client to engine for its Git graph.
2. A local-ahead checkout sends no object reachable from accepted engine haves.
3. Warm effective-directory assembly is within 2x of the filesync directory
   baseline on the benchmark fixture, with a stretch goal of parity.
4. Fallback produces byte-equivalent visible refs and tree content to the
   current full-pack path.
5. No test observes a workspace epoch advancing without export/reload.

## 15. Implementation phases

### Phase 0: instrumentation and permanent benchmark

- Add graph/worktree byte accounting to current RPCs.
- Land the controlled workspace benchmark with cold and warm reporting.
- Capture current full-bundle, packed-worktree, and directory baselines under
  shaped networking.

Exit: performance changes are measurable in CI or a repeatable lab target.

### Phase 1: direct effective-directory fast path

- Factor `materializeWorkspaceGitWorktree` so its effective directory is a
  reusable result behind the version-gated `WorkspaceGit.directory` field.
- Apply the workspace overlay directly.
- Switch sandbox workspace assembly from
  `head.tree + uncommitted + unmanaged` to `ws.git.directory`.
- Preserve `WorkspaceGit.uncommitted` behavior for callers that request a
  changeset.

Exit: each worktree delta is applied once; warm assembly approaches the
filesync baseline without a protocol change.

### Phase 2: persistent checkout mirror and client deltas

- Add `SharedCheckoutGitMirror` lifecycle, persistence, accounting, and locks.
- Add `DescribeCheckout` and `PackCheckoutDelta` with state revalidation.
- Seed from the previous mirror generation and transfer only client-side missing
  objects.
- Keep full `PackCheckout` retry and old-client compatibility.

Initially, remote-based keys may still be selected only within one stable
client while the protocol and lifecycle settle; the on-disk form already
supports broader identity.

Exit: a ref move uploads only newly reachable local objects and survives engine
restart.

### Phase 3: automatic remote seeding

- Report sanitized rewritten remotes and tracking priority in the descriptor.
- Reuse `RemoteGitMirror` to fetch desired tips directly.
- Wire HTTP credential helper, explicit auth, SSH agent/known hosts, and service
  networking through existing remote Git setup.
- Negotiate the client pack against remotely hydrated haves.

Exit: a clean pushed checkout sends no graph bytes over the client link when a
remote is reachable.

### Phase 4: cross-client broad sharing

- Remove client identity from remote-keyed checkout mirror selection.
- Share mirrors across equivalent remote descriptors for the whole engine
  tenant.
- Add concurrent-client tests, restart persistence, usage accounting, and prune
  coverage.
- Document and enforce the single-tenant deployment assumption.

Exit: a second client using the same remote benefits from the first client's or
engine's graph hydration.

### Phase 5: compaction and cleanup

- Add asynchronous repack thresholds and retention-safe GC.
- Remove obsolete reconstruction layers after compatibility windows permit.
- Consolidate duplicated remote/checkout mirror object-import code.
- Re-benchmark large histories, many refs, partial clones, and long-lived
  mirrors.

Exit: disk growth is bounded operationally without regressing pinned results or
fallback behavior.

## 16. Alternatives

### Persist only per client

Simpler identity and isolation, and still useful for ref moves. It does not help
a new machine or reconnect to a fresh client identity on poor Wi-Fi. Retained as
a remote-less fallback, not the end state.

### Always clone from the remote

Eliminates client graph upload only for pushed, reachable repositories. It fails
for local commits, offline/private networks, local remotes, and remote outages.
Remote hydration must be a seed for negotiated client transfer, not a
replacement.

### Keep full bundles and rely on immutable dagql caching

This is the current design. It avoids repacking while one state cache entry is
alive but re-uploads full history on a ref change or cache miss and does not
share by repository source.

### Sync raw `.git`

Rejected by [host-git-reconstruction.md](host-git-reconstruction.md): torn
file-by-file snapshots, host-layout interpretation, dangling pointers,
alternates/partial-clone failure, and unstable cache identity.

### One engine-wide object pool per object format

Maximizes deduplication across unrelated remotes. Git pack lifecycle,
reachability, corruption isolation, and eventual multi-tenant boundaries become
substantially harder. Remote-keyed mirrors plus shared checkout mirrors capture
most benefit with ordinary bare repositories. A lower-level content-deduplicated
object store remains possible later.

### Persistent graph without a direct directory

Solves cold bandwidth but leaves the measured warm `uncommitted → Changeset →
withChanges` cycle. Both halves are required for the full performance goal.

### Direct directory without persistent mirrors

Solves warm engine CPU but still uploads full history over poor Wi-Fi on cold or
changed ref states. This is Phase 1 because it is independently useful, not the
end state.

## 17. Code map

Existing pieces:

- current client graph RPCs: `engine/session/git/git_pack.go`, `git.proto`;
- packed worktree delta: `engine/session/git/git_worktree.go`;
- engine wrappers: `engine/engineutil/client.go`;
- canonical reconstruction: `core/git_hostdir.go`;
- host Git cache field: `core/schema/host.go` (`Host.__gitDir`);
- workspace repository and delta path: `core/schema/workspace.go`;
- remote mirror lifecycle: `core/git_remote_mirror.go`;
- remote fetching/auth/network: `core/git_remote.go`, `core/schema/git.go`;
- filesync mirror lifecycle precedent: `core/client_filesync_mirror.go`,
  `engine/filesync`;
- sandbox workspace assembly: `modules/sandbox/main.dang`.

Planned main additions:

- negotiated checkout RPC handlers under `engine/session/git`;
- `SharedCheckoutGitMirror` beside the existing mirror types in `core`;
- internal mirror selector in `core/schema/query.go`;
- immutable checkout/`WorkspaceGit.directory` materialization in
  `core/git_hostdir.go` and `core/schema/workspace.go`;
- the required v1 schema gate and allowlist coverage;
- integration and benchmark coverage in `core/integration`.

## 18. Status

Proposed. The current full-pack and packed-worktree implementations remain the
compatibility and recovery path throughout the phased rollout.
