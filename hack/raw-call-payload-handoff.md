# Raw call payload handoff

## Goal

Finish the breaking call-payload transport change:

- OTel log body: deterministic raw `callpbv1.Call` protobuf bytes.
- Marker attribute: `dagger.io/dag.call.payload=true` (boolean).
- Instrumentation scope: `dagger.io/dag.call.payload`.
- No digest attribute and no embedded `Call.digest` on the wire.
- Routers and consumers recompute the canonical recipe digest from the decoded
  call content before indexing it.

The explicit boolean attribute remains the primary record discriminator. The
scope is a second reservation/validation signal. A record carrying the marker,
or using the reserved scope, must be consumed as call data even when malformed;
it must never fall through and render protobuf bytes as log text.

## Completed commits

The current workspace has these staged commits:

1. `8ee502d dagql/call: canonicalize call payloads`
   - Adds `call.CanonicalDigest(*callpbv1.Call)`.
   - Makes ordinary `call.ID` construction use the same protobuf-field hashing
     implementation.
   - Adds `call.DecodeCallPayload([]byte)`, which discards unknown protobuf
     fields, rejects embedded self-digests, recomputes the recipe digest, and
     sets it only on the returned local call.
   - Includes golden digest, all-literal, tamper, annotation, unknown-field, and
     malformed-input tests.

2. `a6a768d core,telemetryattrs: emit raw call payloads`
   - Producer verifies the map key and embedded digest against
     `call.CanonicalDigest`.
   - Shallow-copies the call, clears `Digest`, and deterministically marshals it
     into a `log.KindBytes` body.
   - Emits `dagger.io/dag.call.payload=true` under scope
     `dagger.io/dag.call.payload`.
   - Removes the digest attribute constant from `telemetryattrs`.

3. `a913619 dagui: consume raw call payloads`
   - Validates marker, scope, and byte body.
   - Uses `call.DecodeCallPayload` and stores decoded calls directly in one
     `DB.Calls` map.
   - Drops malformed/tampered reserved records instead of rendering them.
   - Removes legacy span payload ingestion, `SpanSnapshot.CallPayload`, the
     encoded payload map, and the call protobuf base64 helpers.
   - Updates DagUI/IDTUI/restore fixtures and tests.

No changes from any FAILED worker were pulled.

## Current state

The producer and DagUI consumer are implemented, but engine routing is still on
the old digest-attribute schema, so the full tree intentionally does not compile
yet.

Passing now:

```text
go test ./dagql/call ./dagql/dagui -count=1
```

Expected current engine failure:

```text
engine/server/telemetry.go: undefined: telemetryattrs.DagCallPayloadDigestAttr
engine/server/session_test.go: undefined: telemetryattrs.DagCallPayloadDigestAttr
```

Known unrelated full-IDTUI failures:

```text
TestUserPromptLeadingGutterShaded/focused
TestFocusedAssistantMessageSinglePrompt
```

## Remaining engine work

### 1. Update the payload-only fast batch filter

File: `engine/telemetry/callpayloadbatch.go`

`isCallPayloadRecord` should return true only for a valid fast-path shape:

- scope is `telemetryattrs.CallPayloadInstrumentationScope`;
- body kind is `log.KindBytes`;
- attribute `telemetryattrs.DagCallPayloadAttr` exists, is boolean, and is true.

Malformed reserved records can take the normal batch path, where the session
exporter validates and drops them.

### 2. Replace digest-attribute routing

File: `engine/server/telemetry.go`

Replace `dagCallPayloadDigest` with a classifier like:

```go
func classifyCallPayloadRecord(rec sdklog.Record) (digest string, payload bool, err error)
```

Required behavior:

- The marker key is the primary discriminator.
- The reserved instrumentation scope is also a discriminator.
- Neither marker nor scope: ordinary log (`payload=false`).
- Marker false/wrong type, missing marker under reserved scope, wrong scope,
  wrong body kind, embedded digest, or malformed body: reserved but invalid
  (`payload=true`, error).
- Valid body: call `call.DecodeCallPayload(rec.Body().AsBytes())` and return its
  computed digest.

In `sessionLogExporter.Export`, classify before routing. On an invalid marked or
scope-reserved record, warn and `continue`; do not return and discard the rest of
a mixed batch. For valid records, feed the computed digest into the existing
atomic `callPayloadMissingTargets` logic. Preserve the marker/body, strip only
the internal origin attribute, and route only to missing targets.

Tests should cover:

- true marker + exact scope + bytes body;
- false/wrong-type marker;
- marker with wrong scope;
- reserved scope without marker;
- wrong body kind;
- embedded self-digest rejection;
- tampered body receiving a different computed address;
- one invalid record mixed with valid records (valid records still deliver);
- partial parent/child target delivery and exact persisted counts;
- byte body surviving client DB -> binary live OTLP reconstruction.

### 3. Enable OTel `KindBytes` persistence

The current pin is:

```text
github.com/dagger/otel-go v1.43.1-0.20260515012101-af7cd0684887
```

Upstream commit `752a39ce1610d800351accfdf93f5e92f255d0ac` adds the missing
`log.KindBytes -> AnyValue_BytesValue` conversion in `LogValueToPB`.

Preferred end state is to bump to that commit and update `go.mod`/`go.sum`.
A local fallback, if the dependency bump remains blocked, is to special-case
`log.KindBytes` in `engine/server/telemetry.go:logRecordRow` before delegating to
`telemetry.LogValueToPB`. The decoder already supports OTLP bytes.

## `go get` / Contributor schema failure

Three isolated workers failed immediately after invoking:

```text
go get github.com/dagger/otel-go@752a39ce1610d800351accfdf93f5e92f255d0ac
```

The error is not emitted by the Go toolchain:

```text
bound object type "Contributor" is not an object in the workspace schema
```

The repository contains no `Contributor` object type. This appears to be a
Dagger agent/workspace-schema recomposition bug: mutating the root `go.mod`
causes the live agent composition to reload, then replay/snapshot encounters a
previously bound `Contributor` object from the agent's GitHub/contribution
module that is absent from the recomposed workspace schema. The command may
have already changed files in the failed worker, but none of those edits were
pulled.

Suggested troubleshooting/workarounds:

1. Apply and commit the dependency bump outside a live agent session, then start
   a fresh session against the already-updated workspace.
2. Reproduce with a minimal worker whose only action is the exact `go get`, and
   inspect the workspace-schema/module reload trace rather than Go command
   output.
3. Temporarily use the local four-line `KindBytes` conversion described above,
   avoiding a live `go.mod` mutation.

## Final validation

Run packages separately where needed; some server tests use shared DB-handle
accounting and have shown interference in broad concurrent package runs.

```text
go test ./dagql/call -count=1
go test ./dagql/dagui -count=1
go test ./core -count=1
go test ./engine/telemetry -count=1
go test ./engine/server -count=1
go test ./engine/client -count=1
go test -race ./engine/telemetry -run TestCallPayloadBatchProcessor -count=1
go test -race ./engine/server -run 'TestCallPayloadClaims|TestSessionLogExporterRoutesCallPayload' -count=1
```

Then run engine integration:

```text
TestClient/TestMultiSameTrace
TestClient/TestClose
```

Also verify an actual raw call record after the full round trip has:

- scope `dagger.io/dag.call.payload`;
- attribute `dagger.io/dag.call.payload=true`;
- `bytes_value` body;
- no `dagger.io/dag.call.payload.digest` attribute;
- decoded wire `Call.digest == ""`;
- locally decoded call digest equal to `call.CanonicalDigest`.
