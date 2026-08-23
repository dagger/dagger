# Engine-persisted agent resume

*Builds on [Resume from trace](./resume-from-trace.md).*

Status: implementation in progress.

## Summary

`dagger agent` will have one resume mechanism: restore agents from a trace.
The engine retains opted-in agent telemetry for a bounded time, and Dagger
Cloud remains the indefinite store.

```console
$ dagger agent -r
# pick a resumable trace retained by this engine

$ dagger agent -r=2f123ba77bf7bd2d4db2f70ed20613e8
# try the connected engine, then Dagger Cloud on a clean local miss
```

The current `--trace` flag and local `llm_id` save files go away. `.resume`
uses the same trace restore path.

Engine archive resume has two phases; Cloud uses the same protocol once it adds
a bootstrap endpoint and may retain today's whole-trace startup until then:

1. Load only the telemetry needed to rebuild every agent, then expose the
   prompt.
2. Stream the remaining historical telemetry into the live frontend in the
   background.

The resumed command gets a new trace ID. Imported telemetry keeps its source
trace ID, and a span link records that the new trace continues the old one.

## Implementation status

The resume-critical runtime substrate is implemented:

- Agent runtimes publish a version-1 checkpoint containing a session-wide
  sequence, identity/name/call digest/parent, latest committed portable snapshot
  digest, projected state, stop reason and error. Publication occurs at identity
  creation and on committed snapshot or projected-state changes.
- Checkpoints enter a lock-free runtime queue, so JSON encoding and OTel
  publication do not run under the agent mutex. A dedicated unbounded session
  log processor drains them through the existing origin/ancestor routing into
  `engine/clientdb`; per-target sequence claims deduplicate the dedicated and
  ordinary log paths. DagUI recognizes the records as control data and does not
  render their JSON bodies as output.
- Graceful agent teardown captures each entry's pre-teardown state, stops the
  runtimes, then publishes one authoritative final checkpoint per registry
  entry. Each final record carries the expected final sequence, providing the
  completeness boundary that archive finalization will verify.
- Core and schema expose transactional batch rehydration. The operation resolves
  every snapshot and handle first, validates the complete set, stages runtime
  construction and durable tombstone leases off-registry, and publishes all
  entries under one registry lock. A validation, acquisition or commit race
  releases staged leases and leaves no partial runtime roster.

The checkpoint consumer and archive lifecycle remain to be implemented. In
particular, no archive manifest, checkpoint/call-payload index, durable
`DB.Checkpoint`, bootstrap sidecar or archive HTTP API exists yet. Finalization
must still validate the expected checkpoint sequence and recursive snapshot
payload closures, and bootstrap must project the verified records into the
batch API. Crash durability, background assimilation, CLI cut-over and Cloud
parity are also pending.

## Goals

1. Make agent resume a property of engine telemetry rather than client-local
   recipe files.
2. Restore every agent's identity, committed conversation and lifecycle state
   before accepting a message.
3. For engine archives, make the prompt available without waiting for
   historical tool spans, logs or metrics.
4. Assimilate the source engine telemetry into the current TUI without changing
   the current trace's root or primary span.
5. Manage local retention by time and a soft disk quota. Use Cloud for
   indefinite retention.
6. Preserve the existing prevalidated restore ordering: resolve every anchor,
   rehydrate every runtime, then attach and focus.

## Non-goals

- Capturing CLI-local spans, the old command root or other telemetry surrounding
  the engine-generated content.
- Indefinite local retention.
- Strong recovery from an engine or host crash in the first version.
- Handing off a live agent loop. Resume reconstructs a new runtime and may fork
  a still-running source.
- Restoring unconsumed mail or awaiters.
- Keeping compatibility with local `llm_id` session files.

## Existing substrate

The implementation already has most of the data and restore machinery:

- The engine writes spans, logs and metrics to one append-only telemetry store
  per client under `worker/clientdbs` (`engine/clientdb`). A main client's store
  receives the telemetry of its nested clients too.
- Closing a session flushes its OTel providers. It does not delete the store;
  periodic GC currently removes inactive stores after one hour
  (`engine/clientdb/store_registry.go`).
- Agent identity spans carry immutable identity, while log records carry
  lifecycle state, stop reason and the latest committed snapshot digest
  (`core/agent_telemetry.go`, `engine/telemetryattrs/attrs.go`).
- Call-payload log records carry the transitive recipe closure needed to rebuild
  a snapshot ID (`core/dag_call_telemetry.go`, `dagql/dagui/extract.go`).
- `TraceImporter` imports foreign OTLP into the live frontend while preserving
  source trace IDs, keeping the live primary span, marking imported roots
  passthrough and sealing unfinished spans (`engine/telemetry/traceimport.go`).
- `RestorePlan` projects imported agent telemetry into rehydration entries, and
  the CLI already resolves all anchors before rehydrating, attaching and
  focusing (`dagql/dagui/agents.go`, `internal/cmd/dagger/restore.go`).

The engine store is therefore sufficient for the resume contract. It need not
be an exact copy of the trace seen by the old CLI.

## CLI

### `dagger agent`

`--resume` keeps its current optional-value form:

```console
$ dagger agent --resume
$ dagger agent -r
# open the connected engine's archive picker

$ dagger agent --resume=2f123ba77bf7bd2d4db2f70ed20613e8
$ dagger agent -r=2f123ba77bf7bd2d4db2f70ed20613e8
# resume an explicit trace
```

A bare flag selects the picker; an attached value is the trace ID. Positional
agent composition cannot be combined with either form because the restored
composition comes from the trace.

An explicit trace ID is resolved in this order:

1. The connected engine archive.
2. Dagger Cloud, only when the engine reports a clean miss.

An active, incomplete or corrupt local archive is not a miss and does not
silently fall back to Cloud.

Bare `-r` initially lists only archives retained by the connected engine. A
future Cloud listing API may add Cloud sessions to the same picker and
deduplicate them by trace ID. Cloud traces remain resumable by explicit ID in
the meantime.

`--trace` and `--partial` are removed. `--trace-timeout` becomes
`--resume-timeout`; it and `--agent` apply after either direct or picker
selection. Bootstrap remains strict. A best-effort per-agent mode can be added
later if strict restore proves too limiting.

Any resume form skips destination workspace module loading and starts from the
same fresh, unbound LLM base that `--trace` uses today. The restored recipes
carry their own workspace and composition.

### Interactive shell

```console
.resume
.resume 2f123ba77bf7bd2d4db2f70ed20613e8
```

`.resume` and `/resume` use the same picker, source resolution and restore
protocol as startup. In v1 they are accepted only while the interactive session
is pristine: it has not spawned, restored or prompted an agent. Otherwise they
fail and direct the user to start a new `dagger agent -r`. This avoids instance
collisions, mixed rosters and ambiguous title/focus replacement. Synthetic
replay contexts are no longer needed.

### Hard cut-over

Delete the local save/load implementation:

- `sessionMetadata`, `getSessionDir` and the `llm-sessions` directory;
- `AutoSaveSession`, `LoadSession`, `ListSessions` and `conflictMarkerCue`;
- `interactivePromptModeOpts.sessionID/resume` and the startup `LoadSession`
  branch;
- save UUID and save-identity bookkeeping;
- `LLMSession.onStep`, its serialization lock, portable-save wait groups and
  `stepped` calls after turns, workspace export and workspace reset;
- local workspace-baseline IDs and replay-only parent-context plumbing;
- the CLI call to `LLM.replay` and both autosave calls to `PortableID`;
- saved-session JSON handling in `cmd/dump-id`, tests, skills and documentation.

The title callback is split from the deleted save callback. The trace restore
startup and `.resume` paths both call `restoreFromTrace`; there is no parallel
local-session branch.

Existing files are neither migrated nor recognized. This is an intentional hard
cut-over.

Title generation remains useful for the live TUI and archive picker, but is no
longer tied to an autosave. The existing title record is CLI-local and never
reaches the engine store, so the title hook also sends a small manifest metadata
update while the archive is active. The manifest is the picker's authority;
updates racing `finalizing` or arriving after `closed` are rejected. An untitled
archive falls back to `Agent session <timestamp>`.

The public `LLM.replay` and `LLM.portableID` fields may be deprecated after the
cut-over. `PortableRecipe` cannot disappear yet: agent snapshot publication
still uses it to produce the digest whose call-payload closure is stored in
telemetry. Retiring it requires a replacement trace-native snapshot encoding,
not merely deleting the local autosave caller.

## Archive model

### Opt-in

Archive retention is a per-session client request in `ClientMetadata`.
`dagger agent` always opts in; other commands retain today's ephemeral behavior.
The archive picker excludes its requester's new trace, and an opted-in session
that closes without publishing an agent identity is discarded rather than
retained as an empty archive.

The client knows the command trace ID before connecting to the engine. It sends
that canonical trace ID with the archive request. The engine binds it to the
session's immutable main client ID. Agent sessions are a single-trace archive
unit: session exporters validate every persisted span/log trace context and every
telemetry-bearing request origin against the canonical trace. Any valid mismatch
marks the archive `incomplete`. Session metrics without a trace context still
belong to the one archive; metrics received from a differently traced request
make it mixed. Read-time span filtering cannot repair a mixed store because
metrics are not independently trace-addressed.

Only the main client store is retained. Retaining nested-client stores would
keep duplicate copies of telemetry already fanned into the main store.

### Manifest

A small versioned manifest indexes the canonical trace ID to the existing main
client store:

```json
{
  "version": 1,
  "generation": "...",
  "traceID": "2f123ba77bf7bd2d4db2f70ed20613e8",
  "mainClientID": "...",
  "boundarySpanID": "...",
  "state": "closed",
  "title": "Investigate cache miss",
  "startedAt": "...",
  "closedAt": "...",
  "expiresAt": "...",
  "sealAt": "...",
  "sizeBytes": 1234567,
  "bootstrap": {
    "file": "...",
    "records": 42,
    "sha256": "..."
  },
  "highWater": {
    "spans": 123,
    "logs": 456,
    "metrics": 7
  }
}
```

The generation is minted once when the archive is registered and never changes.
Manifests are written through a synced temporary file, atomic rename and parent
directory `fsync`, then loaded into an in-memory archive index at engine startup.
Startup changes stale `active` and `finalizing` manifests to `interrupted` before
listing them. Listing reads manifests; it does not reopen every telemetry store.

The bootstrap closure is materialized into its own versioned, checksummed
sidecar during finalization while the store's indexes are hot. Reopening an
evicted clientdb currently scans every stream to rebuild in-memory indexes; a
precomputed sidecar keeps agent bootstrap proportional to resume state rather
than total trace size. Opening and indexing the full store may happen later on
the background path.

The lifecycle states are:

| State | Meaning | Resumable in v1 |
|---|---|---|
| `active` | The session may still append | No |
| `finalizing` | OTel is drained and the store is being checkpointed | No |
| `closed` | Graceful checkpoint and manifest commit succeeded | Yes |
| `interrupted` | Engine restarted before graceful finalization | No |
| `incomplete` | Flush, spill, sync or validation failed | No |

The picker may show active and unavailable entries with their state, but only
closed archives can be selected. Snapshot-and-fork for active archives is a
future extension: capture fixed stream high-water marks and treat that finite
prefix as the source cut.

### Engine archive boundary

The engine store does not contain the CLI root, so its highest retained spans
still point to absent CLI parent IDs. Finalization records which parent IDs are
absent from the complete store. The archive generation owns one persisted
synthetic boundary span ID. Archive export reparents only those true top-level
engine spans to that passthrough boundary. Bootstrap, remainder export and every
retry reuse the same source trace/span context.

This does not recreate the old CLI tree or make it primary. It gives the retained
engine telemetry a coherent imported boundary, a fixed seal point and no
unreceived placeholder roots.

### Resume-critical checkpoints

Graceful close must not infer the final agent roster from bounded presentation
telemetry. Opted-in sessions add a lossless resume-control lane alongside the
existing lossless call-payload lane. A versioned agent checkpoint record carries:

```text
checkpoint sequence
agent ID, name and call digest
parent agent ID
latest committed snapshot digest
projected and pre-teardown state, stop reason and error
```

Identity creation and every committed snapshot/state change enqueue a checkpoint
without blocking the agent mutex. At teardown, after agents stop, the runtime
registry emits one final checkpoint per authoritative entry and records the
expected final sequence. Provider shutdown drains the resume-control and
call-payload lanes. Finalization refuses `closed` unless the main store contains
every expected sequence and every final snapshot's recursive payload closure.

Bootstrap uses the final checkpoint as its authoritative roster/state. It still
imports real identity and ancestor spans for history; if a presentation identity
span is absent, the archive service synthesizes the equivalent ended identity
span from the checkpoint. Thus bounded ordinary OTel queues may lose progress,
but cannot silently omit an agent or select an older valid snapshot from a
gracefully closed archive.

### Graceful finalization

The current session shutdown already stops producers, drains in-flight queries,
stops agents and services, and shuts down the session telemetry providers. After
that telemetry barrier and before publishing shutdown completion, an opted-in
archive is finalized:

1. Mark the manifest `finalizing`.
2. Hold the main client store open.
3. Force all in-memory stream tails into their spill files.
4. Record final high-water row IDs, sizes and the seal timestamp.
5. Validate that every stored span/log trace ID and every trace-attributed OTLP
   request belongs to the canonical archive trace.
6. Build the strict bootstrap closure while the recovered indexes are hot; fail
   finalization if any final checkpoint, agent anchor or payload dependency is
   absent.
7. Write and sync the bootstrap sidecar.
8. Flush and `fsync` all three telemetry files.
9. Atomically commit and directory-sync the `closed` manifest.
10. Release the store and complete session shutdown.

This adds a `DB.Checkpoint` operation. The existing exporters' `ForceFlush`
methods are not a durability barrier: they do not spill or sync the append-only
store. Log append errors must also propagate for opted-in archives instead of
being warned and returned as success.

A successful checkpoint proves that the final lossless agent checkpoints and
call-payload closure accepted by the engine are durable and structurally
sufficient to resume. Bounded presentation queues may still drop ordinary
progress records; local scrollback is intentionally best-effort in v1.

A finalization error leaves the manifest `incomplete`, reports the error during
teardown and does not block engine shutdown indefinitely.

### Crash durability

The first version guarantees only graceful-close persistence. The current store
may keep recent rows in memory and does not fsync during ordinary operation, so
an engine or host crash may lose progress.

Per-step fsync is not a small addition. Snapshot records and their call-payload
closure travel through asynchronous processors and separate store appends; a
plain provider flush cannot prove that a particular checkpoint is complete and
durable.

A later durability phase should add a coalesced checkpoint barrier after a turn
or step:

1. select the committed checkpoint sequence to make durable;
2. flush that checkpoint and its call-payload closure;
3. verify that sequence in the main store;
4. checkpoint and sync the store outside the agent mutex.

If strict per-step durability is required, a dedicated resume WAL may be cleaner
than treating the full telemetry archive as a transactional WAL.

### Retention and quota

Defaults:

- TTL: 7 days from `closedAt`;
- soft quota target: 10 GiB of retained closed archives per engine.

Both are configurable in engine configuration, not per invocation. The quota
counts telemetry, manifests and bootstrap sidecars. Archive listing is paginated
so the number of tiny archives does not create an unbounded response.

GC applies these rules:

1. Never evict active, finalizing or currently-read archives.
2. Delete expired archives first.
3. If still over quota, delete the oldest closed archives by `closedAt`.
4. Reading or resuming an archive does not renew its TTL.
5. The new resumed command creates its own archive and retention period.
6. The newest closed trace is retained even if it alone exceeds the quota;
   older traces are evicted first and the quota violation is reported.

Archive manager references are added to the existing client-store GC keep set,
so the current one-hour ephemeral GC cannot delete retained stores. Eviction
uses the store registry's per-store lock and reference counting; an entry can be
removed from the picker immediately and its files deleted after its final reader
releases them.

The quota manages retained closed archives; it is not a hard bound on active
telemetry growth. An oversized newest archive can temporarily exceed it, so the
engine reports the overage and continues applying its existing filesystem
pressure safeguards.

## Archive APIs

The connected engine exposes authenticated, trace-addressed APIs independent of
the source session's deleted `clientRecord`:

```text
GET  /v1/telemetry/archives
GET  /v1/telemetry/archives/{traceID}/bootstrap
GET  /v1/telemetry/archives/{traceID}/traces
GET  /v1/telemetry/archives/{traceID}/logs
GET  /v1/telemetry/archives/{traceID}/metrics
POST /v1/telemetry/archives/{traceID}/metadata
```

Engines are treated as single-tenant. A current authenticated engine connection
may list and read its archives; trace IDs are identifiers, not bearer secrets.
Nested clients do not receive archive APIs.

The bootstrap endpoint uses a versioned framed protocol rather than one
unbounded response. Its header carries generation and cut; signal frames carry
bounded OTLP span or log batches; the terminal frame carries record counts, a
checksum and the exact exclusion set. The client treats EOF before that terminal
frame as failure.

The full streams reuse the existing binary framed OTLP protocol, cursor headers,
row-to-OTLP conversion and payload limits. Unlike live telemetry subscriptions,
they stop at the manifest's fixed high-water cursor, emit a terminal frame and
do not poll for new rows or depend on `shutdownCh`. Filtered streams advance a
scan cursor across omitted rows and place that cursor on every emitted or
terminal frame, so an excluded tail cannot cause retries to rescan forever.

Archive consumption is strict. The archive importer exposes an explicit
`ImportAndWait` operation: enqueue one batch into the pretty frontend, enqueue a
barrier on the same event loop, and acknowledge the archive cursor only after
that barrier completes successfully. The current tolerant live consumer advances
before its callback and the frontend exporters normally return after enqueueing,
so neither behavior is reused as the archive acknowledgment contract. Transport
goroutines may run independently, but one serialized import queue owns calls
into the frontend exporters.

Cloud implements the same logical source interface. Its transport may remain
OTLP-over-SSE. The frontend importer and restore executor do not depend on which
source supplied the archive.

For explicit resume, archive `not found`, `evicted`, and `API unsupported` are
clean misses that permit Cloud fallback. `active`, `finalizing`, `interrupted`,
`incomplete`, corruption, local I/O failure and transient engine errors are typed
local failures and do not fall back. This keeps local archive defects visible
instead of accidentally hiding them behind a different source.

## Fast resume

### Fixed source cut

Bootstrap and background assimilation must describe the same immutable source
cut:

```text
cut = archive generation
    + span high-water
    + log high-water
    + metric high-water
    + seal timestamp
```

Closed engine archives get the cut from their final manifest. Every retry names
the same generation and cursor bounds; a mismatched generation fails instead of
combining two source versions.

Bootstrap and remainder share one fixed-cut importer and one serialized frontend
queue. Bootstrap completion does not call the current end-of-stream `Seal`.
The importer seals against the manifest's fixed `sealAt` only when the bounded
span remainder reaches its terminal cursor or the client permanently abandons
that signal after retries. A retriable transport error preserves the importer's
unfinished set and cursor. After permanent abandonment no later span retry is
accepted into that importer. Log or metric failure cannot postpone sealing, and
a partial stream cannot choose an earlier seal bound from the records it happened
to receive.

### Bootstrap closure

The bootstrap endpoint returns only the OTLP records required to reconstruct all
agents:

1. The verified final lossless checkpoint for every authoritative agent entry.
2. Every source-trace span carrying valid `dagger.io/agent` and
   `dagger.io/agent.id` identity, using its latest cumulative snapshot. This
   includes long-lived loop spans and the short, ended identity spans emitted by
   `rehydrate` before a restored agent starts; a missing presentation span is
   synthesized from the checkpoint.
3. The parent-ancestor closure of those identity spans, also using latest
   snapshots, so chief/worker relationships and top-level focus can be
   projected.
4. Agent state and stop-reason history attributed to those identity spans, in
   original log order, for historical display and compatibility validation.
5. The recursive call-payload closure of every final snapshot digest.
6. The fixed source cut and the IDs/digests excluded from the remainder streams.

State history remains in bootstrap because control volume is small and the
existing frontend projection consumes those records. The final checkpoint is
the authority: the archive service emits any synthetic identity/state records
needed to make the current projection reproduce its state exactly.

The call closure follows receiver, module and ID-valued argument references in
the same way `dagui.DB.CallIDForDigest` does. Any missing payload makes bootstrap
fail before an agent is rehydrated.

The bootstrap excludes conversation spans, ordinary logs, progress, services,
checks, tests and all metrics. The snapshot recipe is the conversation; these
records are display history, not a prerequisite for sending the next message.

### Required indexes

Current clientdb indexes already support latest span snapshots, ancestor closure
and logs for known span IDs. Add:

```text
(trace ID) -> agent identity span IDs
call payload digest -> payload log row
```

The call-payload index recognizes the reserved instrumentation scope, decodes the
payload and computes its canonical digest. The payload intentionally does not
carry its digest in its body.

All archive indexes should use `(trace ID, span ID)` keys. Some existing indexes
use span ID alone because collisions are improbable; a trace-addressed archive
should not rely on that assumption.

The store also gains fixed high-water and bounded-range reads for the three
streams.

### Bootstrap execution

The client imports bootstrap OTLP into the live frontend's exporters, preserving
source trace IDs. Then it waits for a frontend event-loop barrier before reading
the restore plan.

Execution retains the current ordering:

1. Project every agent from the imported bootstrap.
2. Rebuild every snapshot ID from the call-payload closure.
3. Fail before mutation if any entry or payload is invalid.
4. Rehydrate every agent runtime.
5. Attach every restored conversation.
6. Focus the selected or most recently active top-level agent.
7. Expose the interactive prompt.

Bootstrap is strict. A transport error, missing anchor, missing call frame,
reasonless stop or invalid state fails the command. Rehydration is one
transactional batch engine operation: validate every instance and recipe, acquire
and construct every entry off-registry, then publish the complete set under one
registry lock. A staging failure releases all acquired resources; no runtime is
visible until the whole batch can commit. Attach and focus remain client-side
phases after the batch succeeds.

### Background assimilation

Once all agents are rehydrated, the three remainder streams start independently
in the background and feed the same live frontend exporters.

The remainder is bounded by the bootstrap cut and filters server-side while
advancing its scan cursor:

- Span stream: exclude every snapshot row for a span ID supplied by bootstrap.
  Its latest cumulative snapshot is already present, and replaying an older start
  snapshot would temporarily regress it.
- Log stream: exclude the exact agent-control and call-payload row IDs supplied
  by bootstrap. Ordinary logs attributed to the same identity or ancestor spans
  were not bootstrapped and still stream normally.
- Metric stream: exclude nothing; metrics are background-only because their
  aggregation is not safely idempotent.

The terminal bootstrap frame carries these row IDs and span IDs. Call-payload
digest deduplication in the frontend remains a second defense, not the cursor
protocol.

Each signal has its own cursor and retry lifecycle. A failed background stream
does not cancel the other streams or the resumed agents.

When background assimilation fails:

- keep the prompt and restored agents usable;
- keep every historical record already imported;
- show one persistent notice: `Previous session resumed, but some historical
  progress could not be loaded.`;
- attach the detailed error to the restore span/debug logs.

The first version does not switch a partially loaded engine archive to Cloud.
Cloud fallback happens only on a clean engine miss before bootstrap.

## Trace continuity

The resumed command always starts a new live trace. Imported source spans and
logs retain their original trace ID. This is required by the current restore
projection: an agent is foreign until it publishes an identity span in the live
trace, after which repeated restore skips it.

After bootstrap identifies the source agent identity contexts, the client
creates a short bridge span in the live trace with links to the restored
top-level agent's newest identity context or contexts:

```text
resume agents
  link -> source agent identity
    dagger.io/link.purpose = "continuation"
```

The purpose must be explicit. DagUI treats an empty-purpose link as causal and
would add tree relationships. The new `continuation` purpose is lineage
metadata: it does not reparent either trace or propagate status.

The bridge span also records the source trace ID and archive source (`engine` or
`cloud`) as attributes. Imported history is sent directly to frontend exporters,
not through global telemetry processors, so it is not uploaded again as part of
the new trace.

## Failure semantics

| Failure | Behavior |
|---|---|
| Engine archive not found | Try Cloud |
| Engine archive present but not `closed` | Fail with its state; do not fall back |
| Bootstrap transport or decode error | Fail before rehydration |
| Missing agent anchor or call payload | Fail before rehydration |
| Batch rehydration validation/install error | Fail without creating a runtime |
| Background span/log/metric failure | Warn and continue the resumed session |
| Archive expires during a read | Reader lease completes; deletion waits |
| Graceful finalization fails | Mark archive incomplete and finish shutdown |

## Implementation plan

1. **Resume-critical records and indexes (in progress)** — the lossless
   checkpoint producer, final roster and sequence barrier are implemented;
   agent-identity/call-payload indexes and trace-scoped latest/ancestor/range
   reads remain.
2. **Archive lifecycle (pending)** — add the agent opt-in, archive config,
   manifest index, graceful `DB.Checkpoint`, bootstrap sidecar, archive-aware GC
   and listing.
3. **Finite streams (pending)** — factor the live telemetry framing into strict
   static, trace-addressed archive streams bounded by manifest cursors.
4. **Bootstrap protocol (in progress)** — the transactional batch-rehydrate
   engine operation is implemented; fixed-cut bootstrap import, verified
   checkpoint projection, attach and focus remain.
5. **Background assimilation (pending)** — stream excluded remainders
   independently with cursors, retries and nonfatal TUI failure reporting.
6. **CLI cut-over (pending)** — move trace IDs to `--resume`/`.resume`, add the
   engine picker and engine-first/Cloud-second resolution, and delete `--trace`
   plus all local save/load code.
7. **Trace link (pending)** — emit the custom-purpose continuation bridge span.
8. **Cloud parity (pending)** — implement the archive source interface for
   indefinite traces; until Cloud has a bootstrap endpoint, its adapter may pay
   the current whole-trace startup cost.
9. **Durability follow-up (pending)** — add coalesced verified turn/step
   checkpoints if graceful-close persistence proves insufficient.

## Validation

- Gracefully close an opted-in agent session, restart the CLI against the same
  engine, resume by trace ID and continue every restored agent conversation.
- Assert bootstrap contains all authoritative final checkpoint sequences, agent
  instances, teardown state history and the exact recursive payload closure, but
  no ordinary tool logs or metrics.
- Saturate ordinary OTel queues before graceful close and verify the lossless
  final checkpoints still restore every latest committed snapshot.
- Assert the prompt becomes available before a blocked background stream
  completes.
- While the prompt is active, release historical span/log batches and verify
  scrollback and individual tool-call details appear without duplicate output,
  state regression or metric inflation.
- Kill one background stream and verify agents remain usable, the other streams
  continue and one persistent warning appears.
- Verify source and live trace IDs remain distinct, the continuation link is
  present, and imported telemetry is not re-exported to Cloud.
- Verify all anchors are resolved before the first rehydration and every agent is
  rehydrated before the first attach or message.
- Verify graceful close spills and syncs the store before the manifest becomes
  closed; a failed checkpoint produces an incomplete, unselectable archive.
- Exercise TTL expiry, quota eviction, reader leases, newest-oversize behavior
  and coexistence with the existing one-hour ephemeral GC.
- Verify bare `-r` lists only the connected engine initially, excludes its own
  new archive, and explicit resume falls back to Cloud only on a clean engine
  miss.
- Verify `.resume` succeeds only before the session has started agent work.
- Restore an engine-only capture and verify the synthetic passthrough boundary
  removes dangling CLI-parent placeholders without becoming the live primary.
- Interrupt bootstrap before its terminal frame and verify no runtime is created;
  retry filtered remainder streams and verify scan cursors cross excluded tails.
- Verify old UUIDs and JSON files are not accepted and that `LoadSession`,
  `AutoSaveSession`, `ListSessions`, `getSessionDir`, `conflictMarkerCue`, save
  UUIDs and portable autosave hooks no longer exist.
- Verify generated help and docs contain no `--trace`, saved-session ID or local
  `llm_id` resume wording, and no resume path calls `LLM.replay` or the public
  `LLM.portableID` field.
