# Trace-native agent resume

Status: implementation in progress on
[`vito/dagger:trace-native-agent-resume`](https://github.com/vito/dagger/tree/trace-native-agent-resume),
replacing [#14193](https://github.com/dagger/dagger/pull/14193) and
[#14197](https://github.com/dagger/dagger/pull/14197). The checklist below records
implemented and verified work. The numbered sections retain the approved design
and historical investigation; they are requirements, not a blanket completion claim.

## Scope correction

Repository-history embedding is rolled back in a follow-up commit, without
rewriting the implementation history. `Workspace.snapshot` again retains
`Host.__gitDir` for no-usable-remote/local-filesystem-remote captures, matching
upstream's session-dependent behavior. It no longer puts every local branch and
tag's history into literal trace payloads. The associated origin-metadata fix and
fresh-server full-history tests are removed. Remote-backed capture is unchanged.

`Workspace.withCommit` patch normalization is retained: the selected merged delta
and pending remainder no longer replay their original Changeset producers. This
does not make a client-dependent base repository portable. Tests now distinguish
that boundary. Earlier local-history portability results below are historical,
not current guarantees; this scope decision supersedes conflicting requirements
in the historical design sections.

Cloud resume is supported again: try the connected engine first, then transparently
fetch the whole Cloud trace when the archive is absent, evicted, or its endpoint
is unavailable before bootstrap import. Validate the observed latest canonical
agent/subscription records, source namespace, complete recipe closure and graph
before creating runtimes. Cloud downloads do not supply an independent final
roster witness, so this path does not claim archive finality or bootstrap-first
startup. Explicit engine generations, ambiguity, authorization failures,
corruption, incomplete local archives, and failures after bootstrap begins do not
silently switch sources. `--source-session` can disambiguate either source;
`--generation` is an optional engine-only pin and requires `--source-session`.

## Implementation checkpoint

Commit references in the original checklist name the pre-rebase implementation
commits; the continuation commits are listed in the session handoff below.

Session handoff: draft [PR #14298](https://github.com/dagger/dagger/pull/14298)
was rebased onto upstream `main` at `0ceaef6`, preserving upstream OAuth refresh
while resolving the two CLI conflicts, and published with an approved exact lease.
The former CI load conflict is resolved. Continuation commits add archive discovery
and exact selection (`df0e920`), fix rebased lint failures (`5c2a72d`), and preserve
sanitized Git origin metadata in immutable local snapshots (`abe79fb`).

Post-rebase validation: the complete `test-split:test-llm` and
`test-split:test-workspaces` check groups pass locally, as do `golangci-lint:lint-all`,
`golang:generate-all:up-to-date`, and Markdown lint/fix checks. The actual engine
restart, CLI restore, and four workspace commit cases pass again. CLI discovery
and selected-generation restoration also pass with a broken destination module.
Focused CLI/archive/control/server/storage race tests pass. A full CLI package
race run outside the engine harness failed because it lacked a `dagger` executable
(and the release download returned 403); this is not a full-suite pass claim.

- [x] Canonical typed, revisioned agent/subscription control, creation publication,
  retained capture leases, strict dependency checks, independent close witness,
  and protected payload/control delivery (`4255c9a`, `51aa5a7`, `158b585`,
  `0a9f24e`). Producer suites have focused race coverage.
- [x] Canonical live roster and inert whole-graph restoration with frontend
  application acknowledgment (`1813d56`, `0586867`, `b69faed`). Actual engine
  tests pass for dormant/paused/failed/explicitly stopped agents, repeated failure
  preservation, real notification filters/replacements/removals, and no synthetic
  historical completion (`61e51c0`).
- [x] Persistent verified bootstrap and original history import (`fd7210a`).
  `TestArchiveSurvivesEngineRestart` stops the actual engine process and starts a
  different process over the same state volume; authenticated bootstrap and a
  continued recorded turn pass without fetching historical streams (`8cf9e4d`).
  This passed again after archive composite identity (`de6e598`).
- [x] Trace-authoritative CLI initialization without destination model/provider
  lookup (`7dec76e`). Actual `TestCLIArchiveResumeIgnoresDestination` passes:
  missing destination module, no destination provider configuration, continued
  prompt turn, no implicit export, and untouched legacy JSON sentinel (`79a467b`).
- [x] Repository-history embedding from `0cc698c` has been rolled back. Existing
  session-local snapshots remain usable while the source session is connected;
  strict agent capture reports their `Host.__gitDir` dependency as unsupported.
  No all-repository-history portability guarantee is made.
- [x] Local JSON persistence/picker/restore and public `portableID`/`emitHistory`
  removed (`4fbfde7`, `1176673`); obsolete TUI QA JSON mount removed (`df25dc7`).
  Public GraphQL schema and Go, Python, TypeScript, Rust, PHP, Elixir SDKs generated
  successfully (`4aa2649`); combined integration/CLI compilation passes.
- [x] Same **trace ID across multiple source sessions**: archive identity now
  includes source session and generation; plain ambiguous lookup fails explicitly,
  selected bootstrap/lease APIs avoid mixing sessions (`de6e598`). Registration
  failures cannot suppress live canonical telemetry (`f6bacfb`). Producer
  ambiguity, pagination, reopening and lease tests passed under the race detector.
- [x] Cursor-aware history retry importer (`df7ead8`) and actual span/log/metric
  callback cursors wired through the CLI (`8166a10`). Targeted importer retry,
  seal retry, CLI bootstrap barrier and background-history tests pass under the
  race detector; acknowledgment retries do not enqueue duplicate history.
- [ ] Same **runtime session with agents created under multiple trace roots** is
  still unsupported for strict restoration. Composite archive identity does not
  solve this separate registry/graph/history problem; do not infer completeness.
- [x] Minimal CLI generation selection/listing: `--list-archives` reads retained
  metadata, optionally filtered by `--trace`; `--source-session` selects a source
  namespace, with optional `--generation` pinning an exact engine cut. Invalid
  combinations fail before engine work;
  ambiguous reads guide the user to discovery. Unit race coverage includes
  pagination, safe title rendering, selection, and errors; actual CLI discovery
  and selected restore pass without loading the destination module or provider.
- [x] Final CLI reference help regenerated with the from-source docs generator;
  `golang:generate-all` and its up-to-date check pass. `docs:references` also
  succeeds after the rebase with no schema drift.
- [ ] Refresh remaining module SDK snapshots against the from-source engine.
  Publishing the rebased SHA fixed the earlier unresolved remote-dependency error,
  and `go-sdk:generate` / `dang-sdk:generate` then ran successfully, but inspection
  showed they used the hosting engine's older API (including removed LLM methods)
  and rewrote local dependencies to remote SHA URLs. That output was discarded;
  successful execution is not evidence of correct module SDK generation.
- [x] Local-Git capture/commit tests retain frozen-file/history/author assertions
  and now assert that strict capture rejects their session-local history rather
  than requiring its conversion into a portable bundle.
- [x] Committed-local-Workspace capture no longer retains the original live
  incoming Changeset in either the commit recipe or pending remainder (`3db0f6c`).
  Capture materializes the selected resolved delta after the original eager
  three-way merge, preserving author, pending-edit and conflict semantics.
  Actual engine tests pass for `TestWorkspaceWithCommitFreezesHostAndAuthor`,
  `TestWorkspaceWithCommitIncomingChanges` (clean and unrelated-dirt cases),
  `TestWorkspaceWithCommitMergeConflicts` (all three variants), and
  `TestWorkspaceWithCommitScopedHistory`. Producer partial/all-commit tests use
  an explicit immutable fixture base and preserve the absence-of-producer-call
  assertion, independently of local snapshot portability.
- [ ] Broader acceptance/performance work in §13 remains. No constant-time startup,
  crash-completeness, Cloud finality parity, or end-to-end latency claim is made.
  Internal recipe flattening remains; unsupported live dependencies fail capture.

**One authoritative agent telemetry model, indexed for bootstrap-first restore.
The trace owns the agents, their Workspaces, and their notification relationships;
the client's checkout does not.**

This supersedes conflicting requirements in [resume-from-trace.md](resume-from-trace.md),
particularly whole-history-before-prompt startup, dependence on the destination
checkout, omission of notification subscriptions, and continued local JSON session
persistence. That document remains historical context for existing code references.
The lifecycle and capability principles in [async-agents.md](async-agents.md) and
[agent-messaging.md](agent-messaging.md) still apply unless explicitly revised here.

## 1. Decision and scope

Replace both PRs as designs, while reusing their useful implementation work:

- Replace #14193's second, JSON-encoded checkpoint stream with one authoritative,
  revisioned agent telemetry contract consumed by both the live UI and archives.
- Preserve #14197's persistent engine archive, verified fixed-cut bootstrap,
  frontend application barrier, and asynchronous historical import.
- Restore actual `notify` subscriptions, not just parent metadata.
- Make snapshot-backed recipes, rather than the current client Workspace, the
  authority for every restored agent.
- Remove the old local-file JSON autosave/restore path and its public
  `LLM.portableID` and `LLM.emitHistory` dependencies. Older branches call the
  latter history-reemission API `replay`; audit actual callers when implementing.
- Treat internal LLM recipe flattening as a removable implementation technique,
  not a requirement of the archive protocol. Close capture/laziness gaps before
  substituting raw committed recipes.

A fresh implementation should be based on current main, not a mechanical rebase
of the old checkpoint protocol. It may be reviewed in slices (§12), but the
end-to-end acceptance criteria cover the whole design. No new timing numbers or
crash-completeness guarantees are asserted by this proposal.

## 2. Evidence and corrections to the earlier design

The investigation used these revisions:

| Source | Revision |
| --- | --- |
| upstream main | `5832caf8fe74bf060f866f67656cf185314e4206` |
| #14193, `extract/agent-checkpoints` | `46616cbc2803e39f5a09b184c2ef691c1d65576f` |
| #14197, `extract/engine-trace-archives` | `2813bb134d60f1a4a34ab0dd8ae249f6ed052789` |
| `llm-workspace-dev-env` | `93a85205` |

These are investigation baselines, not a requirement to implement against those
old trees. Source paths below refer to main unless a branch is named.

### 2.1 Payload transport improved; agent control records did not

PR #14189 has merged. Call-payload records have dedicated ingress and retries and no
longer share the ordinary bounded log queue. Agent state and snapshot-digest
records still do: `engine/server/session.go`, `engine/telemetry/logbatch.go`, and
`core/agent_telemetry.go` distinguish these classes. The ordinary queue has 2,048
slots at the inspected revision.

Thus the old queue-overflow argument is obsolete for call payloads, but not for
agent control records. Having a recipe does not prove which recipe the runtime
last committed, or that its final lifecycle update arrived. Nor is the payload
lane absolutely durable: it has finite retries and shutdown limits. Archives must
verify the referenced payload closure and successful finalization, not equate
protected ingress with durable storage.

### 2.2 The fast path is bootstrap first, history later

The old workspace branch and #14197 implement:

1. Fetch a small, prebuilt bootstrap at a fixed persisted cut.
2. Import it and wait for the frontend to apply it.
3. Resolve all required anchors and restore all agents.
4. Attach and focus the prompt.
5. Import historical spans, logs, and metrics in the background.

See #14197's `internal/cmd/dagger/restore.go`. The old branch's
`restore_test.go` tests that blocked historical loading does not block the prompt.
The checkpoint JSON format is not the source of the speedup. Cloud fallback in
those implementations still fetches the entire trace synchronously; current main's
agent trace restore also waits for the whole Cloud trace.

### 2.3 Today's telemetry is close, but not a complete latest-record contract

`dagql/dagui/agents.go` currently derives:

| Fact | Current source |
| --- | --- |
| Handle and name | Identity/loop span attributes |
| Committed conversation digest | Snapshot log record |
| State and stop reason | State log record |
| Pre-teardown state | Earlier state-log history |
| Parent | Span ancestry |
| Failure text | Loop span status |

State and snapshot records are independent updates. They are attributed to spans,
not self-contained handle-keyed records. Taking the latest arbitrary record loses
information. Arrival-order ingestion also allows old imported records to overwrite
newer state.

Arbitrary changes to a long-lived span's attributes do not stream live: the live
processor publishes a snapshot at start, not every mutation. A final span export
can contain later attributes, but that does not make mutable span attributes a
sufficient live control protocol.

PR #14197 treats checkpoint JSON as authoritative, then repairs or synthesizes ordinary
agent telemetry from it in `engine/server/archive.go`. This duplicates lifecycle
truth and reconciles two representations. The replacement removes that duplication,
not merely the JSON syntax.

### 2.4 Parent identity does not restore notification subscriptions

`AgentRuntimes.Notify` is the writer of the watched runtime's `subs` map. Core
`spawn` does not automatically subscribe a parent. A staff wrapper may call
`worker.notify(subscriber: chief, on: [IDLE, FAILED])`, but that is an ordinary
subscription, not an implication of lineage.

PR #14193's `parentHandle` populates `checkpointParentAgentID`; its checkpoint has no
subscription edges, filters, or delivery state. Restoring the parent relationship
therefore does not fix missing end-of-turn notifications. Both endpoints can exist
and be addressable while the notification edge between them is absent.

### 2.5 Snapshotting and lazy tools weaken the case for mandatory flattening

`composeAgents` snapshots the Workspace before composition. `withTools.object` is
`LazyRef`: recipe loading reconstructs the receiver but skips evaluating the bound
object. The schema resolver derives the object's type/provenance and records a lazy
binding; dispatch loads the object. Same-type rebinding replaces the earlier value.
Consequently:

```text
withTools(slowThing).withTools(fastThing)
```

need not execute either object during restore; subsequent dispatch uses `fastThing`
when both objects have the same type. Different types remain independently bound.
The superseded recipe still contributes to the dependency closure and identity,
and schema/module resolution can do work. Handle/type-resolution fallback can be
eager, and argument construction may have evaluated an object before supplying its
ID. These qualifications do not negate the lazy-reference guarantee.

Focused DagQL lazy-reference tests passed during investigation, including a
fresh-server recipe reload asserting zero executions of a side-effecting lazy
argument. This was not an end-to-end test of raw-recipe agent restore.

Current gaps remain: startup snapshotting can fall back to a live Workspace;
`Workspace.snapshot` also has unsupported/no-Git fallbacks; later `@` references
can introduce direct host mounts; and `withWorkspace`, `withSkills`, and
`withMCPServer` have eager loading paths. Loading a service object does not itself
prove the service starts.

`PortableRecipe` currently prunes superseded bindings and bounds reconstruction
work. It carries the surviving Workspace recipe verbatim, however, and cannot make
an unsafe surviving dependency portable. Some comments/tests describe older
live-checkout startup and save/rebind behavior too broadly. Snapshot-at-capture is
the fundamental correctness technique; flattening is not intrinsically necessary
if historical dependencies are immutable and references appropriately lazy.

## 3. Required behavior and non-goals

### 3.1 User-visible contract

`dagger agent --trace <id>` restores a new set of runtimes from a source trace:

- The trace's committed conversations and Workspaces are authoritative.
- All required agents, including dormant and explicitly stopped agents, exist
  before any restored agent can execute a tool against another.
- Notification relationships and their configured state filters are restored.
- The prompt becomes usable without waiting for unrelated historical telemetry.
- Original imported telemetry supplies scrollback. We do not re-emit history as
  new conversation spans.
- Incomplete restore-critical data produces an explicit error, not an older
  snapshot presented as current, an empty seed, or a client-checkout fallback.
- Restoring does not automatically spend model tokens or continue interrupted
  execution. An explicit user action starts work.

An agent still running in the source session is not handed off. Restoring a
supported cut forks new runtimes in the destination session. Stable handles identify
agents within their session; source-session identity distinguishes historical
telemetry from new runtime incarnations.

### 3.2 Guarantees deliberately not claimed

- Crash-complete recovery from an engine/process failure. This design verifies
  gracefully finalized archives. An unsealed trace is not silently promoted to a
  complete archive.
- Exactly-once replay of pending mailbox messages or lifecycle events. Messages
  incorporated into a committed conversation are retained by its recipe; pending
  messages and messages dequeued but not yet committed remain outside the initial
  restore guarantee. §6 defines future notification continuity without replaying
  historical completions.
- Executable restoration of synchronous `LLM.loop` calls that never created an
  agent. Their historical telemetry remains viewable.
- Instant startup independent of conversation size. The required recipe closure
  may be large. The guarantee separates necessary reconstruction from unrelated
  historical UI data, rather than promising a constant-size bootstrap.
- Availability of expired external content, secrets, or credentials merely because
  a recipe exists. Required dependencies and missing capabilities must be checked
  and reported; the client checkout is not a recovery source.

## 4. One authoritative agent telemetry model

### 4.1 Identity and state

Introduce a versioned control contract within the existing telemetry plane. For
small scalar agent facts, use typed OTLP attributes, not an opaque JSON document in
a log body. Existing identity spans remain useful diagnostic/UI structure, but
must not be a second authority for mutable restore state.

A semantic agent projection contains:

| Field | Meaning |
| --- | --- |
| Source session/trace and runtime incarnation | Namespace and ordering domain |
| Stable agent handle | Agent identity within that namespace |
| Revision | Monotonic, producer-assigned committed projection revision |
| Name | Display label, not identity |
| Parent handle, if any | Explicit lineage; independent of subscriptions |
| Conversation digest | Runtime's last committed reconstructible LLM recipe |
| Snapshot capture status | Referenced committed conversation captured, or explicit failure; not a persistence acknowledgment |
| State | Projected lifecycle state |
| Stop reason and pre-teardown state | Distinguish dismissal from session cleanup |
| Failure information | Preserved even when a restored failure has no new loop |
| Last activity | Producer fact for focus selection, not import arrival time |

The implementation should replace the current split mutable-state authority with
a coherent projection per committed runtime revision. Identity spans may repeat
immutable facts for observability. Do not indefinitely emit old mutable logs plus
a new competing checkpoint stream and reconcile them later. Compatibility adapters
for historical traces must be explicitly versioned and must not invent completeness.

Use the same projection/folding rules in the live roster, archive index, and restore
planner. The index is a materialized view of telemetry, not a second writable agent
database or a new global agent-discovery API. Visibility remains capability/client
scoped.

### 4.2 Publication and coherence

Publish at creation, committed conversation advances, relevant lifecycle/error
changes, and teardown. Creation includes fresh agents that have never started a
loop. Subscription changes have their own control records (§6).

At a completed runtime mutation, capture immutable state and the corresponding
conversation reference, and assign a revision under the runtime lock. Retain the
necessary lease while deriving/encoding its recipe outside that lock. Preserve
revision association even if asynchronous derivations complete out of order.

An unsuccessful capture must mark that revision unrestorable. Never attach a
cached earlier digest to a newer committed conversation and call it complete. A
state-only revision may reuse a capture of the same committed conversation; a new
projection revision does not itself require a different recipe. Capture success
establishes that association, not that every referenced payload or content object
has persisted. Finalization verifies persistence and closure separately (§8).
If processing coalesces superseded revisions, successful finalization must still
prove that the latest required projection and its conversation were captured and
persisted.

This also avoids claiming that moving JSON serialization out of the lock solves
the expensive part: the old PR still constructs `PortableRecipe` under the runtime
mutex. Recipe work and export must leave that critical section, without introducing
lease-release, publication, or state/snapshot races.

### 4.3 Protected delivery and persistence

Restore-critical control records must bypass the ordinary bounded/drop-on-overflow
queue. Use reliable ordered delivery with explicit persistence outcomes, excluding
those records from duplicate ordinary processing. The precise processor reuse and
backpressure policy are implementation details; unbounded memory growth is not a
new durability guarantee.

Settle per-target delivery claims after successful persistence and release failed
claims for retry. #14193 claims checkpoint targets before the DB write and does not
release them; its old batch implementation also discards failed batches. Do not
carry that mechanism into the replacement.

If retries or shutdown deadlines are exhausted, keep the archive unsealed and
surface the failure. A valid archive cannot be declared from a best-effort drain.
Call-payload closure must be available through verified persisted storage, even
when live spans also carried those payloads through bounded queues.

### 4.4 Indexing and history isolation

Index by source namespace and agent handle, applying only newer revisions within
a runtime incarnation. Do not compare revisions from different sessions as if they
formed one global sequence. Imported source facts must not overwrite destination
runtime facts after rehydration.

A bootstrap installs a projection at a known cut. Historical import may populate
old spans/logs for display, but must not regress that projection. Use revision-aware
application and/or explicit exclusion of control rows already folded into bootstrap.
Tests must cover older control records arriving after new live agent activity.

## 5. Lifecycle restoration

Restore every agent in the verified visible roster, not just agents with loop
spans. Preserve explicit parent handles for focus and lineage, without depending
on importing the full ancestor span tree. Preserve original failure text across
successive restores.

| Source state | Destination state |
| --- | --- |
| `IDLE` | `IDLE` |
| `PAUSED` | `PAUSED` |
| `FAILED` | `FAILED`, with error |
| Explicit `STOPPED` | `STOPPED` tombstone; committed snapshot remains readable |
| Session-cleanup `STOPPED` | Map the explicitly recorded pre-teardown state |
| `RUNNING` or `WAITING_INPUT` at a supported cut | `IDLE`, retaining the committed conversation; no automatic continuation |

Missing/unknown state or ambiguous stop semantics must not be guessed in strict
restore. Graceful cleanup must record pre-teardown state explicitly; taking only
the final `STOPPED` record cannot reconstruct it.

Runtime creation and graph installation are distinct phases. Restore in parent-first
order where the API requires it, but do not activate anything until the whole
required graph is installed. On a failure partway through rehydration, keep the
prompt disabled and clean up newly created runtimes; do not leave a partially
usable session advertised as restored.

## 6. Notification subscriptions are restore state

### 6.1 Record the actual graph

Represent subscription state independently of lineage:

```text
(source namespace, watched handle, subscriber handle)
    -> selected lifecycle states, subscription revision, presence/removal
```

A subscription update replaces the selected state set, matching today's idempotent
per-subscriber `notify` behavior. Publish changes as typed control records, including
empty/removal semantics so an older subscription cannot reappear during import.
These records belong to the same protected telemetry/index/finalization model as
agent projections; do not hide a second subscription JSON database in the archive.

Persist arbitrary edges and filters. Inferring `[IDLE, FAILED]` from a parent link
would lose custom filters and non-parent watchers and could subscribe an agent
that was never subscribed. Validate endpoints against the visible restore set;
never expose or resurrect agents outside the caller's authorized scope.

### 6.2 Restore without generating historical completion events

After all endpoint runtimes exist, install subscriptions before enabling any
restored work. Do not restore them by blindly calling the public `notify` API:
it performs an immediate level check and can enqueue an old completion, wake the
chief, and spend tokens merely because restore occurred.

The proposed restore-only installation path sets filters and initializes edge
bookkeeping against the destination runtime's mapped state and restored committed
conversation, without publishing a synthetic historical event. Future real
transitions then use normal notification semantics, including the existing rule
that an `IDLE` event requires newly committed work. The exact bookkeeping must be
validated against that rule; mapped lifecycle state alone is not a delivery
watermark. Public `notify` keeps its immediate level check for genuinely new
subscriptions.

This is **future-notification continuity**, not exactly-once delivery across the
shutdown boundary. Notifications already incorporated into a committed conversation
remain there; other events/mail remain subject to §3.2's exclusion. In particular,
a worker completion awaiting delivery at shutdown is not recovered merely by
restoring its subscription. If pending-event recovery is added later, it needs
event identities, mailbox persistence, and consumption watermarks as one design,
not a replay of all current subscribed states.

A regression test must restore a chief/worker setup and demonstrate that a new
worker turn notifies the chief with the configured filter, without a completion
being synthesized solely by restoration.

## 7. Workspace authority and recipe portability

### 7.1 Capture state, do not rebase it onto the client

The Workspace embedded in each agent's committed trace snapshot is authoritative.
`--trace` must not load destination agent modules, compose destination agents,
rebind restored agents to `CurrentWorkspace`, or merge local edits into them.
Initializing the client Workspace is allowed for client-side operations; it does
not make it an input to the restored agent graph.

Original frozen workspaces, pending overlays, tool/module bindings, and conversation
data must reconstruct from the trace's recipes and their durable content
references. Missing required data is an error, not a request to fall back to the
client checkout. This applies when starting restore and to subsequent implicit
fallbacks such as `.clear`. Reset/clear may use traced state but must never silently
substitute the destination Workspace.

Export remains an explicit outbound operation into the client checkout. Export
comparison/conflict bookkeeping must not become authority for the agent's state.
The old independently serialized local-save baseline is not a reason to retain
JSON saves or consult destination state during restore. Model reset state and export
bookkeeping separately so advancing an export baseline does not implicitly redefine
what the agent restores or clears to.

Current Ctrl+U/`ResetWorkspace` is an explicit inbound reload of the client checkout,
not automatic restore leakage. Whether to retain that command for restored sessions
is a separate UX decision (§14); if retained, it must be an explicit user-directed
state change, not part of `--trace` reconstruction.

### 7.2 Strengthen capture at every ingress

Audit initial composition and subsequent bindings, not just the session's first
Workspace:

- Initial Workspace capture, including no-Git/unsupported cases and large-content
  failures that currently return a live Workspace.
- Prompt `@` paths and later host-mounted directories/files.
- Workspace replacements and returned tool objects.
- Skills, service/MCP bindings, module definitions, and continuation-produced LLMs.
- Engine-local handles crossing into a recipe that must survive its source session.

For the trace-native path, do not silently label a live fallback as a durable
snapshot. Prefer a supported immutable capture; where unavailable, fail capture
clearly and prevent a strict resumable archive from being sealed. Merely eliminating
handle syntax does not establish independence from live client state.

### 7.3 Remove flattening only after proving the stronger invariant

The protocol stores a committed conversation digest and its verified closure. It
does not specify that the digest must come from `PortableRecipe` or that a public
portable-ID query must be evaluated on every step.

An ordinary materialized committed recipe is an acceptable anchor once its
dependencies satisfy the snapshot invariant. It must describe committed data, not
re-run the LLM's step or infer the latest conversation from arbitrary descendant
spans. A span may describe an uncommitted intermediate response or unrelated LLM
work; the runtime is the authority on its committed tip.

During implementation, retain existing internal flattening where necessary until
raw-recipe tests pass. This is a temporary safety measure, not justification for
retaining public `portableID` or local JSON persistence. Audit still-eager binding
paths separately; do not delete flattening and assume `withTools` laziness covers
`withWorkspace`, skills, or services.

After correctness is established, measure dependency closure size, recipe
materialization, and reconstruction cost. General recipe pruning/laziness or
optional compaction may be worthwhile, but should not introduce a second session
serialization format. A superseded lazy recipe can cost storage/schema work without
executing its object constructor.

## 8. Archive construction and graceful finalization

### 8.1 Persistent, scoped storage

Reuse #14197's persistent archive and call-payload index concepts. An archive holds
append-only telemetry, the materialized control projection, and a verified bootstrap
for a particular generation/cut. It must survive engine process restart and worker
reset, enforce retention, and protect active readers from eviction.

Archive APIs must authenticate newly connected clients before executable queries,
and enforce the same visibility boundary as their telemetry. Subscription edges,
parent relationships, and final manifests must not leak another client's agents.
Do not apply a registry-global sequence to a client-visible subset and interpret
legitimate visibility gaps as loss.

Use configurable TTL/quota retention. The old PR's seven-day/10-GiB defaults are a
starting point to reassess, not correctness requirements. Active-reader leases and
clear expired/evicted errors are requirements.

### 8.2 Verified close boundary

A successfully closed archive requires this ordering:

1. Stop admitting new producers into the closing scope; quiesce ordinary runtime
   mutations and subscription changes. Capture pre-teardown facts before cleanup
   rewrites them, and assign any final teardown revisions before fixing the expected
   revisions below. No later cleanup may silently advance that sealed projection.
2. Obtain the expected final roster and required agent/subscription revisions from
   the quiesced producers, within the archive's visibility scope. This expectation
   must not be inferred solely from the records the archive happened to receive.
3. Finish snapshot capture and persist control records and required payloads.
   Drain successfully; do not treat a timed-out or failed export as success.
4. Establish a fixed persisted cut/generation. Compare the indexed projections
   against the expected final roster/revisions and graph.
5. Verify every selected snapshot's complete recipe closure and integrity.
6. Persist bootstrap and completion manifest, making the closed marker visible
   only when its referenced data is durable and mutually consistent.

The manifest is archive-level completeness evidence, not another full-state JSON
log after every agent transition. The producer expectation witnesses identities
and revisions (including removals), rather than serializing a competing lifecycle
projection. It must account for agents and edges removed before shutdown as well
as those still registered. The exact witness and close-barrier mechanism remains
an implementation decision; the requirement is an independent completeness check.
Its concrete typed storage/wire format is separate from ordinary agent telemetry.

A high-water mark alone proves where received data ends, not that a whole agent or
its last update was ever received. The independent expected roster/revisions close
that hole. Conversely, there is no need to require every superseded historical
checkpoint sequence from 1 to N, as #14197 currently does. The selected final state
and closure must be complete; discarded intermediate projections need not be.

### 8.3 Bootstrap contents

The verified bootstrap includes:

- Version, source identity, archive generation, and fixed stream cuts/cursors.
- Complete visible final agent projections and notification graph.
- All required call-payload frames for the selected conversation anchors, with
  verified digests and references.
- Minimal identity/display context needed to install the roster without walking
  the full historical span ancestry.
- Remainder import cursors/exclusions preventing duplicates or state regression.

Closure traversal must include receivers, explicit arguments, implicit inputs,
module references, and nested literal references. A digest or old engine-local
handle alone is not a bootstrap. Avoid relying on best-effort live span copies of
payloads when verifying archives.

Keep this restore plan independent of downloading all historical log output. Where
the current planner reads the frontend DB, use the same canonical projection in
both places rather than translating from checkpoint JSON into synthetic authority.

## 9. CLI restore and background history

The CLI sequence is:

1. Initialize the connection/client facilities without composing destination agents
   or loading destination workspace modules for restore.
2. Fetch and validate the raw bootstrap's identity, version, generation, manifest,
   and required payload closure and integrity, without evaluating the recipes.
3. Import bootstrap control/display data and wait for an explicit frontend
   application acknowledgment. Exporter enqueue completion is not sufficient.
4. Resolve every required anchor from the applied bootstrap before creating any
   runtime. Then rehydrate all agents without starting execution and install
   notification relationships using the non-emitting restore path.
5. Attach conversations and select focus (explicit `--agent` first; otherwise
   top-level lineage and recorded activity). Use traced state for reset behavior.
6. Enable the prompt and start asynchronous historical spans/logs/metrics import.

Raw validation establishes bootstrap consistency, not side-effect-free recipe
evaluation. Anchor resolution may load schemas/modules or exercise still-eager
binding paths (§7); those must satisfy the capture and laziness requirements.
The all-anchors barrier prevents a partial runtime graph from becoming usable,
not every possible evaluation side effect.

Historical downloads use the bootstrap's fixed generation/cuts and exclusion rules,
with reconnect cursors and bounded retries. They must not redefine the live primary
span, infer new restore state, or overwrite newer control revisions. A background
history failure produces a visible nonfatal warning; a verified restored runtime
remains usable. Required bootstrap failures are fatal before prompt activation.

Test cancellation and cleanup of readers/import goroutines when the CLI exits.
Rendering and frontend locking must remain responsive while history is arriving;
moving the fetch into a goroutine alone is not proof of a usable prompt.

## 10. Source selection, compatibility, and removal of local saves

### 10.1 Engine first; explicit Cloud limitations

Use the connected engine's archive first. Fall back to Cloud when the local
archive is absent or evicted, or the archive endpoint is unsupported/unavailable
before any bootstrap is imported (including transport failures). Do not switch on
corruption, incomplete finalization, ambiguous identity, authorization failure,
explicit generation pins, cancellation, or failures after bootstrap starts.

Cloud can offer the same fast path only if it exposes equivalent verified
bootstrap/finality information. Otherwise a full-fetch fallback may reconstruct
the projection, but cannot claim fast startup or strict final-roster completeness
without the corresponding evidence. Never manufacture a successful close marker
from end-of-download alone.

Legacy or unsealed traces may remain viewable. If partial restore is supported,
require explicit opt-in and identify omitted agents, subscriptions, and guarantees.
Do not make `--partial` a silent dependency-substitution mechanism: a kept agent may
reference an omitted worker. Strict restore is the normal path; safe dependency
handling for partial restore is an implementation decision that must be settled
before advertising that mode.

### 10.2 Hard cutover away from JSON session persistence

Remove the old local-file path, not merely make trace restore another option:

- Automatic writes to `$XDG_STATE_HOME/dagger/llm-sessions`.
- JSON session metadata, serialized LLM IDs, and separate baseline wrappers.
- Local saved-session picker and local-file `--resume`/`-r` behavior.
- Local-session load/save commands and their history re-emission.
- Public `LLM.portableID` and `LLM.emitHistory` once their callers are removed or
  migrated, including non-CLI callers/tests and generated SDK/schema surfaces.

Do not conflate removal of session-file persistence with removal of Workspace
export: Ctrl+S/export remains useful. Audit actual command wiring rather than
removing every operation named "save". Internal portable construction may
transitionally remain as described in §7.3.

There is no requirement to read old JSON files or silently convert them during
restore. Do not delete users' existing files. Unsupported legacy invocation should
fail with a clear explanation. Whether `-r` is removed or deliberately reassigned
to trace selection is an explicit CLI decision, not compatibility with file UUIDs.

Only advertise a session as successfully resumable after archive finalization has
succeeded. A resume command/picker may reference trace IDs and verified archives,
not locally serialized conversations. History comes from original telemetry,
never `emitHistory` as a substitute for an unavailable archive.

## 11. Independent fixes to preserve from #14193

These are useful without the old checkpoint wire format:

- Compact stopped-agent telemetry contexts instead of retaining resolver/query
  contexts. Retained conversation leases still protect necessary objects.
- Acquire lifecycle leases outside the registry mutex, with duplicate-publication
  recheck and correct release on every failure/race path.
- Preserve explicit parent handles across restoration.
- Publish fresh dormant agents at creation and preserve restored failure text.

Re-derive these changes against current main and keep their focused race/lifetime
tests. Do not reuse old checkpoint delivery claims, imply that all snapshot work
has left the runtime lock when it has not, or allow stale cached digests to satisfy
final-revision validation.

## 12. Suggested implementation and PR structure

One replacement effort covers both old PRs. Reviewable slices are:

1. **Canonical control telemetry:** coherent agent revisions, explicit lineage and
   teardown/error facts, creation publication, subscription control state, shared
   projection, and protected persistence. Include independent lifetime/lock fixes
   where directly necessary; otherwise separate them.
2. **Archive and bootstrap:** persistent index, visibility-scoped finalization,
   final roster/graph validation, verified recipe closure, authenticated readers,
   retention, and fixed-cut bootstrap API. Adapt #14197 instead of carrying its
   historical checkpoint-sequence verifier.
3. **CLI graph restore:** frontend barrier, all-agent rehydration, non-emitting
   subscription installation, trace-authoritative Workspace/reset behavior, and
   background history with revision isolation.
4. **Snapshot hardening and recipe simplification:** cover all capture ingress,
   remove live fallbacks from strict resumability, prove raw-recipe restoration,
   then remove internal flattening where justified. This can land incrementally;
   the archive contract does not depend on which safe anchor implementation is used.
5. **Trace-only cutover:** remove local JSON persistence and public portable/history
   APIs, migrate callers and tests, regenerate SDKs/schema, and update CLI help.

Do not remove the last working persistence path before the replacement archive
workflow is operational. Conversely, a final PR claiming trace-only consolidation
must not leave autosave quietly invoking `portableID` in the background.

For each PR changing public APIs, regenerate and publish the GraphQL schema
snapshot and include the semantic SDL diff in the PR description. Explain which
slices remain follow-ups rather than describing the whole proposal as implemented.

## 13. Acceptance tests and performance evidence

### Control plane and finalization

- Saturate ordinary log queues while agent/control records and payloads are
  delivered; verify the final indexed roster/graph and anchors.
- Inject persistence failures before/after per-target claims. Retry failed targets
  without treating them as delivered; exhausted retries prevent a closed marker.
- Fail capture of the latest conversation after a prior successful revision.
  Strict finalization must reject it, not restore the earlier digest as current.
- Exercise out-of-order capture completion, rapid state/snapshot changes, lease
  release, registry races, and teardown under the race detector.
- Missing entire agent, final update, subscription, or recipe dependency prevents
  strict sealing even if all received cursors are well formed.
- Visibility-scoped archives do not fail on unrelated registry sequence gaps or
  reveal unauthorized parent/subscriber identities.

### Multi-agent semantics

- Restore dormant, idle, paused, failed, explicitly stopped, and session-stopped
  agents; preserve errors and pre-teardown mapping across repeated restores.
- Restore every required handle before a chief can dispatch to a worker. Inject
  rehydration failure and verify cleanup/no prompt activation.
- Restore a parent/worker subscription and a non-parent/custom-filter subscription.
  A new matching transition notifies the correct subscriber; nonmatching states do
  not. Replaced/removed subscriptions stay replaced/removed.
- Restoration itself emits no historical completion and starts no model turn.
  Document the excluded pending-mail/event case rather than claiming exactly-once.

### Workspace and recipe authority

- Resume from an unrelated client checkout, a checkout with missing/different
  modules, and a changed source checkout. Agent-visible files, tools, module schema,
  and pending edits come from the trace, not any destination files.
- Test no-Git/unsupported/failed capture and post-start `@` references. A live
  dependency must not be silently accepted as a durable snapshot.
- Raw same-type `withTools(slow).withTools(fast)` reload must not execute `slow`;
  loading the conversation should not execute `fast` either. Verify behavior with
  a fresh server, not just cache hits. Cover handle fallback separately.
- Cover repeated Workspace rebinding, skills/services, continuations, pending
  overlays, exported edits, and disappearance of the source session. Show when
  the raw committed recipe is equivalent before removing flattening.
- `.clear` never falls back to the client Workspace. Explicit export changes the
  destination only as requested and does not rebind the agent to it.

### Bootstrap, history, and persistence

- Block all historical streams after a valid bootstrap: the prompt still becomes
  usable and a restored agent can execute a turn.
- Block frontend application after bootstrap enqueue: restore cannot read a stale
  plan or activate the prompt before the acknowledgment.
- Deliver older control events after new live activity: no state, snapshot, or
  subscription regression. Import duplicate/reconnected history without duplicate
  messages or changing the live primary trace.
- Background history failure warns without breaking the restored session;
  bootstrap/closure corruption fails before runtime creation or prompt activation.
- Restart the engine process before restore, not just the worker or client. Verify
  archive reopening, retention, active-reader protection, and authenticated access.
- Verify absence/eviction-only Cloud fallback and explicit behavior for legacy,
  unsealed, unauthorized, and partial traces.
- Trace-only sessions neither read nor write JSON session files and never call
  removed public APIs. Regenerated schema/SDKs and CLI help agree.

Measure bootstrap transfer, frontend application, recipe reconstruction, runtime
rehydration, prompt-ready time, and historical completion separately. Compare with
main's full-fetch restore using representative multi-agent traces. Report recipe
closure size and capture cost as well as time to prompt; no invented latency target
should substitute for the blocked-history correctness test.

## 14. Implementation decisions still to settle

The behavioral requirements above are decisions; these details require explicit
choices in the replacement PR:

- Exact OTLP attribute names/version and subscription encoding (typed records and
  state arrays, not a parallel opaque checkpoint blob).
- Protected-processor backpressure/coalescing policy and shutdown deadlines, while
  preserving final-revision verification and explicit failure.
- The producer-side final roster/revision witness and close barrier, including
  removed agents/edges, without introducing another mutable-state authority.
- Restore-only subscription bookkeeping that suppresses historical completions
  while preserving notification on newly committed work.
- Cloud bootstrap/finality availability and what legacy partial restore can safely
  support. The local fast path must not be advertised as a Cloud speedup.
- Whether `-r` is removed or becomes an explicitly trace-based shortcut.
- Whether explicit inbound client-workspace reload remains available in restored
  sessions, and how `.clear` defines a traced reset target independently of export
  bookkeeping. Neither may silently import client state during restore.
- Archive retention defaults and durable content-reference availability guarantees.
- Which binding paths need additional laziness/capture before internal flattening
  can be removed, and whether later compaction is worth its measured cost.

Pending-mailbox/exactly-once notification recovery and crash-complete archives are
separate future designs, not unspecified promises hidden inside this one.
