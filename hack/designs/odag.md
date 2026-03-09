# Part 1: ODAG Trace Visualization (Mutable Object DAG)

## Table of Contents
- [Decision Snapshot](#decision-snapshot)
- [Problem](#problem)
- [Goals](#goals)
- [Non-Goals](#non-goals)
- [Solution](#solution)
- [Research Notes (Existing Plumbing)](#research-notes-existing-plumbing)
- [Core Data Model](#core-data-model)
- [Backend API (V2 Source of Truth)](#backend-api-v2-source-of-truth)
- [Algorithms](#algorithms)
- [Standalone App Architecture](#standalone-app-architecture)
- [Frontend Stack](#frontend-stack)
- [UX Design](#ux-design)
- [Feasibility and Tradeoffs](#feasibility-and-tradeoffs)
- [Unknowns and Open Questions](#unknowns-and-open-questions)
- [Handoff Snapshot](#handoff-snapshot)
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
   - engine-emitted per-field `refs` is authoritative for dependency extraction (no backend path/type heuristics)
   - hard cutover is acceptable for this new attribute (`dagger.io/dag.output.state` is not a released compatibility surface)
   - reuse existing DAGQL literal/JSON plumbing
   - Secret remains normal object reference semantics.
10. Frontend stack:
   - current prototype: embedded HTML/CSS/JS + SVG renderer (no external frontend build)
   - future candidate direction: React + TypeScript + React Flow/ELK if/when editing-scale UX is required.
11. V3 home information architecture:
   - primary nav is entity-first: discovered domains such as Terminals, Services, Repls, Checks, Workspaces, Sessions, Pipelines (`dagger call` style submitted call chains), Shells, Workspace Ops, Git Remotes, Registries.
   - while taxonomy is still settling, the right pane should stay inventory-first: click a domain on the left, see that domain's inventory immediately on the right.
   - deeper specialized views can return later once the base inventory interaction is obviously useful.
12. Underlying execution/debug hierarchy:
   - `dagql session` -> `dagql client` -> spans/calls -> object bindings
   - trace remains a secondary ingest/debug/import escape hatch, not the primary UI silo.
13. Entity taxonomy boundary:
   - `object` keeps its strict ODAG meaning: immutable DAGQL snapshot plus derived mutable binding history.
   - `entity` is a broader derived UI concept built from the same telemetry facts.
   - the relationship between an entity and objects is domain-specific and must not be forced into one universal mapping.
14. Execution-scope hierarchy:
   - clients are the primary derived execution objects
   - sessions are derived from root clients
   - every client maps to one `dagger.io/engine.client` `connect` span.
15. Reliability boundary:
   - this client-tree model is the target API shape
   - current telemetry does not prove the full hierarchy reliably enough to treat it as protocol truth
   - explicit engine-emitted `session_id` / `client_id` / `parent_client_id` telemetry is the preferred long-term fix.
16. Edge semantics:
   - default object-object edges are field-reference dependencies (`field_ref`)
   - call containment and call-produced-object relationships are separate relations, not the same kind of edge
   - receiver/input lineage is a provenance overlay, not part of the default supply-chain graph.
17. V3 heuristic order:
   - discovery/classification comes before specialized visualization.
   - heuristics should first answer what domain/entity a span or call belongs to, then apply domain-specific rendering rules.
18. Working method:
   - scaffold all candidate entity domains with mock data first.
   - pick one domain and implement it end-to-end through derivation, API, and UI.
   - record findings and next steps in this design doc after each milestone.

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
5. **Support discrete history navigation**: move across ODAG mutation revisions by selecting events.
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
4. Frontend renders ODAG at event-boundary time `t` selected from a revision-history pane.
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
   - payload includes object type and typed field map (`name`, `type`, `value`, `refs`)
   - `refs` contains unique object snapshot IDs referenced by that field (directly or nested), sorted deterministically
   - backend treats `refs` as authoritative for field-reference extraction
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
8. **Current telemetry signals for session/client derivation**:
   - Current spans do not emit explicit `session_id` / `client_id` attributes in OTEL payloads.
   - Useful resource attrs that are present today:
     - `service.name`
     - `service.version`
     - `process.command_args`
     - `dagger.io/client.version`
     - `dagger.io/client.os`
     - `dagger.io/client.arch`
     - `dagger.io/client.machine_id` (sometimes present)
     - VCS labels such as `dagger.io/git.remote`, `dagger.io/git.ref`, `dagger.io/git.branch`
   - Useful scope/name patterns that are present today:
     - root spans with scope `dagger.io/cli`
     - lifecycle spans with scope `dagger.io/engine.client`
     - common `dagger.io/engine.client` names: `connect`, `connecting to engine`, `creating client`, `starting session`, `subscribing to telemetry`, `consuming /v1/traces|logs|metrics`
   - Current ODAG prototype still approximates `session == trace`; this should be replaced by a dedicated derivation pass using the signals above.

Note: this research pass did not include `dagger/dagger.io` dagviz internals, so integration assumptions below stay conservative.

## Core Data Model

The backend REST API is the architectural source of truth for ODAG.  
Persistence and API semantics are modeled from OTel span data first; higher-level entities are derived deterministically.

### Semantic distinction

1. **DAGQL**: immutable IDs (`<FooID>`) represent object states.
2. **ODAG rendering**: mutable object (`<Foo>`) holds ordered history of immutable states.
3. **ID scope**: DAGQL IDs are treated as globally scoped across traces/sessions; when an object must be isolated to a session/client, that scope is already mixed into the ID by the engine, so ODAG should not add an extra artificial session-local namespace.
4. **Ground truth vs inference**:
   - receiver, args, output, span ancestry, and emitted object state are telemetry facts
   - object bindings, mutations, `contains_object`, and collapse decisions are ODAG derivations
   - the API should preserve that distinction explicitly.

### Source-of-truth layering

1. **Ingested (authoritative)**:
   - OTel spans (`trace_id`, `span_id`, parent, timing, status, attrs, events, resource/scope).
2. **Derived (versioned transform output)**:
   - DAGQL calls, object snapshots, mutable object bindings, binding mutations, session/client labels.
3. **Views**:
   - session/client/trace scoped projections are query-time filters over one global pool.
   - primary UX is session-first; trace-centric views remain secondary/debug-oriented.
4. **Heuristic policy**:
   - heuristics are acceptable for deriving engine-defined execution scopes such as session/client when telemetry does not yet expose them directly
   - those heuristics must live behind a narrow, versioned derivation boundary so they can be tuned or replaced later without changing the rest of the model.

### V3 entity-centric IA

1. Home UI is organized around discovered entity domains, not raw object bindings alone.
2. Entities are broader than ODAG objects and currently fall into three rough buckets:
   - object-centric: domains that can map fairly directly onto bindings/snapshots (`Service`, parts of `Check`)
   - execution-centric: domains defined primarily by client/session/call continuity (`Terminal`, `Repl`, `dagger call`, `dagger shell`)
   - external-resource-centric: domains defined by side effects or external endpoints (`Workspace`, git remote, registry, export/import surfaces)
3. Discovery sequence:
   - first scan spans/calls/state for evidence and classify candidate entities
   - then render a specialized view per entity domain
   - only after that apply domain-specific visualization heuristics
4. Object bindings and snapshots remain substrate and drill-down layers, not necessarily the home-page primitive.
5. In the UI and API language, the left nav should be described as entity domains/areas rather than object types.

### First concrete entity definition: Pipeline

1. `Pipeline` is the V3 label for one client-submitted, connected DAGQL call chain that aims at one intended result.
2. Canonical examples:
   - one invocation of `dagger call`
   - one top-level interactive command submitted inside `dagger shell`
3. A `dagger call` pipeline is a CLI chain of DAGQL function calls:
   - each additional subcommand extends the chain by calling a function on the object returned by the previous step
   - intermediate steps therefore need to keep returning object-capable values until the terminal step
4. A pipeline currently assumes one terminal DAGQL output value from the final step in the chain.
5. Ambiguous batch cases such as `dagger -c '...; ...'` or shebang interpreter sessions are intentionally left split per top-level command for now:
   - they are submitted together, but may contain multiple independent chains and outputs
   - V3's current page shape is explicitly output-centric, so multi-output batches would blur the entity boundary too early
6. The CLI may then perform built-in, type-driven post-processing on that returned value:
   - `Changeset`: preview, confirm, and optionally apply to the workspace
   - `Container` / `Directory` / `File`: export convenience when `--output` is used
   - `LLM`: enter interactive prompt mode
   - object-valued returns: print object IDs rather than recursively rendering another structure
7. Those post-processing flows can emit follow-up spans or side effects that are not themselves part of the core DAGQL call chain, but are still part of the same user-facing pipeline context.
8. V3 should therefore model Pipeline as an execution entity with:
   - core evidence: command root + chained DAGQL calls + terminal output
   - attached follow-up evidence: output handling, apply/export flows, prompt handoff, and other CLI-managed continuations
9. The renderer should not treat those follow-up spans as unrelated noise merely because they do not look like one more chained DAGQL object mutation.
10. First implementation heuristic:
   - derive one Pipeline from each client whose `process.command_args` classify as `dagger call`
   - use the client scope as the hard execution boundary
   - collect DAGQL call events owned by that client as the pipeline's core chain
   - treat the latest call in that owned chain as the terminal call/output
   - associate later non-call spans in the same client scope as follow-up evidence
11. This is intentionally conservative:
   - it is better to under-associate follow-up behavior than to claim unrelated trace noise is part of the run
   - future explicit client-mode telemetry can tighten classification, especially for interactive shell commands and multi-command batch cases
12. Pipeline MVP fields:
   - `runID`, `traceID`, `sessionID`, `clientID`, `rootClientID`
   - `command`, `commandArgs`, `chainLabel`
   - `startUnixNano`, `endUnixNano`, `statusCode`
   - `callIDs`, `terminalCallID`, `terminalCallName`
   - `terminalReturnType`, `terminalOutputDagqlID`
   - `postProcessKinds`, `followupSpanIDs`
   - per-pipeline `evidence` and `relations` lists for direct UI consumption
13. First endpoint:
   - `GET /api/pipelines`
   - same scope filters as other entity endpoints (`traceID`, `sessionID`, `clientID`, `from`, `to`, pagination)
14. First UI wiring:
   - only the `Pipelines` domain switches from mock data to this endpoint initially
   - other domains stay mocked until their discovery contracts are defined

### Second concrete entity definition: Shell

1. `Shell` is the V3 label for one invocation of `dagger shell`.
2. Unlike `Pipeline`, a Shell is session-centric rather than output-centric:
   - it can own many DAGQL calls over time
   - it may spawn descendant clients
   - it does not have one singular terminal DAGQL output that defines the entity
3. A Shell should therefore be anchored on execution scope:
   - command root (`process.command_args` classifies as `dagger shell`)
   - explicit `sessionID`
   - explicit client tree (`clientID`, `parentClientID`, `rootClientID`)
4. First implementation heuristic:
   - derive one Shell from each client whose `process.command_args` classify as `dagger shell`
   - use that client as the shell root
   - include descendant clients that share the same root client
   - collect calls and non-call spans owned by that root client tree as shell activity
5. First shell payload should emphasize:
   - shell identity and scope (`shellID`, `traceID`, `sessionID`, `clientID`, `rootClientID`)
   - command summary (`command`, `commandArgs`)
   - session extent (`startUnixNano`, `endUnixNano`, `status`)
   - descendant activity (`childClientIDs`, `childClientCount`, `callIDs`, `callCount`, `spanCount`)
   - per-shell `evidence` and `relations`
6. First endpoint:
   - `GET /api/shells`
   - same scope filters as other entity endpoints
7. First UI wiring:
   - only the `Shells` domain switches from mock data to this endpoint
   - its primary cues are session duration, descendant clients, and owned calls, not object outputs

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
  FieldName   string // top-level field key in output-state payload
  StateDigest string // single referenced object state digest (one row per target)
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

### V2 backend entities (REST-domain)

```go
// Ingested from OTLP. Closest model to telemetry source-of-truth.
type Span struct {
  TraceID       string
  SpanID        string
  ParentSpanID  string
  Name          string
  StartUnixNano int64
  EndUnixNano   int64
  StatusCode    string
  StatusMessage string
  Attributes    map[string]any
  Events        []map[string]any
  Resource      map[string]any
  Scope         map[string]any
}

// Derived from spans carrying dagger.io/dag.call.
type Call struct {
  ID                  string // stable derived ID (e.g. spanID)
  SpanID              string
  TraceID             string
  ParentCallID        string
  ClientID            string
  ReceiverDagqlID     string
  ArgDagqlIDs         []string
  OutputDagqlID       string
  ReturnType          string
  TopLevel            bool
  ParentChainIncomplete bool
}

// Immutable object state (digest-keyed), potentially shared across traces/sessions.
type ObjectSnapshot struct {
  DagqlID         string // immutable DAGQL object ID (dag.output digest / call digest fallback)
  TypeName        string
  OutputStateJSON map[string]any
  FieldRefs       []FieldRef // extracted snapshot references
  StateMissing    bool       // true when referenced but state payload has not been observed yet
}

// Mutable ODAG identity: "binding" of an object through time.
// This is the entity rendered as a DAG node in ODAG views.
type ObjectBinding struct {
  BindingID         string // obj-*
  TypeName          string
  Alias             string // Type#N (ODAG-computed, not telemetry-native)
  ScopeSpanID       string // containment scope anchor
  CurrentDagqlID    string
  Archived          bool // scope/session no longer active
}

// Derived event: binding changed from one immutable state to another.
type BindingMutation struct {
  MutationID       string
  BindingID        string
  CauseCallID      string
  ScopeSpanID      string
  PrevDagqlID      string
  NextDagqlID      string
  StartUnixNano    int64
  EndUnixNano      int64
  Visible          bool
}

// Derived execution scope. This should become the top-level UX entity.
type Session struct {
  ID                string
  Status            string
  Open              bool
  TraceIDs          []string
  FirstSeenUnixNano int64
  LastSeenUnixNano  int64
}

// Derived actor within a session (CLI invocation / SDK client / similar).
type Client struct {
  ID                string
  SessionID         string
  TraceIDs          []string
  RootSpanIDs       []string
  FirstSeenUnixNano int64
  LastSeenUnixNano  int64
}
```

Notes:
1. Avoid synthetic span lifecycle transitions (`STARTED` event rows) unless ingestion actually captures them incrementally; treat span timing/status fields as source-of-truth.
2. `ObjectSnapshot` is immutable and never archived.
3. `ObjectBinding` is mutable and lifecycle-scoped (`Archived` derived from scope/session closure).
4. `FieldRef` storage is normalized as a singular relation:
   - key: `(from_dagql_id, field_name, target_dagql_id)`
   - `UNIQUE` on that key; use standard sqlite upsert semantics for dedupe.
5. Unresolved `target_dagql_id` values are retained:
   - create placeholder `ObjectSnapshot` rows with `StateMissing=true`
   - fill/clear when payload later arrives for that snapshot.
6. Sharing semantics:
   - the same `dagql_id` may appear in multiple traces/sessions.
   - if an object is session/client-isolated, that isolation is part of the emitted `dagql_id`; ODAG should model session/client/trace as properties and link tables, not as a second namespace layer over immutable IDs.
7. Hierarchy semantics:
   - session and client are higher-level execution scopes derived from telemetry.
   - spans and calls belong under a client (or directly under a session if client identity is unavailable).
   - object bindings are derived from calls and are therefore naturally attributable to client/session via those calls.
   - trace is an ingest/debug boundary, not the primary ownership hierarchy for ODAG entities.
8. Derivation contract for session/client:
   - current implementation may use heuristics to detect session/client boundaries from known engine span/resource patterns
   - those rules should be isolated in one derivation module with explicit tests and `derivationVersion` coverage.
9. Current heuristic target:
   - clients should be derived from `dagger.io/engine.client` `connect` spans
   - sessions should be derived from root clients rather than directly from OTel trace IDs
   - until explicit IDs are emitted, derived `Session.ID` and `Client.ID` are synthetic and versioned.

### Conceptual ORM shape

These pseudo-types are not the low-level SQL schema. They describe the full lookup/relationship surface ODAG should support.

```graphql
type Client {
  "Every client is part of a session"
  session: Session!

  "When a module is loaded, it connects back to the engine. These module clients are handled differently"
  isModule: Bool

  "A client may have a parent. If it doesn't, it's the root client of its session"
  parent: Client

  "Every client maps to a connect span"
  connectSpan: Span!

  children: [Client!]

  "Top-level function calls made by this client"
  calls: [Call!]
}

type Session {
  ID: SessionID!

  "Every session has a root client"
  root: Client!
}
```

Notes:
1. `calls` is the conceptual relationship exposed by the API, not a promise that current telemetry makes strict descendant-walk attribution trivial.
2. `Session` is primarily a wrapper around the root-client tree; it exists because the concept is useful in the UI and API.
3. With current telemetry, `parent`, `children`, and `calls` may be heuristic/provisional unless explicit client identifiers are emitted.

### Client Detection Heuristic and Risk

Current state:

1. ODAG can reliably detect candidate clients by finding spans with:
   - scope `dagger.io/engine.client`
   - name `connect`
2. ODAG cannot currently treat the resulting client tree as protocol truth.
3. The parent span of a `connect` span is **not** assumed to be a special "client span"; it is only a regular span executing in some client's context.

Current heuristic:

1. Each `dagger.io/engine.client` `connect` span defines one derived client.
2. Parent client is inferred by walking ancestor spans and finding the nearest ancestor already attributable to another client.
3. Root clients define sessions.
4. Calls and spans are attributed to clients by:
   - structural ownership where possible
   - fallback root-local ordering when ancestry alone is insufficient

Inherent risks:

1. Nested clients are visible, but parentage is only inferred.
2. Later DAGQL call spans often occur after the relevant `connect` span has finished, so strict descendant ownership is incomplete.
3. Concurrent or interleaved clients within one root session may be misattributed.
4. `Client.parent`, `Client.children`, and `Client.calls` should therefore be treated as best-effort derived structure until explicit telemetry exists.

Design consequence:

1. Keep the `Client` / `Session` API shape now because it is the right product model.
2. Make the heuristic and its confidence explicit in the backend contract and UI/debug surfaces.
3. Plan to replace the heuristic with explicit engine-emitted identifiers:
   - session ID
   - client ID
   - parent client ID
   - optional client kind metadata
4. Do not let downstream schema or UX assumptions harden around current heuristic behavior.

### Edge Taxonomy

ODAG needs several different relationship types. They should not all be rendered as the same kind of edge.

#### 1. Structural dependency edges

These are the default object DAG edges.

1. `field_ref`
   - meaning: object state A contains a field whose value references object state B
   - evidence source: emitted `dagger.io/dag.output.state` field `refs`
   - storage level: `dagql_id -> dagql_id`
   - render level: projected to `ObjectBinding -> ObjectBinding` at the selected revision
   - story told: supply chain / composition / "this object currently depends on that object"

This is the main edge type that should be shown in the default ODAG view.

#### 2. Containment relations

These are not object-object edges. They define nested render structure.

1. `contains_call`
   - call A contains child call B
2. `contains_object`
   - a call scope contains an object binding because that call created or mutated it in the current render lens

Containment should be used for:
1. call boxes containing child calls
2. call scopes containing the objects directly created/mutated there

Containment should **not** be inferred from object fields.

#### 3. Provenance / lineage relations

These explain where an object value came from, but they are not persistent structural dependencies.

1. `produced_by`
   - relation: binding mutation -> exact call that returned the new value
   - objective, event-scoped, not subjective
2. `derived_from_receiver`
   - relation: output value was produced by invoking a call on a receiver object
   - example: `foo = obj.bar()`
   - meaning: `foo` is derived from `obj` through call `bar`
3. `derived_from_arg`
   - relation: output value was produced using one or more object-valued call arguments
   - useful for debug/provenance overlays, usually too noisy for the default DAG

Recommended rendering policy:
1. keep these as optional overlays or side-panel facts
2. do not mix them into the default `field_ref` supply-chain graph
3. expose them in the backend render model so alternative views can use them

#### 4. Snapshot-level evidence vs binding-level render

As with `field_ref`, provenance evidence starts at immutable DAGQL state level:

1. call has `receiver_dagql_id`
2. call has zero or more `arg_dagql_id`
3. call has `output_dagql_id`

That evidence is then projected into binding-level relationships at the selected revision.

Implications:

1. A call does not guarantee a visible receiver binding:
   - receiver may be `Query`
   - receiver may be filtered out
   - receiver may not yet have enough state to materialize a visible binding
2. `Query` handling:
   - `Query` is technically an object in DAGQL semantics
   - for ODAG binding projection, v1 may treat `receiver=Query` as "no receiver binding"
   - if useful later, `Query` can be promoted to a first-class root binding or root scope anchor without changing the underlying telemetry model
3. If receiver and output collapse onto the same binding, there is no separate inter-object edge:
   - the relationship is represented as a mutation event on one binding
4. If receiver and output map to different bindings, a provenance edge may be emitted:
   - `receiver_binding -(derived_from_receiver)-> output_binding`

#### 5. Collapse decision tree

The exact collapse policy determines whether a call creates a new binding or mutates an existing one.

Current recommended decision tree:

1. If the call has no object output, emit no binding mutation.
2. If the call has an object output but no receiver binding, create a new binding.
3. If the call has a receiver binding and output type differs from receiver type, create a new binding.
4. If the call has a receiver binding and output type matches receiver type, collapse to a mutation on the receiver binding.
5. Future explicit engine hints should be able to override this heuristic:
   - mutate existing binding
   - create new binding
   - alias existing binding / no-op

Consequences:

1. `produced_by` is always objective at call-event level.
2. `receiver_dagql_id` is always objective at call-event level.
3. `contains_object` is partly view-dependent because it follows the chosen collapse/render lens.
4. `derived_from_receiver` is objective at snapshot/call evidence level, but may disappear as a separate binding edge if collapse merges both sides into one binding.
5. "receiver/output collapse onto the same binding" is an ODAG heuristic, not a telemetry fact.

## Backend API (V2 Source of Truth)

V2 APIs expose global pools + filters. Session/client views are primary; trace-centric endpoints become convenience/debug views.

### Canonical endpoints

```http
GET /api/v2/spans
GET /api/v2/calls
GET /api/v2/object-snapshots
GET /api/v2/object-bindings
GET /api/v2/mutations
GET /api/sessions
GET /api/v2/clients
GET /api/pipelines
GET /api/shells
GET /api/pipelines/object-dag
```

Common query parameters:
1. `traceID`, `sessionID`, `clientID`
2. `from`, `to` (unix nano)
3. `limit`, `cursor`
4. `includeInternal=true|false`

Convenience views (kept for compatibility):
1. `/api/traces` and `/api/traces/{id}/meta`
2. `/api/traces/{id}/events`
3. `/api/traces/{id}/snapshot?t=...|step=...`
4. These remain useful for import, plumbing debug, and exact OTel boundary inspection, but should not dominate the main UX.

Retired render-model routes:
1. The old generic render-model routes (`/api/v2/render`, `/api/v2/views/{view}/render`) were useful during V2 exploration.
2. They are no longer part of the active V3 server surface after the pipeline DAG cutover.
3. Specialized V3 entity views should use dedicated endpoints and authoritative substrate facts instead of reviving that generic render layer.

### Derivation/versioning

1. Every derived entity set is associated with a `derivationVersion` for reproducibility.
2. Unknown or partial ancestry is surfaced explicitly (`parentChainIncomplete`) rather than hidden.
3. Clients should not assume derived identities are immutable across derivation-version changes.

## Algorithms

### 1) Ingest and normalize DAGQL call spans

For each incoming span:

1. Read attributes.
2. If `dagger.io/dag.digest` and `dagger.io/dag.call` are present, decode `callpbv1.Call`.
3. Keep a `SpanRecord` index by span ID and call digest.
4. Track parent span graph even for non-DAGQL spans (needed for top-level detection).
5. Parse emitted serialized object state payload for produced objects and extract field object-ID refs.
6. For each extracted ref target, ensure a snapshot row exists (placeholder with `StateMissing=true` when payload is missing).
7. Build a lookup index `stateID -> typed field map` for node expansion and recursive exploration.
8. If a span has `dagger.io/dag.output` but no `dagger.io/dag.output.state`, resolve from local state cache.
9. If state payload is still unavailable, materialize object node as `state unavailable`, with no dependency edges for that state.
10. If missing state payload arrives later for a known state ID, retroactively backfill that state in-place (same object/node identity) and recompute dependencies/history for affected timeline frames.

### 2) Detect top-level DAGQL function spans

Definition: DAGQL function call span that is not the child (at any ancestor depth) of another DAGQL function call span.

Algorithm:

1. Candidate set: spans with decoded DAGQL call payload.
2. For each candidate, walk parent chain until root.
3. If any ancestor span is also a DAGQL call span, candidate is not top-level.
4. Remaining spans are top-level.

This avoids false negatives when DAGQL span nesting includes passthrough/internal non-call spans between DAGQL calls.
If parent ancestry is incomplete in collected data, flag that on events (`parentChainIncomplete`) so UI/debugging can audit classification confidence.

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
   - for each `(fieldName, targetStateDigest)` in emitted state payload refs, map referenced state -> object and emit `referenced object -> current object`
   - edge label is field name (e.g. `Mounts`)
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

Discrete history mode (default UI):

1. Build a revision list from projected events in reverse chronological order.
2. Selecting a revision requests `GET /api/traces/{traceID}/snapshot?t=<event_end_unix_nano>`.
3. Revision index is the canonical navigation unit; absolute time is displayed as supporting context.
4. Default selection starts at the latest revision for a newly opened trace.

### 7) Event model for debugability

The UI event stream intentionally carries multiple abstraction layers in one row:

1. **RAW layer**: every span (`rawKind=span`) for plumbing/debug context.
2. **CALL layer**: DAGQL call metadata when present (`rawKind=call`, call name/digest/types).
3. **DERIVED layer**: ODAG operation when applicable (`operation=create|mutate` + `objectID`).
4. **Visibility signals**: `visible`, `topLevel`, `callDepth`, and nearest parent DAG call metadata.

This model keeps ODAG rendering explainable when filtering/pruning hides many events or objects.

### 8) Derive sessions and clients from current telemetry

Current telemetry does not export explicit session/client IDs into OTEL span payloads, so ODAG must derive them behind a narrow heuristic boundary.

Reliability assessment:

1. Root client detection is reasonably reliable:
   - `dagger.io/engine.client` `connect` spans are clearly identifiable.
2. Nested client detection is partially reliable:
   - child module/client connects do appear as spans and can often be found.
3. Parent-child client hierarchy is not reliably encoded in raw span ancestry:
   - a child client's `connect` span is often nested somewhere inside the parent client's work, but not under the parent client's own `connect` span
   - later DAGQL query/call spans also often occur after the relevant `connect` span has completed
4. Therefore:
   - client hierarchy can be approximated
   - client call ownership can be approximated
   - neither should be mistaken for exact protocol truth until explicit IDs are emitted.

Client derivation:

1. Find spans where:
   - scope name is `dagger.io/engine.client`
   - span name is `connect`
2. Each `connect` span becomes one derived client lifecycle anchor.
3. `Client.ID` is synthetic and stable within a derivation version: `client:<trace_id>/<connect_span_id>`.
4. Enrich derived client labels from the connect span resource:
   - `service.name`
   - `service.version`
   - `process.command_args`
   - `dagger.io/client.version`
   - `dagger.io/client.os`
   - `dagger.io/client.arch`
   - `dagger.io/client.machine_id`
5. Derive parent client from span ancestry:
   - do **not** treat the parent span of `connect` as a special "client span"
   - instead, treat it as an ordinary span executing in some client's context
   - walk ancestors of the `connect` span
   - find the nearest ancestor span that is already attributable to another client
   - if found, that owning client is the parent
   - otherwise the client is a root client
6. Confidence:
   - this yields a plausible tree in many observed traces
   - it is not guaranteed correct by current telemetry semantics.

Session derivation:

1. Every root client defines one session.
2. `Session.ID` is synthetic and stable within a derivation version: `session:<root_client_id>`.
3. A non-root client inherits the session of its nearest root-client ancestor.
4. Fallback: if a trace has calls but no client connect span, synthesize one root session for that trace and attach orphan calls/spans directly to it.

Call/span attribution:

1. Prefer structural attribution over flat time-window grouping.
2. Use the client tree as the primary hierarchy:
   - a span belongs to the nearest client whose connect context contains it
   - a child client boundary steals ownership for its own subtree
3. In current traces, connect spans often complete before later `POST /query` / DAGQL call spans begin, so descendant-walk alone is insufficient.
4. Where strict ancestry does not settle ownership, use root-local ordering as a fallback:
   - within one root-client session, a call/span belongs to the latest client anchor that started before it and is not shadowed by a more specific child-client attribution
5. Top-level DAGQL calls exposed on `Client.calls` are those attributed to that client after this ownership pass.
6. Confidence:
   - this is useful for exploration and debugging
   - it is not strong enough to be the sole source of truth for persistent client hierarchy semantics.

Heuristic boundaries:

1. This logic must live in one dedicated derivation module.
2. It must be guarded by `derivationVersion`.
3. It must be easy to replace with explicit telemetry once the engine emits true session/client IDs.
4. If multiple concurrent clients share one session root and interleave heavily, fallback ordering heuristics may misattribute some calls; ODAG should surface that as derived behavior, not as protocol truth.
5. If correctness of client hierarchy matters to the product model, emit explicit telemetry:
   - `dagger.io/dag.session_id`
   - `dagger.io/dag.client_id`
   - `dagger.io/dag.parent_client_id` on `connect` spans
   - optional `dagger.io/dag.client_kind` (`root`, `module`, `nested-sdk`, ...)

## Standalone App Architecture

### Components

1. **Backend (Go, local process)**
   - Reuse `internal/cloud/auth` and `internal/cloud/trace` for auth + stream subscription.
   - Build ODAG transformer service and expose JSON/WS APIs.
2. **Frontend (web SPA)**
   - Workflow-style graph canvas + left revision-history pane.
   - Card-selection expansion for object state fields.
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
   - revision-history rendering and object-card expansion

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
3. `odag rebuild`
   - Global rebuild command for ODAG derived state.
   - Deletes all derived ODAG data and recomputes it in one pass from source-truth telemetry/span data already stored locally.
   - This is the default operator workflow after derivation/schema changes; do not rely on piecemeal endpoint-specific repair as the primary rebuild story.
4. Persistent store behavior
   - Store traces across restarts.
   - List traces with metadata (trace ID, first/last seen, source mode, status).
   - Select a stored trace for replay/visualization in UI.

## Frontend Stack

Current implementation uses an embedded static web app (no external frontend toolchain):

1. **Current prototype stack**
   - server-embedded HTML/CSS/JS assets
   - SVG graph rendering and interaction in plain JS
   - no separate npm/vite build step; optional `odag serve --dev` file-watch reload
2. **Visual profile**
   - Dark dotted background
   - Rounded object cards with edge routing and event/object selection cues
   - card expansion on selection to reveal full object state fields
3. **Future candidate stack (if complexity grows)**
   - React + TypeScript + Vite + shadcn/tailwind for UI composition
   - React Flow + ELK for richer layout/interaction and potential editor-like affordances

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

1. **V3 shell**:
   - left: entity-domain nav
   - top-right: secondary nav for the active domain
   - bottom-right: specialized content pane for that domain
2. **Home route behavior**:
   - start mock-first while entity discovery is being built
   - use the shell to settle taxonomy, labels, and per-domain subviews before wiring real backend derivations
3. **Supporting drill-down hierarchy**:
   - sessions/clients/calls/object bindings remain important supporting scopes
   - they are substrate and drill-down tools, not the only top-level home-page taxonomy
4. **Trace page layout**:
   - left: revision history list (events)
   - right: ODAG graph panel
   - top center: trace title/subtitle context
   - this is a secondary/debug-oriented route, not the default way users should enter the data.
5. **Trace list page**:
   - table layout (`trace`, `created`, `spans`, `status`)
   - relative creation time and dot-based status signaling

### Node visual language

1. Node title: object alias (`<Type>#N`).
2. Collapsed state: title only.
3. Expanded state (selected): full field list, one field per row.
4. State badge:
   - `running` (active mutation in flight)
   - `cached`
   - `failed`
   - `stable`

### Interactions

1. Click history row to set current revision and redraw graph at that boundary.
2. Click node to toggle expansion and select object.
3. Toggle filters on history stream:
   - calls / derived / visible row filtering
4. Dual selection cues must co-exist clearly:
   - current event highlight (and mutated-object badge/ring)
   - selected object highlight (and matching history rows)

Current event row details should show:
1. mutation call identity (`type.field`, span ID/time)
2. parent DAGQL call context (when present)
3. raw span identity/kind and visibility classification

### Containment vs dependency (for future rendering exploration)

To support "enter a box" UX without semantic ambiguity:

1. **Dependency edge (`A -> B`)**:
   - means object/reference relationship (field/input/receiver/output reference).
   - this is graph connectivity, not ownership.
2. **Containment (`inside X`)**:
   - primary containment axis is **call scope** (span subtree / call subtree).
   - "show objects created inside this function call" is strict containment.
3. **Object-centric lens (`inside object`)**:
   - implemented as a filtered view over calls/mutations where object is receiver or direct cause path.
   - this is a lens, not structural ownership.
4. **Russian-doll / zoom-in story**:
   - entering a call opens a nested call-scoped ODAG subgraph.
   - entering an object applies object-centric lens on top of current call scope.
   - both can compose, but containment remains call-first for deterministic semantics.

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
2. **Session/client heuristic derivation vs explicit telemetry**
   - Heuristics are acceptable for clearly engine-defined scopes and unblock the session-first UX.
   - They must remain encapsulated; once explicit telemetry exists, the heuristic layer should be replaceable without reshaping downstream APIs.
   - Current best heuristic is structurally clean but not semantically perfect: client ownership is inferred from `dagger.io/engine.client connect` anchors plus root-local time windows.
   - If the client tree becomes central to the product model, explicit telemetry should replace heuristics early rather than after the API solidifies.
3. **Apply mutation at end-time vs start-time**
   - End-time is semantically safer.
   - Start-time can feel more “live” but can show speculative state.
4. **Top-level seed filter strictness**
   - Strict seed filter reduces clutter strongly.
   - May hide useful transitive context unless “include neighbors” is available.
5. **Backend transform vs browser-only transform**
   - Backend transform allows reuse of Go internals and auth.
   - Browser-only would simplify deployment but complicate CORS/auth and protobuf decode parity.
6. **Payload richness vs telemetry overhead**
   - Emitting object state payloads gives accurate dependencies and simpler UI semantics.
   - Payload size and serialization cost must be bounded for large traces.

## Unknowns and Open Questions

1. **dagger.io dagviz reuse**: which components/algorithms can be imported directly once repository access is confirmed?
2. **Very large traces**: expected practical upper bounds (span count/object count) for v1 UX targets.
3. **Collector transport expansion**: when to add OTLP gRPC ingest in addition to HTTP/protobuf.
4. **Top-level certainty with partial ancestry**: some events show `parentChainIncomplete`; classify conservatively and expose debug context in UI.
5. **Concurrent clients within one session root**: current time-window client assignment may be insufficient if future traces interleave multiple clients more aggressively.
6. **Explicit telemetry follow-up**: once engine spans export true session/client identifiers, the heuristic derivation should be removed rather than further complicated.
7. **Client hierarchy correctness**: without explicit `parent_client_id`, nested-client relationships remain best-effort.

## Handoff Snapshot

These are the latest design decisions that should be preserved across handoff.

1. **Immutable ID terminology**
   - use `dagql_id` for immutable object-state IDs
   - do not let `snapshot_id` linger as the primary name in new schema/API design
2. **State payload contract**
   - `dagger.io/dag.output.state` is new and can hard-cutover
   - engine should emit per-field `refs`
   - backend should treat `refs` as authoritative and stop inferring nested dependency paths
3. **Edge taxonomy**
   - default object DAG edge is `field_ref`
   - containment is separate (`contains_call`, `contains_object`)
   - receiver/arg lineage is provenance overlay, not default DAG structure
4. **Ground truth vs ODAG inference**
   - call receiver/args/output are telemetry facts
   - bindings, mutations, and collapse decisions are ODAG derivations
   - "receiver/output collapse onto the same binding" is heuristic, not protocol truth
5. **`Query` handling**
   - `Query` is technically an object
   - v1 may treat `receiver=Query` as "no receiver binding"
   - this should be implemented as a projection choice, not as a claim about underlying telemetry
6. **Execution scope model**
   - target API shape is client-tree first, sessions derived from root clients
   - current telemetry does not make parentage/call ownership fully reliable
   - explicit `session_id` / `client_id` / `parent_client_id` telemetry is the intended long-term fix
7. **Client heuristic honesty**
   - keep the `Client` / `Session` model in the API because it matches the product direction
   - keep heuristic risk explicit in the contract and UI/debug surfaces
   - do not overfit persistence or UX to current heuristic behavior
8. **Trace role**
   - trace remains useful as ingest/debug/import boundary
   - it should not become the dominant user-facing silo in the v2 architecture
9. **V3 home UX**
   - organize the default shell around discovered entity domains, not only sessions/clients or raw objects
   - allow each domain to define its own specialized view and heuristics
10. **`object` vs `entity`**
   - keep `object` as the strict ODAG substrate term
   - use `entity` for broader domain-specific derived concepts in the V3 shell
11. **Discovery before visualization**
   - first answer what a span/call cluster represents
   - only then project object relationships and specialized visuals inside that domain
12. **Design-doc continuity**
   - keep this document current at every milestone
   - someone picking up mid-stream should be able to see current mockups, active implementation target, and the next open question here
13. **Pipeline definition**
   - `Pipeline` means one client-submitted unit of work that executes a connected DAGQL call chain toward one intended result
   - `dagger call` is a pipeline; one interactive command inside `dagger shell` is also a pipeline
   - ambiguous multi-command batch cases (`dagger -c`, shebang interpreter flows) stay split per top-level command until V3 has an explicit multi-output model
   - CLI output handling triggered by that result still belongs to the same pipeline context even when the follow-up spans are not part of the DAGQL chain

## Implementation Plan

### Stage Checklist (Execution Status)

- [x] Stage 1: CLI/server/store scaffold (`odag serve`, `odag run`, sqlite schema, health endpoint)
- [x] Stage 2: OTLP ingest mode (trace/span persistence from `/v1/traces`)
- [x] Stage 3: Backend trace APIs (list/get/events) + ODAG projection model
- [x] Stage 4: Web UI shell + revision history + ODAG canvas
- [x] Stage 5: Cloud pull mode + polish (tests, docs, UX refinements)
- [x] Stage 6: Backend render-model API (`/api/v2/render`, `/api/v2/views/{view}/render`; later retired from the active V3 surface)
- [x] Stage 7: V3 entity-first shell scaffold with mock data
- [x] Stage 8: Implement `Pipeline` end-to-end through derivation, API, and UI
- [x] Stage 9: Capture lessons learned from `Pipelines` and choose the next domain
- [x] Stage 10: Implement `Shells` end-to-end through derivation, API, and UI
- [x] Stage 11: Capture lessons learned from `Shells` and choose the next domain
- [x] Stage 12: Add clean V3 entity routes and detail-page scaffolding
- [x] Stage 13: Expose `Sessions` as a first-class V3 primitive
- [x] Stage 14: Rename `CLI Runs` to `Pipelines` and focus detail pages on object DAGs
- [x] Stage 15: Implement `Workspace Ops` end-to-end through derivation, API, and UI
- [x] Stage 16: Implement `Git Remotes` end-to-end through derivation, API, and UI
- [x] Stage 17: Implement `Services` end-to-end through derivation, API, and UI

### Active Next Tasks

- [x] Engine telemetry hard cutover: change `dagger.io/dag.output.state` payload to include per-field `refs` and bump payload version.
- [x] Backend derivation: consume engine-provided `refs` as authoritative and remove fallback dependency extraction heuristics based on nested path walking.
- [x] Backend/API naming pass: rename immutable ID fields from `snapshot_id` to `dagql_id` across derived sqlite schema and REST JSON models.
- [x] Replace current `session == trace` approximation with a client tree derived from `dagger.io/engine.client` `connect` spans, then derive sessions from root clients; keep trace routes as secondary/debug views.
- [x] Encapsulate session/client heuristics in a dedicated derivation layer with tests and `derivationVersion` coverage, including parent-client inference from span ownership plus fallback root-local ordering for unresolved call attribution.
- [x] Materialize the edge taxonomy in the backend model:
  - `field_ref` as default object-object dependency
  - call/object containment relations
  - receiver/arg provenance as optional overlays, not default DAG edges
- [x] Implement `Query` receiver handling explicitly in backend projection:
  - default v1 behavior: treat as no receiver binding
  - keep room for later promotion to a root binding/root scope anchor if useful
- [x] Add global rebuild workflow for derived data:
  - `odag rebuild`
  - delete all derived ODAG data
  - recompute derived state in one pass from stored source-truth telemetry/span data
- [x] Add explicit engine OTEL telemetry for true execution-scope identifiers:
  - session ID on relevant spans
  - client ID on relevant spans
  - parent client ID on `dagger.io/engine.client` `connect` spans
  - optional client kind metadata
- [x] Once explicit execution-scope telemetry lands, remove or heavily downgrade client/session heuristics and treat emitted IDs as the only source of truth.
  - Legacy traces without emitted scope IDs may still use the old derivation path as a backward-compatibility fallback.
- [x] Pivot the current web shell from object-type language to entity-domain language:
  - left nav lists discovered domains
  - secondary nav selects specialized subviews
  - mock data is acceptable while settling taxonomy
- [x] Implement the `Pipeline` domain (`dagger call` style submitted call chain) end-to-end:
  - [x] derive run identity from command root plus chained DAGQL calls
  - [x] attach terminal output type and CLI post-processing behavior
  - [x] keep follow-up apply/export/prompt spans associated with the same pipeline context in the first read-only API slice
  - [x] wire the V3 shell's `Pipelines` domain to the real endpoint
  - [x] route inventory rows to clean `/pipelines/<short-id>` detail pages
  - [x] reduce the pipeline detail page to a thin recap plus an object DAG view
- [x] Record what did and did not generalize from the first domain before adding a second one.
- [x] Implement the `Workspace Ops` domain end-to-end:
  - [x] derive workspace-op identity from host-bound call patterns and side-effect spans
  - [x] keep exports/imports/host-access attached to workspace roots rather than scattering them as unrelated follow-up noise
  - [x] wire the V3 shell's `Workspace Ops` domain to the real endpoint
- [x] Implement the `Git Remotes` domain end-to-end:
  - [x] derive remote identity from authoritative module refs, load-module spans, and explicit git calls
  - [x] normalize remote identity at repository/module-ref granularity rather than per revision
  - [x] wire the V3 shell's `Git Remotes` domain to the real endpoint
- [x] Implement the `Services` domain end-to-end:
  - [x] derive service identity from authoritative `Service` output-state objects plus related DAGQL calls
  - [x] keep service detail focused on definition and activity rather than generic object rendering
  - [x] wire the V3 shell's `Services` domain to the real endpoint

Stage 2 implementation note:
- `/v1/traces` now decodes OTLP HTTP/protobuf and upserts trace/span records in sqlite.
- `/v1/logs` and `/v1/metrics` are currently compatibility no-op endpoints (`202 Accepted`) so standard OTEL env wiring works without exporter failures.
- Server now emits simple lifecycle logs for client connect/disconnect and OTLP trace upload start/completion (per trace ID in each ingest batch).
- Trace lifecycle/status robustness improvements:
  - OTLP parent span IDs that are all-zero (`000...`) are normalized to empty, so root spans are correctly recognized as roots.
  - Root detection in trace summarization also treats all-zero parent IDs as root-equivalent for backward compatibility with previously ingested rows.
  - Store now runs stale-status reconciliation (`ingesting` -> `completed`/`failed`) with two safety nets:
    - close-grace timeout for traces with no open spans and no recent updates
    - hard stale timeout for traces that remain ingesting long after the last update.

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
- Historical note: this v2-first shell was later hard-replaced by the Stage 7 V3 entity shell below.
- `odag serve` now hosts an embedded web UI with split routes (no external frontend build step required for the local experiment):
  - `/` v2 global explorer page (no required trace silo)
  - `/traces/{traceID}` dedicated trace view page for maximum ODAG canvas space
  - optional dev mode: `odag serve --dev [--web-dir ...]` serves frontend assets from disk and injects browser auto-reload on local file changes
  - UI includes:
    - index page is now a simpler v2-first data explorer:
      - loads `/api/v2/*` entities without requiring `traceID`
      - keeps a compact top toolbar for:
        - optional trace filter
      - include-internal toggle
      - cloud trace import
    - reduces home-route navigation to two page-like views:
      - `Objects`
      - `Events`
      - `Objects` page is a wide table-first view over object bindings, with trace/session/client folded into columns on each row
      - `Events` page is a unified table over call, mutation, and span activity rows
      - both pages support row search, basic client-side sort/filter controls, and clickable trace/session/client chips for scope narrowing
      - row-level DAG entry actions now open a dedicated `/dag` page:
        - object rows open `mode=object` with `focusObjectID=<objectID>`
        - call and mutation rows open `mode=scope` with `scopeCallID=<callID>`
        - links carry the current trace/session/client scope when available
        - explicit row drill-ins default `keepRules=off` so the selected object/call cannot be pruned away before render
    - `/dag` is now a separate object-graph page backed by `/api/v2/render`:
      - graph defaults to a global object DAG across all traces when opened without scope parameters
      - global DAG uses `object-bindings` plus `object-snapshots` to synthesize the current object set and current `field_ref` edges
      - global view defaults to `live objects only`
      - graph defaults to structural `field_ref` edges only
      - containment and provenance remain out of the default view
      - scoped render entry keeps `keepRules=default` and `dependencyHops=1`
      - if an explicit object/call drill-in still resolves to an empty graph, the page retries once with pruning disabled
      - selected object details appear in a sidebar inspector, and clicking a node also expands that card inline to show its fields
  - navigation polish:
    - trace list "Open" actions now use regular links for native browser-history behavior
    - trace-page top-left back control now prefers `history.back()` (same-origin referrer), with `/` fallback
  - dedicated trace page now uses a left-side revision history pane (replacing top step controls and bottom event stream)
  - trace navigation back to list uses a small top-left back-arrow control (unobtrusive, conventional placement)
  - frontend live-refreshes via polling:
    - home-route tables update automatically when new rows arrive for the active scope
    - trace page updates revision history as new events arrive
    - current selected revision is preserved (no forced auto-follow to latest)
  - selecting a history item moves the DAG snapshot to that event boundary time
  - history pane includes checkbox filters for `calls`, `derived`, and `visible`
  - history cards use table-like aligned columns (kind/call/parent/visible/time) while keeping card styling
  - dual selection cues are explicit and composable:
    - current-event selection highlights the event row and marks the mutated object with an event badge/ring
    - selected-object selection uses a distinct object contour color and highlights all history rows that mutate that object
  - dedicated central trace title row above the DAG canvas
  - ODAG object cards are expandable on selection: collapsed cards show identity only; selected card expands and renders the full state field list (one field per line)
  - object cards show ODAG alias (`Type#N`) as primary label; immutable state digest text is hidden from card body
  - when state payload is unavailable, selected object cards now show fallback metadata (`snapshot count`, `activity call count`) instead of only a single warning line
  - trace view drops the inspector pane to maximize graph/history real estate

Stage 5 implementation note:
- Cloud pull mode is implemented in both CLI and backend API:
  - `odag fetch <traceID> [--org ...]`
  - `POST /api/traces/open` with `{ "mode": "cloud", "traceID": "...", "org": "..." }`
- Web UI now exposes Cloud import controls (trace ID + optional org) and refreshes local trace list after import.
- `odag run` now isolates ambient trace context by default (clears inherited `TRACEPARENT`/`TRACESTATE`/`BAGGAGE` and legacy `OTEL_TRACE_*`) so nested invocations create a fresh trace; `--inherit-trace-context` restores chaining behavior when desired.
- `odag run` now handles interrupts robustly:
  - captures `Ctrl-C`/`SIGTERM`
  - forwards interrupt to child process
  - escalates to kill after a short grace timeout if child does not exit
  - suppresses usage spam on interrupt (`SilenceUsage`) for cleaner UX.
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
- Active design decision (pending implementation): hard-cutover this schema to include per-field `refs`:
  - `fields` entry target shape: `{ name, type, value, refs }`
  - `refs` is the authoritative source for dependency extraction in ODAG backend materialization.
- ODAG consumes these attributes when present and marks missing payloads as `missingState`.
- Engine telemetry output-state encoding is hardened against typed-nil or panicking `dagql.IDable` and `dagql.Typed` values to prevent resolver panics; serializer now fails closed for those fields instead of crashing query execution.

Post-MVP projection refinement:
- Default rendering now excludes `dagger.io/ui.internal=true` spans/events from seed scope and UI event stream to reduce noise.
- Object projection ignores scalar outputs (e.g. `String`, `Int`, `Boolean`, `Float`, `JSON`, `Void`) even if older traces contain `dag.output` for them.
- Mutation collapse now tolerates module-qualified type names (e.g. `ModuleSource` vs `mymod.ModuleSource`) via normalized type matching, reducing false "create" splits in chains.
- Default keep rules now prune top-level non-`Query.*` call-only fan-out objects and non-top-level-only objects, while preserving top-level writes and mutation-heavy top-level outputs.
- Event debug fields now include call-depth and nearest parent DAG call metadata to audit top-level classification directly in UI.
- Projection now rehydrates `dag.output.state` payloads by immutable output digest when emitters dedupe repeated IDs, avoiding false `state unavailable` flags on duplicate-return spans.
- Projection now derives object dependency edges (`kind=field-ref`) from emitted output-state field references:
  - `fromObjectID` = object owning the referencing field
  - `toObjectID` = object referenced by the field value
  - edge evidence is aggregated across object-state history (`evidenceCount`)

### Phase 0: Spike

1. Implement ODAG transformer against recorded span fixture.
2. Define emitted object-state telemetry payload format and parser (deterministic protobuf + base64, versioned).
3. Measure transform + layout time with realistic payload sizes.

### Phase 1: Local standalone MVP

1. Add local Go server (`odag serve`) with persistent trace store + trace open/list/stream endpoints.
2. Implement Mode A (cloud trace pull by trace ID).
3. Implement Mode B (OTLP collector ingest endpoints).
4. Add frontend with embedded web assets (SVG ODAG canvas + revision history UI).
5. Implement top-level seed filter and default pruning heuristics.
6. Support discrete event-step navigation via history selection.
7. Add convenience wrapper (`odag run <command...>`) that injects OTEL env vars.

### Phase 2: Scale and robustness

1. Incremental ODAG diff updates.
2. Performance optimizations (virtualization, edge culling, workerized layout).
3. Better edge/type labeling and mutation-heuristic overrides.

### Phase 2.5: Source-of-truth API stabilization

1. Add `/api/v2/*` global entity endpoints (spans, calls, snapshots, bindings, mutations).
2. Keep `/api/traces/*` as compatibility views backed by v2 entities.
3. Add derivation-version metadata and migration tests for deterministic behavior.

Phase 2.5 implementation note:
1. Implemented initial read-only `/api/v2/*` endpoints:
   - `/api/v2/spans`
   - `/api/v2/calls`
   - `/api/v2/object-snapshots`
   - `/api/v2/object-bindings`
   - `/api/v2/mutations`
   - `/api/v2/sessions`
   - `/api/v2/clients`
2. Current active V3 entity routes later standardized on:
   - `/api/sessions`
   - `/api/pipelines`
   - `/api/shells`
   - `/api/pipelines/object-dag`
3. Responses now include `derivationVersion` (`odag-v2alpha1`) and pagination cursor support (`cursor` as offset token + `nextCursor`).
4. V2 derivation uses `ProjectTraceWithOptions` with keep-rules disabled so object/call pools are not constrained by the trace-view pruning heuristics.
5. Existing `/api/traces/*` endpoints remain unchanged and continue to serve the current UI.
6. Historical note: this phase also added generic render-model endpoints backed by the same v2 projection core:
   - `/api/v2/render` with explicit `mode` query parameter
   - `/api/v2/views/{view}/render` with route-selected rendering universe
   - these routes were later removed from the active V3 server surface after specialized entity endpoints replaced them
7. Render response now carries precomputed containment + dependency structure (`calls`, `objects`, `edges`, `navigation`) so frontend views can iterate quickly with minimal local derivation logic.
8. Render response now also carries trace context and object-state details needed for direct UI consumption:
   - trace header fields (`traceTitle`, `traceStartUnixNano`, `traceEndUnixNano`)
   - active call IDs at snapshot time
   - object `currentState` and `snapshotHistory`
9. At that stage, the trace page data fetch path used `/api/v2/render` (global mode) for initial load, revision selection (`t`), and live refresh, reducing reliance on compatibility `/api/traces/{id}/snapshot` shaping.
10. Trace page now requests `keepRules=default` for artifact-centric readability in large traces while preserving raw full-pool access for API/debug consumers.
11. History filter defaults were tuned for legibility (`derived=true` by default) so raw-span noise does not dominate first render.
12. Real smoke run (`odag run -- dagger -c 'container | from alpine | with-exec -- sh -c "echo hi" | stdout'`) validated pruning impact on a 3069-span trace:
   - full pool: 389 objects
   - render with `keepRules=default`: 5 objects

Stage 7 implementation note:
1. Current root web UI is a hard-cutover V3 shell rather than the earlier v2-first explorer.
2. The shell is intentionally mock-first while the entity taxonomy settles.
3. Current left-nav domains are:
   - Terminals
   - Services
   - Repls
   - Checks
   - Workspaces
   - Sessions
   - Pipelines
   - Shells
   - Workspace Ops
   - Git Remotes
   - Registries
4. Current V3 shell simplification:
   - no right-pane secondary nav for now
   - the selected domain renders directly to its inventory table in the right pane
   - evidence/relations remain useful API concepts, but are not currently first-class UI tabs
   - the right pane avoids redundant `Inventory` labeling and extra top-right status badges; the selected domain name already provides enough context
   - the top-left sidebar now keeps only a tiny brand block (`icon + product title`) above the domain list; no explanatory copy and no search/filter field until those prove necessary
   - mocked domains are labeled directly in the left nav with a small `mock` badge so unwired taxonomy areas are visible at a glance
   - the sidebar footer remains pinned below the domain list; the domain list itself owns scrolling so long inventories do not overlap footer cards
5. The next implementation milestone is to pick one domain and wire it end-to-end through discovery, API shaping, and right-pane inventory rendering.

Stage 8 implementation note:
1. First real Pipeline API slice is implemented at `GET /api/pipelines`.
2. Current derivation rule:
   - classify a client as a Pipeline when its `process.command_args` parse as `dagger call`
   - use ordered client-owned DAGQL calls as the pipeline chain
   - use the latest owned call as the terminal output
   - attach later non-call spans in the same client scope as follow-up evidence
3. Current payload includes:
   - run identity and scope (`runID`, `traceID`, `sessionID`, `clientID`, `rootClientID`)
   - command summary (`command`, `commandArgs`, `chainLabel`)
   - terminal output (`terminalCallID`, `terminalCallName`, `terminalReturnType`, `terminalOutputDagqlID`)
   - follow-up classification (`postProcessKinds`, `followupSpanIDs`)
   - per-pipeline `evidence` and `relations` lists for direct UI consumption
4. Validation coverage currently proves:
   - explicit execution-scope IDs still work across traces
   - one `dagger call` client becomes one Pipeline
   - Changeset follow-up spans remain attached to that run instead of appearing as unrelated noise
5. V3 shell wiring is now live for the `Pipelines` domain only:
   - the left-nav entry still participates in the shared mock shell
   - selecting `Pipelines` fetches real data from `/api/pipelines`
   - the right pane now renders the real Pipeline inventory directly
   - shell chrome reflects the current domain state (`Mock`, `Activating`, `Live Domain`, `Hybrid Degraded`) instead of pretending the entire app is still mocked
   - the inventory columns now emphasize full command, session, relative start time, and output type; final-call details stay on the per-pipeline page
6. Current Stage 8 result is intentionally hybrid:
   - `Pipelines` is live end-to-end
   - all other domains remain mocked
7. Stage 9 should focus on what generalized and what did not:
   - using client-owned call order instead of existing `TopLevel` semantics was necessary for CLI chain reconstruction
   - same-client follow-up spans are useful, but conservative by design and probably still incomplete
   - the shared shell can host one live domain without forcing the rest of the taxonomy to crystallize too early
8. Current status semantics:
   - `Pipeline` row status is derived from the terminal DAGQL call outcome plus ingesting/not-ingesting state
   - attached follow-up spans remain evidence on the same entity, but do not by themselves flip a successful pipeline to `failed`

Stage 9 implementation note:
1. What generalized well from the first real domain:
   - explicit session/client/root-client telemetry is the right substrate for entity discovery
   - per-entity `evidence` and `relations` payloads are immediately useful in the shell without extra adapter layers
   - one live domain can coexist with mocked domains inside the same shell, which keeps taxonomy work moving without demanding a full backend cutover
2. What did not generalize:
   - object-centric assumptions are still too narrow for execution entities
   - generic table renderers are not enough on their own; each live domain still needs its own primary cues surfaced first
   - shell chrome must reflect per-domain live state, otherwise hybrid mode becomes misleading
3. Next domain choice: `Shells`.
4. Rationale for `Shells` as the second live slice:
   - it is adjacent to `Pipelines` because both start from CLI command identity and explicit execution scope
   - it forces V3 to handle a session-centric entity that does not have one singular terminal output
   - it exercises client trees and session continuity directly, which are core V3 substrates we need to validate early

Stage 10 implementation note:
1. First real Shell API slice is implemented at `GET /api/shells`.
2. Current derivation rule:
   - classify a shell root when a root client's `process.command_args` parse as `dagger shell`
   - do not promote descendant/nested clients into standalone shell entities even if they inherit the same resource-level command args
   - aggregate descendant clients, calls, and non-call spans under that shell root
3. Current payload includes:
   - shell identity and scope (`shellID`, `traceID`, `sessionID`, `clientID`, `rootClientID`)
   - command summary (`command`, `commandArgs`, `mode`, `entryLabel`)
   - session extent (`startUnixNano`, `endUnixNano`, `status`)
   - descendant activity (`childClientIDs`, `callIDs`, `callCount`, `spanCount`, `activityNames`)
   - per-shell `evidence` and `relations`
4. Validation coverage currently proves:
   - one `dagger shell` root client becomes one shell entity
   - descendant module clients stay attached to that entity instead of being misclassified as independent shells
   - shell activity aggregates both DAGQL calls and non-call spans from the same explicit client tree
5. V3 shell wiring is now live for the `Shells` domain:
   - selecting `Shells` fetches real data from `/api/shells`
   - the right pane now renders the real Shell inventory directly
   - shared shell chrome now reflects multiple live domains rather than assuming only one live slice exists
   - shell layout now constrains the app to a true viewport-height frame so the main pane scrolls correctly instead of trapping long tables below the fold
6. Current hybrid state after Stage 10:
   - `Pipelines` is live end-to-end
   - `Shells` is live end-to-end
   - all remaining domains remain mocked

Stage 11 implementation note:
1. What generalized across both `Pipelines` and `Shells`:
   - explicit session/client/root-client telemetry is still the best substrate for execution-centric domains
   - one shared live-domain shell path in the UI can host multiple real domains without special-casing the overall app structure
   - entity-local `evidence` and `relations` remain a useful common API contract even when the primary cues differ per domain
2. What did not generalize:
   - resource-level `process.command_args` can bleed into descendant clients, so promotion rules must stay domain-specific and root-aware
   - execution-centric domains can share substrate but still require distinct primary cues (`terminal output` for `Pipelines`, `client tree continuity` for `Shells`)
3. Next domain choice: `Workspace Ops`.
4. Rationale for `Workspace Ops` as the third slice:
   - it is the first strongly external-resource-centric domain in the V3 sequence
   - it tests whether V3 can move beyond command/session identity into host-side side effects such as `File.export`, `Directory.export`, and `Host.directory`
   - it exercises the original problem statement directly: some of the most useful insights come from recognizing that these spans belong to one meaningful workspace operation rather than rendering them as disconnected low-level noise

Stage 12 implementation note:
1. V3 shell routing now uses clean path-based URLs rather than query-param entity selection.
2. Current route pattern:
   - list pages live at `/<entity-domain>` such as `/pipelines` and `/shells`
   - live entity detail pages live at `/<entity-domain>/<short-id>`
3. Current detail-page support:
   - `Pipelines` inventory rows navigate to per-pipeline pages at `/pipelines/<short-id>`
   - `Shells` inventory rows navigate to per-shell pages at `/shells/<short-id>`
4. Current detail-page content is intentionally thin:
   - route-specific recap in a compact header card
   - one body card for the entity's primary specialized view
5. Current route identity is UI-derived, not yet backend-native:
   - short IDs are currently deterministic route keys derived from trace plus client identity in the frontend
   - if direct linking/pagination needs hard guarantees later, promote short IDs into the API contract explicitly

Stage 13 implementation note:
1. V3 now exposes `Sessions` as a first-class left-nav domain backed by `GET /api/sessions`.
2. Current primitive-first adjustment:
   - `Pipelines` inventory now points directly at the `Session` primitive instead of using a vague `Scope` label
   - `Sessions` is now visible as its own inventory and detail route in the shell
3. Current session semantics:
   - sessions are derived from root clients
   - they sit between trace and client in the execution hierarchy
   - trace remains container context, not the primary identity of a session row

Stage 14 implementation note:
1. V3 now uses `Pipelines` as the UI/domain label instead of `CLI Runs`.
2. Working definition:
   - one client-submitted, connected DAGQL call chain aimed at one intended result
   - `dagger call` is always a pipeline
   - one top-level interactive command inside `dagger shell` is also a pipeline
   - ambiguous multi-command batch cases stay split per top-level command until V3 has an explicit multi-output model
3. Current pipeline detail page shape is intentionally minimal:
   - a thin recap card repeating the pipeline row's primary facts
   - a body card rendering the pipeline-scoped object DAG
4. Current object DAG source:
   - fetch `GET /api/pipelines/object-dag?traceID=<trace>&callID=<terminal-call-span>`
   - scope graph membership to the pipeline's own call chain plus output-reachable refs, not to V2 mutable-binding activity
   - build dependency edges directly from emitted output-state field `refs`
   - keep `field_ref` edge direction exactly as emitted for now; discuss direction changes separately
   - use the terminal output DAGQL ID only as a layout/highlight focus when one exists
   - if an authoritative pipeline call has an output DAGQL ID but no emitted output-state payload, keep that immutable node in the graph anyway and type it from the call return type; missing payload only blocks deeper ref expansion
5. Current output-shape handling:
   - object-valued outputs can highlight a concrete output snapshot node
   - non-object outputs still render the pipeline-scoped object DAG, but there is no concrete output node to focus yet
   - list cardinality, especially list-of-objects outputs, is not preserved cleanly enough in the current pipeline payload and should be added explicitly later if V3 wants first-class multi-item output rendering
6. Current implementation detail:
   - pipeline DAG nodes are immutable output states keyed by DAGQL ID, not V2 mutable bindings
   - this keeps the page anchored on authoritative pipeline-local facts even before any later collapse heuristics are added
7. Current module-load heuristic:
   - detect module setup from sibling prelude work owned by the same client but outside the terminal call subtree
   - use `load module: ...` spans plus top-level `Query.moduleSource`, `ModuleSource.asModule`, and `Module.serve` calls as evidence
   - do not infer module context from command-line parsing alone, because the client can also set module context through environment such as `DAGGER_MODULE`
   - if those low-level module calls occur inside the terminal call subtree itself, treat them as the pipeline, not as detached setup metadata
8. Design decision:
   - specialized V3 pipeline rendering should be built directly from authoritative DAG call facts plus immutable output-state payloads and refs
   - do not make V3 pipeline DAGs depend on V2's inferred mutation/object-creation layer as their source of truth
   - use V2 render only as a transitional/debug aid where it happens to help, not as the semantic basis of the pipeline view
9. Consequence for the pipeline page:
   - scope the graph by work inside the pipeline itself, especially the terminal call subtree and related attached evidence
   - treat CLI module loading (`load module: ...`, `Query.moduleSource`, `ModuleSource.asModule`, `Module.serve`, and similar setup calls) as first-class pipeline metadata when the actual sibling prelude spans show that setup occurred
   - keep those setup calls out of the main pipeline DAG unless the user explicitly ran those low-level functions as the pipeline itself
10. Cleanup decision:
   - the deprecated generic render-model route (`/api/v2/render` and `/api/v2/views/{view}/render`) is no longer part of the active V3 server surface
   - live V3 domains now speak through canonical `/api/pipelines`, `/api/sessions`, `/api/shells`, and `/api/pipelines/object-dag` routes instead of keeping V2 path names alive
11. Current pipeline inventory boundary:
   - when a same-client `parsing command line arguments` span exists, treat its completion as the boundary between CLI/module setup and the submitted pipeline
   - exclude same-client module-prelude calls before that boundary from pipeline call counts, terminal-call selection, and detail-page DAGs
   - if parsing fails and no post-parse user calls exist, do not materialize a fake successful pipeline row from prelude calls alone
12. Live validation checkpoint:
   - verified on a real `odag run -- dagger call -m github.com/dagger/dagger/modules/wolfi container`
   - expected pipeline DAG is now the narrow 2-node chain `Wolfi -> Container`, with module loading attached separately as metadata instead of leaked into the main DAG

Stage 15 implementation note:
1. First real Workspace Ops API slice is implemented at `GET /api/workspace-ops`.
2. Current derivation rule:
   - classify one workspace-op entity per explicit `Host.directory`, `Host.file`, `Directory.export`, or `File.export` call
   - decode the authoritative call payload and lift the host/export path directly from call arguments (`path`, with conservative fallback names)
   - keep the entity boundary at the explicit call itself; do not yet collapse multiple ops into inferred workspace roots
3. Current attachment rule:
   - derive pipelines separately first
   - attach a workspace op back to a pipeline only when the op's client and timing fit inside one proven pipeline window
   - prelude host-access before CLI parse remains visible as a workspace op, but stays detached from the pipeline instead of being misattributed
4. Current V3 shell shape:
   - `Workspace Ops` is now a live left-nav domain at `/workspace-ops`
   - the inventory is a simple list of real operations with status, path, started time, and optional pipeline/session links
   - each detail page stays thin: recap card plus one facts card
5. Current live validation checkpoint:
   - verified on a real `odag run -- dagger call -m github.com/dagger/dagger/modules/wolfi container`
   - current live workspace-op evidence is the module-load `Host.directory` read against the local workspace, correctly shown without a fake pipeline attachment

Stage 16 implementation note:
1. First real Git Remotes API slice is implemented at `GET /api/git-remotes`.
2. Current derivation rule:
   - classify one git-remote entity per normalized external repository or module ref
   - authoritative sources currently include:
     - `dagger.io/module.ref`
     - `load module: ...` spans
     - explicit `Query.git` style DAGQL calls decoded from authoritative call payloads
   - propagate remote identity forward through git-object receiver chains so later `GitRepository.*` or `GitRef.*` calls stay attached to the same normalized remote
3. Current identity rule:
   - normalize away revision suffixes such as `@<sha>` and transport suffixes such as `.git`
   - keep the full latest resolved ref separately as metadata (`latestResolvedRef`)
   - remote identity is therefore repository-or-module centric rather than revision centric
4. Current attachment rule:
   - derive pipelines separately first
   - attach a remote back to a pipeline when a same-client pipeline window fully contains the remote span, with same-client fallback kept for pre-parse module-load spans that semantically belong to the submitted pipeline
5. Current V3 shell shape:
   - `Git Remotes` is now a live left-nav domain at `/git-remotes`
   - the inventory is a simple list of normalized remotes with host, attached-pipeline count, session count, and last-seen time
   - each detail page stays thin: recap card plus one recent-pipelines table
6. Current live validation checkpoint:
   - verified on the real local ODAG dataset by serving this branch on a fresh port against a copied sqlite DB
   - the Wolfi module remote now appears as one normalized entity (`github.com/dagger/dagger/modules/wolfi`) with three attached `dagger call -m ... container` pipelines
   - explicit remote git calls also appear as normalized entities such as `github.com/opencontainers/runc` and `github.com/containernetworking/plugins`

Stage 17 implementation note:
1. First real Services API slice is implemented at `GET /api/services`.
2. Current derivation rule:
   - classify one service entity per authoritative immutable `Service` output-state object in trace scope
   - do not depend on the old generic mutable-render layer for service discovery
   - layer related DAGQL activity on top by matching calls that:
     - produce the service
     - receive the service
     - consume the service via input refs
3. Current service identity rule:
   - primary identity is `traceID + dagqlID`
   - display name prefers `CustomHostname`, then container image ref, then a short service ID fallback
   - service kind is derived conservatively from authoritative fields (`Container`, `TunnelUpstream`, `TunnelPorts`, `HostSockets`)
4. Current status rule:
   - use the latest receiver-side lifecycle call when available (`Service.up`, `Service.start`, etc.)
   - open/unset lifecycle calls render as `running`
   - failing lifecycle calls render as `failed`
   - otherwise the service stays `ready` or `created`
5. Current V3 shell shape:
   - `Services` is now a live left-nav domain at `/services`
   - the inventory is a simple list of services with status, kind, creator, session, and last activity
   - each detail page stays thin: recap card, definition card, and an activity table
6. Current live validation checkpoint:
   - verified on the real local ODAG dataset by serving this branch on a fresh port against a copied sqlite DB
   - the current data now renders two real nginx-backed services
   - one service shows a failed `Service.up`, and another shows an open `Service.up` as `running`

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
   - revision-history navigation remains responsive with progressive rendering
