# Part 1: ODAG Trace Visualization (Mutable Object DAG)

## Table of Contents
- [Decision Snapshot](#decision-snapshot)
- [Problem](#problem)
- [Goals](#goals)
- [Non-Goals](#non-goals)
- [Solution](#solution)
- [Research Notes (Existing Plumbing)](#research-notes-existing-plumbing)
- [Core Data Model](#core-data-model)
- [Algorithms](#algorithms)
- [Standalone App Architecture](#standalone-app-architecture)
- [Frontend Stack](#frontend-stack)
- [UX Design](#ux-design)
- [Feasibility and Tradeoffs](#feasibility-and-tradeoffs)
- [Unknowns and Open Questions](#unknowns-and-open-questions)
- [Implementation Plan](#implementation-plan)
- [Validation Plan](#validation-plan)

## Decision Snapshot

1. Experiment scope: standalone tool in `cmd/odag` (not integrated into `cmd/dagger` in v1).
2. v1 trace mode: completed traces first; live-follow is future work.
3. Backend/frontend line:
   - backend owns OTel parsing and ODAG semantics
   - frontend consumes high-level ODAG-domain events/snapshots.
4. Data ingestion modes:
   - cloud trace pull by trace ID
   - generic OTLP collector ingest (HTTP/protobuf first).
5. Runtime UX:
   - `odag serve` is long-running and persistent
   - `odag run <command...>` is a tiny OTEL-env wrapper (no output interception, no scope creep).
6. Telemetry extension:
   - keep `dagger.io/dag.output` unchanged
   - add `dagger.io/dag.output.state` + `dagger.io/dag.output.state.version`.
7. State emission semantics:
   - emitters SHOULD include state payload at least once per output ID
   - dedupe re-sends allowed
   - collectors cache by output ID and handle missing payload gracefully (`state unavailable`).
8. Dependency semantics:
   - no inferred dependency fallback when state payload is missing
   - if state arrives later, retroactively backfill affected timeline frames.
9. State payload shape:
   - typed field map, JSON-encodable nested structure, scalar + object refs
   - reuse existing DAGQL literal/JSON plumbing
   - Secret remains normal object reference semantics.
10. Frontend stack:
   - React + TypeScript + Vite + Tailwind + shadcn/ui
   - React Flow + ELK for workflow/circuit-style canvas.

## Problem

Current trace UIs are built around append-only spans and immutable function-call trees. That is accurate to execution, but not ideal for understanding object evolution.

1. **Chained object calls are visually noisy**: long call chains (`container().from().withExec()...`) are shown as many nodes even when they conceptually mutate one object.
2. **Object identity is implicit**: immutable DAGQL IDs represent object states, not a long-lived object in the visualization.
3. **Dependency intent is hard to see**: arguments and fields that reference other object IDs are buried inside call rendering.
4. **Equal rendering for all objects causes crowding**: large traces produce too many nodes if every created ID is rendered.
5. **Time navigation is weak**: there is no direct “object state at time T” model.

## Goals

1. **Visualize trace as an ODAG**: a DAG of mutable objects, where each object has state history over immutable DAGQL IDs.
2. **Preserve execution truth**: ODAG is a rendering layer only; no change to underlying immutable DAGQL semantics.
3. **Collapse same-type chains**: represent same-type receiver->return calls as object mutations.
4. **Render only referenced objects**: include only IDs referenced by top-level DAGQL call spans (returned, args, receiver).
5. **Support time travel**: step, play, and jump-to-end views over object-state transitions.
6. **Ship as a standalone local web app experiment**: runs locally, authenticates to Dagger Cloud, streams trace data, renders ODAG.
7. **Scope v1 to completed traces**: no live in-progress trace mode in initial implementation.
8. **Add object-state telemetry in v1**: emit serialized produced object state so dependencies are field-accurate.

## Non-Goals

1. Replacing existing CLI progress frontends.
2. Changing DAGQL execution model or ID semantics.
3. Perfect semantic inference of every field-level dependency from partial traces; v1 depends on explicit emitted object state.
4. Full feature parity with current cloud trace UI.
5. Live in-progress trace rendering in v1.

## Solution

Build a local web app with a small local backend:

1. Backend streams cloud span updates, reuses current auth and GraphQL subscription plumbing.
2. Backend decodes DAGQL call metadata from span attributes and builds an intermediate immutable call graph.
3. Backend transforms immutable call graph + span timing into an ODAG timeline:
   - Mutable object nodes with ordered state history.
   - Mutation events when same-type calls return same-type objects.
   - Dependency edges from receiver/arg/object references.
4. Frontend renders ODAG at time `t` with controls for play/pause/step/end.
5. Scope filter keeps only objects referenced from top-level DAGQL call spans.
6. Backend supports two data procurement modes:
   - **Cloud trace mode**: fetch by trace ID from Dagger Cloud (`spansUpdated` stream).
   - **Collector mode**: act as a generic OTLP collector endpoint any Dagger client can export to.

For v1, dependency edges are derived from emitted serialized object state for each produced object ID.

## Research Notes (Existing Plumbing)

The core plumbing already exists and can be reused directly:

1. **Cloud trace streaming**:
   - `internal/cloud/trace.go`
   - `Client.StreamSpans` subscribes to `spansUpdated` and yields attributes/events/links.
2. **CLI trace command ingestion path**:
   - `cmd/dagger/trace.go`
   - Streams cloud spans -> converts to OTLP -> exports through frontend pipeline.
3. **DAGQL metadata emitted on spans**:
   - `core/telemetry.go`
   - Sets:
     - `dagger.io/dag.digest`
     - `dagger.io/dag.call` (encoded `callpbv1.Call`)
     - `dagger.io/dag.inputs`
     - `dagger.io/dag.output` (when returning an object)
4. **New telemetry required for ODAG v1**:
   - serialized object state payload for each produced object ID
   - payload includes object type and typed field map (`name`, `type`, `value`)
   - field values include both scalar values and object references (IDs) for object-typed fields
   - payload preserves full nested structure (lists/objects), not flattened paths
   - payload values are JSON-encodable and should reuse existing engine JSON/literal encoding plumbing as much as possible (DRY)
   - Secret handling relies on existing DAGQL semantics (e.g. `Secret` as object reference ID), with no ODAG-specific redaction layer
   - encoding should match existing practice used by `dagger.io/dag.call`: deterministic protobuf bytes, base64-encoded string attribute.
   - suggested attrs:
     - `dagger.io/dag.output` (existing output object state ID digest; unchanged)
     - `dagger.io/dag.output.state` (base64 protobuf payload for that output object state)
     - `dagger.io/dag.output.state.version` (e.g. `v1`)
   - protocol semantics:
     - emitters SHOULD include `dagger.io/dag.output.state` at least once per unique `dagger.io/dag.output` ID within a trace
     - collectors MUST gracefully handle missing state payloads (older engines or partial streams)
   - emission optimization:
     - avoid re-sending `dagger.io/dag.output.state` for an output ID whose identical payload was already emitted in the trace
     - consumers cache by output state ID (`dagger.io/dag.output`) and reuse cached payload when later spans only provide the ID.
5. **Attribute constants**:
   - `sdk/go/telemetry/attrs.go`
6. **Current span DB and call decode logic**:
   - `dagql/dagui/db.go`
   - `dagql/dagui/spans.go`
   - Decodes call payload, correlates creator spans, tracks output/input/effects.
7. **Existing immutable DAG visualization helper**:
   - `dagql/dagui/dot.go`
   - Useful as reference for receiver/arg edge extraction.

Note: this research pass did not include `dagger/dagger.io` dagviz internals, so integration assumptions below stay conservative.

## Core Data Model

### Semantic distinction

1. **DAGQL**: immutable IDs (`<FooID>`) represent object states.
2. **ODAG rendering**: mutable object (`<Foo>`) holds ordered history of immutable states.

### Proposed types

```go
type ODAG struct {
  Objects map[string]*ObjectNode      // objectID -> node
  States  map[string]*ObjectState     // stateDigest -> state
  Edges   []*ObjectEdge               // object-level dependencies
  Frames  []TimelineFrame             // precomputed playback checkpoints
}

type ObjectNode struct {
  ObjectID        string              // stable local ID
  TypeName        string              // CoreType or module.Type
  ModuleRef       string              // optional
  StateHistory    []string            // ordered state digests
  FirstSeen       time.Time
  LastUpdated     time.Time
  ReferencedByTop bool
}

type ObjectState struct {
  StateDigest     string
  ObjectID        string
  TypeName        string
  CallDigest      string              // call producing this state
  SpanID          string              // producing span
  StartTime       time.Time
  EndTime         time.Time
  ReceiverState   string
  ArgStateRefs    []ArgRef
  StatePayloadRaw string              // emitted serialized object state (opaque to UI)
  Fields          []StateField        // extracted typed field map for lookup/inspection
  FieldRefs       []FieldRef          // extracted object-ID references from state payload
}

type ArgRef struct {
  Name       string
  StateDigest string
  Path        string // e.g. "opts.source" for nested object/list args
}

type FieldRef struct {
  Path        string // field path in serialized object state
  StateDigest string // referenced object state digest
}

type StateField struct {
  Name  string
  Type  string
  Value *callpbv1.Literal // JSON-encodable nested value; object refs are Literal_CallDigest
}

type ObjectEdge struct {
  FromObjectID string
  ToObjectID   string
  Kind         string // field-ref
  Label        string
  FirstSeen    time.Time
  LastSeen     time.Time
  EvidenceCount int                  // number of object states where this field-ref appears
}
```

## Algorithms

### 1) Ingest and normalize DAGQL call spans

For each incoming span:

1. Read attributes.
2. If `dagger.io/dag.digest` and `dagger.io/dag.call` are present, decode `callpbv1.Call`.
3. Keep a `SpanRecord` index by span ID and call digest.
4. Track parent span graph even for non-DAGQL spans (needed for top-level detection).
5. Parse emitted serialized object state payload for produced objects and extract field object-ID refs.
6. Build a lookup index `stateID -> typed field map` for inspector and recursive exploration.
7. If a span has `dagger.io/dag.output` but no `dagger.io/dag.output.state`, resolve from local state cache.
8. If state payload is still unavailable, materialize object node as `state unavailable`, with no dependency edges for that state.
9. If missing state payload arrives later for a known state ID, retroactively backfill that state in-place (same object/node identity) and recompute dependencies/history for affected timeline frames.

### 2) Detect top-level DAGQL function spans

Definition: DAGQL function call span that is not the child (at any ancestor depth) of another DAGQL function call span.

Algorithm:

1. Candidate set: spans with decoded DAGQL call payload.
2. For each candidate, walk parent chain until root.
3. If any ancestor span is also a DAGQL call span, candidate is not top-level.
4. Remaining spans are top-level.

This avoids false negatives when DAGQL span nesting includes passthrough/internal non-call spans between DAGQL calls.

### 3) Compute referenced seed IDs (scope filter)

For each top-level DAGQL span:

1. Add receiver digest (`call.receiverDigest`) if present.
2. Add all call-digest references found recursively in args (`Literal.callDigest`, nested list/object).
3. Add returned output digest (`dagger.io/dag.output`) if present.
4. Add call digest itself (`dagger.io/dag.digest`) for continuity.

Only objects reachable from these seed states are rendered.

### 4) Build mutable objects from immutable states

Process call spans in `(endTime, startTime, spanID)` order:

1. Determine produced state digest:
   - prefer `dagger.io/dag.output` when present (object-returning call),
   - fallback to call digest when needed for continuity.
2. Resolve receiver state digest from call payload.
3. Decide mutation vs creation:
   - **Mutation** if receiver exists, receiver type equals return type, and receiver already mapped to an ODAG object.
   - **Creation** otherwise.
4. Map produced state digest -> objectID.
5. Append produced state into object history.
6. Emit dependency edges:
   - for each field reference in emitted state payload, map referenced state -> object and emit `referenced object -> current object`
   - edge label is field path from payload extraction (e.g. `mounts.src`)
   - upsert aggregated edge and bump evidence count
   - update edge `FirstSeen`/`LastSeen` from mutation event time bounds.

### 5) Apply scope filter

1. Keep objects referenced by top-level **create/mutate** events, including receiver/input object references for those events.
2. Keep top-level call outputs only when they are `Query.*` roots or when they show non-top-level activity on the same object identity.
3. Drop non-top-level-only objects by default (this avoids large `File`/`Directory` fan-out noise in execution-heavy traces).
4. Optionally keep immediate neighbor objects if they are required to preserve an edge endpoint in rendered view (toggleable).
5. Drop all others to reduce visual crowding.

### 6) Timeline model

State transition policy for time `t`:

1. Mutation is applied at producing span end time by default.
2. During span runtime, mutation is shown as “pending” badge on target object.
3. Playback frame at `t` shows:
   - latest object state with `state.EndTime <= t`
   - active/pending spans where `start <= t < end`
   - only edges observed by `t`

This keeps state transitions deterministic and avoids showing future states early.

Discrete playback mode (default UI):

1. Build a step list from projected events that target an object (`event.objectID != ""`).
2. Slider position maps to a step index, not absolute wall time.
3. Backend resolves `GET /api/traces/{traceID}/snapshot?step=<n>` to an exact event boundary (stable even when multiple events share timestamps).
4. Timeline labels show `Step i / N` (with relative time as secondary context).

Operator-friendly controls (default UI):

1. Replace player/seek bar with explicit controls: first, back, forward, last.
2. Show status, current step, and last step as independent readouts.
3. Default selection stays on step 1 for newly selected traces; manual navigation controls progression.

## Standalone App Architecture

### Components

1. **Backend (Go, local process)**
   - Reuse `internal/cloud/auth` and `internal/cloud/trace` for auth + stream subscription.
   - Build ODAG transformer service and expose JSON/WS APIs.
2. **Frontend (web SPA)**
   - Workflow-style graph canvas + timeline controls + side inspector.
   - Receives incremental ODAG snapshots or patches over WebSocket.

### Backend / Frontend Boundary

1. **Backend owns ODAG semantics**
   - OTel/DAG attribute parsing and normalization
   - output-state payload cache and backfill
   - immutable->mutable object reconstruction
   - dependency extraction and timeline event computation
2. **Frontend owns presentation**
   - layout, styling, animation
   - interaction and viewport state
   - timeline controls and inspector rendering

Frontend consumes ODAG-domain events/snapshots, not raw OTel.

### Data Procurement Modes

1. **Mode A: Cloud trace pull**
   - Input: `org + traceID`
   - Reuse `internal/cloud/trace.go` streaming (`StreamSpans`).
   - Best for replaying existing traces.
2. **Mode B: OTLP collector ingest (generic)**
   - Backend exposes OTLP HTTP ingest endpoints compatible with Dagger client exporters:
     - `POST /v1/traces`
     - `POST /v1/logs`
     - `POST /v1/metrics`
   - Protocol priority: OTLP HTTP/protobuf first (fastest integration with existing `dagger` CLI/exporter behavior).
   - Any Dagger client/CLI can point `OTEL_EXPORTER_OTLP_*` env vars at this backend.
   - Reuse approach from `cmd/dagger/run.go` telemetry proxy.
3. **Mode C: CLI wrapper convenience**
   - `odag run <command...>` executes a command with OTEL env vars pointed at `odag serve`.
   - This is intentionally a thin wrapper over collector mode, not a separate execution runtime.

### Runtime and CLI UX

1. `odag serve`
   - Runs as a long-lived local server.
   - Exposes:
     - OTLP ingest endpoint(s) for trace/log/metric intake.
     - Web UI endpoint (e.g. `http://localhost:5454`).
   - Maintains persistent local trace store and ODAG-derived index.
2. `odag run <command...>`
   - Convenience wrapper that executes a command (for example `dagger call ...`) with OTEL env vars pointed at `odag serve`.
   - `--server` defaults to `$ODAG_SERVER` when set, otherwise `http://127.0.0.1:5454`.
   - Collects telemetry into local store without requiring manual OTEL env setup.
   - v1 behavior: requires `odag serve` to already be running; if unavailable, fail with a clear message and suggested command.
   - Wrapper remains passthrough-only (no command output capture/summarization).
3. Persistent store behavior
   - Store traces across restarts.
   - List traces with metadata (trace ID, first/last seen, source mode, status).
   - Select a stored trace for replay/visualization in UI.

## Frontend Stack

Chosen renderer direction is workflow/circuit-canvas style (similar to modern workflow editors), while keeping ODAG read-only in v1.

1. **Core stack**
   - React + TypeScript + Vite
   - Tailwind + shadcn/ui for application shell controls (panels, sliders, toggles, dialogs, tables)
2. **Canvas engine**
   - React Flow for nodes/edges, handles, pan/zoom/select, and interaction primitives
   - ELK (`elkjs`) for layered DAG auto-layout
3. **State and performance**
   - Zustand for app state (timeline cursor, selection, filters, viewport)
   - Worker-assisted transform/layout path for large traces
   - Incremental graph patching from backend diffs
4. **Visual profile**
   - Dark dotted background
   - Rounded object nodes with typed ports
   - Solid primary edges + styled secondary/context links
   - Timeline-synchronized edge/node emphasis
5. **Editability posture**
   - v1 remains read-only
   - component choices keep a clean path to optional future drag-and-drop editing.

### API sketch

```http
POST /api/session/login              // optional: validate auth state
POST /api/traces/open                // { org, traceID }
GET  /api/traces                     // list locally stored traces
GET  /api/traces/{id}/meta           // trace metadata
GET  /api/traces/{id}/stream         // websocket: span batches + odag diffs
GET  /api/traces/{id}/snapshot?t=... // materialized ODAG at time t
POST /api/ingest/mode                // { mode: "cloud" | "collector" }
```

### Suggested package layout

```text
cmd/odag/
internal/odag/server/
internal/odag/transform/
web/odag/
```

`hack/designs/odag.md` is intentionally design-only; code layout can move if promoted later.

## UX Design

### Main view

1. **Graph panel**: ODAG object nodes + dependency edges.
2. **Timeline panel**: scrubber, play/pause, step, speed, end.
3. **Inspector panel**: selected object details:
   - type (`CoreType` or `module.Type`)
   - current state digest (short)
   - state history table (time, producing call, span status)
   - incoming/outgoing dependencies

### Node visual language

1. Node title: object type.
2. Node subtitle: object alias (`<Type>#N`) and current state short digest.
3. State badge:
   - `running` (active mutation in flight)
   - `cached`
   - `failed`
   - `stable`
4. History sparkline or step count for mutation depth.

### Interactions

1. Hover edge to highlight related call/arg path.
2. Click node to lock inspector.
3. Toggle filters:
   - top-level scope only (default on)
   - include neighbor dependencies
   - show internal spans influence
4. Playback modes:
   - step event
   - play to next mutation
   - jump to end

Current event details (timeline focus) should show:
1. mutation call identity (`type.field`, span ID/time)
2. input objects used by this event
3. state payload summary and field-reference edges added/removed at this event.

## Feasibility and Tradeoffs

### Feasibility

1. **High**: required DAGQL metadata already exists in span attrs.
2. **High**: cloud streaming and auth code already implemented.
3. **Medium**: new engine telemetry payload is required for field-accurate dependencies.
4. **Medium**: object mutation inference is heuristic (same-type receiver->return), but works for core chaining patterns.
5. **Medium**: large traces need incremental transforms and layout throttling.

Encoding note:
1. Existing call payload format (`dagger.io/dag.call`) is deterministic protobuf + base64 string (`dagql/call/callpbv1/call.go`).
2. ODAG payloads should reuse this pattern for consistency and robust decode behavior across stream hops.

### Tradeoffs

1. **Mutation heuristic simplicity vs semantic precision**
   - Simple rule is robust and cheap.
   - Some calls may look like mutation but are conceptual forks; need override hooks later.
2. **Apply mutation at end-time vs start-time**
   - End-time is semantically safer.
   - Start-time can feel more “live” but can show speculative state.
3. **Top-level seed filter strictness**
   - Strict seed filter reduces clutter strongly.
   - May hide useful transitive context unless “include neighbors” is available.
4. **Backend transform vs browser-only transform**
   - Backend transform allows reuse of Go internals and auth.
   - Browser-only would simplify deployment but complicate CORS/auth and protobuf decode parity.
5. **Payload richness vs telemetry overhead**
   - Emitting object state payloads gives accurate dependencies and simpler UI semantics.
   - Payload size and serialization cost must be bounded for large traces.

## Unknowns and Open Questions

1. **dagger.io dagviz reuse**: which components/algorithms can be imported directly once repository access is confirmed?
2. **Very large traces**: expected practical upper bounds (span count/object count) for v1 UX targets.
3. **Collector transport expansion**: when to add OTLP gRPC ingest in addition to HTTP/protobuf.

## Implementation Plan

### Stage Checklist (Execution Status)

- [x] Stage 1: CLI/server/store scaffold (`odag serve`, `odag run`, sqlite schema, health endpoint)
- [x] Stage 2: OTLP ingest mode (trace/span persistence from `/v1/traces`)
- [x] Stage 3: Backend trace APIs (list/get/events) + ODAG projection model
- [x] Stage 4: Web UI shell + timeline + ODAG canvas + inspector
- [x] Stage 5: Cloud pull mode + polish (tests, docs, UX refinements)

Stage 2 implementation note:
- `/v1/traces` now decodes OTLP HTTP/protobuf and upserts trace/span records in sqlite.
- `/v1/logs` and `/v1/metrics` are currently compatibility no-op endpoints (`202 Accepted`) so standard OTEL env wiring works without exporter failures.
- Server now emits simple lifecycle logs for client connect/disconnect and OTLP trace upload start/completion (per trace ID in each ingest batch).

Stage 3 implementation note:
- API endpoints now expose trace list/meta and projected ODAG data:
  - `GET /api/traces`
  - `GET /api/traces/{traceID}/meta`
  - `GET /api/traces/{traceID}/events`
  - `GET /api/traces/{traceID}/snapshot?t=<unix_nano>`
  - `GET /api/traces/{traceID}/snapshot?step=<event_index>`
- Backend now projects immutable DAGQL call/output spans into mutable object histories and mutation events, with top-level seed filtering.
- Projection summary now includes command/root-span context:
  - trace title inferred from CLI command spans (`process.command_args` when available)
  - command span list and root span list for trace-level debug context
- Dependency edges remain empty until `dagger.io/dag.output.state` payloads are emitted by the engine (objects are still shown with `missingState` signaling).

Stage 4 implementation note:
- `odag serve` now hosts an embedded web UI with split routes (no external frontend build step required for the local experiment):
  - `/` trace list page (picker/import)
  - `/traces/{traceID}` dedicated trace view page for maximum ODAG canvas space
- UI includes:
  - trace list rows include creation time (`firstSeen`) to aid scanability when many traces exist
  - dedicated trace page now uses a left-side revision history pane (replacing top step controls and bottom event stream)
  - selecting a history item moves the DAG snapshot to that event boundary time
  - history pane includes checkbox filters for `calls`, `derived`, and `visible`
  - history cards use table-like aligned columns (kind/call/parent/visible/time) while keeping card styling
  - dual selection cues are explicit and composable:
    - current-event selection highlights the event row and marks the mutated object with an event badge/ring
    - selected-object selection uses a distinct object contour color and highlights all history rows that mutate that object
  - dedicated central trace title row above the DAG canvas
  - ODAG object canvas (workflow-style cards with mutation highlighting)
  - object cards show ODAG alias (`Type#N`) as primary label; immutable state digest text is hidden from card body
  - trace view drops the inspector pane to maximize graph/history real estate

Stage 5 implementation note:
- Cloud pull mode is implemented in both CLI and backend API:
  - `odag fetch <traceID> [--org ...]`
  - `POST /api/traces/open` with `{ "mode": "cloud", "traceID": "...", "org": "..." }`
- Web UI now exposes Cloud import controls (trace ID + optional org) and refreshes local trace list after import.
- Telemetry protocol constants were added for upcoming output-state payload support:
  - `dagger.io/dag.output.state`
  - `dagger.io/dag.output.state.version`
- Engine emission is now implemented in `core`:
  - when a span sets `dagger.io/dag.output`, it also emits `dagger.io/dag.output.state` + `.version` (`v1`) for first-seen output IDs in a trace
  - emitter deduplicates by `(traceID, dag.output)` and avoids resending the same state payload repeatedly
- Current payload encoding is `base64(json)` (version `v1`) with shape:
  - root: `{ type, fields }`
  - `fields` entry: `{ name, type, value }`
  - object references in values are emitted as immutable call digests (state IDs)
- ODAG consumes these attributes when present and gracefully handles absence (`missingState`), enabling compatibility with both older and newer engines.
- Engine telemetry output-state encoding is hardened against typed-nil or panicking `dagql.IDable` and `dagql.Typed` values to prevent resolver panics; serializer now fails closed for those fields instead of crashing query execution.

Post-MVP projection refinement:
- Default rendering now excludes `dagger.io/ui.internal=true` spans/events from seed scope and UI event stream to reduce noise.
- Object projection ignores scalar outputs (e.g. `String`, `Int`, `Boolean`, `Float`, `JSON`, `Void`) even if older traces contain `dag.output` for them.
- Mutation collapse now tolerates module-qualified type names (e.g. `ModuleSource` vs `mymod.ModuleSource`) via normalized type matching, reducing false "create" splits in chains.
- Default keep rules now prune top-level non-`Query.*` call-only fan-out objects and non-top-level-only objects, while preserving top-level writes and mutation-heavy top-level outputs.
- Event debug fields now include call-depth and nearest parent DAG call metadata to audit top-level classification directly in UI.

### Phase 0: Spike

1. Implement ODAG transformer against recorded span fixture.
2. Define emitted object-state telemetry payload format and parser (deterministic protobuf + base64, versioned).
3. Measure transform + layout time with realistic payload sizes.

### Phase 1: Local standalone MVP

1. Add local Go server (`odag serve`) with persistent trace store + trace open/list/stream endpoints.
2. Implement Mode A (cloud trace pull by trace ID).
3. Implement Mode B (OTLP collector ingest endpoints).
4. Add frontend with React Flow canvas, shadcn/tailwind shell, timeline, inspector.
5. Implement top-level seed filter and neighbor toggle.
6. Support playback: step, play, end.
7. Add convenience wrapper (`odag run <command...>`) that injects OTEL env vars.

### Phase 2: Scale and robustness

1. Incremental ODAG diff updates.
2. Performance optimizations (virtualization, edge culling, workerized layout).
3. Better edge/type labeling and mutation-heuristic overrides.

### Phase 3: Payload evolution (future)

1. Version object-state payload format for compatibility.
2. Add optional compact/delta encoding for very large traces.
3. Add richer field semantics (e.g. field categories, cardinality hints).

### Phase 4: Optional live-mode extension

1. Add live-follow mode for in-progress traces if experiment proves useful.

### Phase 5: Integration decision

1. Evaluate promotion path into `cmd/dagger` and/or cloud web UI.
2. If promoted, extract reusable transform package from `internal/odag`.

## Validation Plan

1. **Golden fixtures**
   - Input: raw span batches
   - Output: ODAG snapshots at fixed timestamps
2. **Property checks**
   - Each state digest belongs to exactly one object
   - Object state history is time-ordered
   - No rendered object outside top-level reference scope (unless neighbor toggle enabled)
   - Field-reference edge evidence count equals number of contributing object states
3. **UX checks**
   - 10k+ span traces remain interactive at end-state view
   - timeline scrub remains responsive with progressive rendering
