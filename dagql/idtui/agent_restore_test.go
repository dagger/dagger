package idtui

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/protobuf/proto"
)

// The seam `dagger agent --trace` reads the restore plan through
// (hack/designs/resume-from-trace.md §5.1, "Reading the DB back"): the
// projection and the anchor rebuild are reads OF the frontend's DB, which the
// frontend owns single-threaded, so the CLI cannot reach for the DB directly.
//
// What is asserted here is that the seam really goes through the frontend's
// own DB — the one the import lands in — and that both halves report what the
// CLI has to act on: a plan naming the imported agents, an encoded handle for
// an anchor whose payload arrived, and a loud failure for one whose did not.

// The conversation the imported chief committed last, as call payloads: a
// two-frame chain with a string argument, which is the shape an agent's
// SNAPSHOT chain has (§13.5 measured 71 of 177 CI-trace payloads failing to
// rebuild on absent ARGUMENT frames — a snapshot chain has almost none).
func cannedSnapshotChain() (root *callpbv1.Call, frames []*callpbv1.Call) {
	llm := &callpbv1.Call{
		Field: "llm",
		Type:  &callpbv1.Type{NamedType: "LLM"},
	}
	setDigest := func(frame *callpbv1.Call) {
		dgst, err := call.CanonicalDigest(frame)
		if err != nil {
			panic(err)
		}
		frame.Digest = dgst.String()
	}
	setDigest(llm)
	withPrompt := &callpbv1.Call{
		Field:          "withPrompt",
		Type:           &callpbv1.Type{NamedType: "LLM"},
		ReceiverDigest: llm.Digest,
		Args: []*callpbv1.Argument{{
			Name:  "prompt",
			Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: cannedAnchorPrompt}},
		}},
	}
	setDigest(withPrompt)
	return withPrompt, []*callpbv1.Call{withPrompt, llm}
}

const (
	cannedAnchorPrompt = "find the leak"
	// cannedMissingDigest names a conversation whose payload never reached
	// this client — §9's first row, and the one §5.3.3 fails a restore on.
	cannedMissingDigest = "xxh3:neverpublished"
)

var cannedAnchorDigest = func() string {
	root, _ := cannedSnapshotChain()
	return root.Digest
}()

// cannedRestoreLogs is the rest of the capture's log channel: the resume
// anchor each agent published, and the raw call payloads that make the chief's
// anchor rebuildable.
func cannedRestoreLogs(traceID byte, withWorker bool) *collogspb.ExportLogsServiceRequest {
	record := func(span byte, attrs ...*commonpb.KeyValue) *logspb.LogRecord {
		return &logspb.LogRecord{
			TimeUnixNano: uint64(time.Unix(foreignTurnEnd, 0).UnixNano()),
			TraceId:      cannedTraceID(traceID),
			SpanId:       cannedSpanID(span),
			Body:         &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: ""}},
			Attributes:   attrs,
		}
	}
	anchor := func(span byte, digest string) *logspb.LogRecord {
		return record(span, cannedStringAttr(telemetryattrs.AgentSnapshotDigestAttr, digest))
	}

	records := []*logspb.LogRecord{anchor(foreignLoopSpanID, cannedAnchorDigest)}
	if withWorker {
		// The worker's anchor names a conversation whose payload never
		// arrived, so the two halves of §5.3.3 are both reachable from one
		// capture.
		records = append(records, anchor(foreignWorkerSpanID, cannedMissingDigest))
	}

	_, frames := cannedSnapshotChain()
	payloadRecords := make([]*logspb.LogRecord, 0, len(frames))
	for _, frame := range frames {
		payloadCall := proto.Clone(frame).(*callpbv1.Call)
		payloadCall.Digest = ""
		payload, err := (proto.MarshalOptions{Deterministic: true}).Marshal(payloadCall)
		if err != nil {
			panic(err)
		}
		payloadRecords = append(payloadRecords, &logspb.LogRecord{
			TimeUnixNano: uint64(time.Unix(foreignTurnEnd, 0).UnixNano()),
			TraceId:      cannedTraceID(traceID),
			SpanId:       cannedSpanID(foreignTurnSpanID),
			Body:         &commonpb.AnyValue{Value: &commonpb.AnyValue_BytesValue{BytesValue: payload}},
			Attributes:   []*commonpb.KeyValue{cannedBoolAttr(telemetryattrs.DagCallPayloadAttr, true)},
		})
	}

	return &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{
				{LogRecords: records},
				{
					Scope:      &commonpb.InstrumentationScope{Name: telemetryattrs.CallPayloadInstrumentationScope},
					LogRecords: payloadRecords,
				},
			},
		}},
	}
}

// restorableFrontend is what the CLI holds when the restore runs: a frontend
// whose DB has just had a RESTORABLE source trace imported into it — the
// slice-4 capture plus the anchors and payloads a plan is made of.
//
// A report-mode frontend, because that is the mode whose dispatch runs
// inline: a headless TUI only QUEUES dispatched work until something steps
// it, so a blocking read would deadlock here. Production blocks on the live
// event loop instead, exactly as `dagger trace`'s SurfacedFailedCheckSpans
// already does — Frontend.Run has the loop running before it calls back.
func restorableFrontend(t *testing.T, withWorker bool) *frontendPretty {
	t.Helper()
	ctx := context.Background()
	db := dagui.NewDB()
	// The live session's root, and nothing else: the plan is projected BEFORE
	// anything is re-hydrated, so the resuming session has published no agent
	// of its own yet. (liveSessionTrace models the session AFTER a restore,
	// where the chief's identity span makes it a live-session agent the plan
	// must leave out — which is the rule slice 3 landed, not this seam's
	// business.)
	require.NoError(t, db.ExportSpans(ctx, telemetry.SpansFromPB(
		cannedTrace(liveTraceIDByte, cannedSpan{
			id: liveRootSpanID, name: "dagger agent --trace", start: 1000,
		}).GetResourceSpans())))

	imp := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   db,
		Logs:    db.LogExporter(),
		Metrics: db.MetricExporter(),
	})
	require.NoError(t, imp.ImportSpans(ctx, foreignSessionTrace(withWorker)))
	states := []cannedStateRecord{{span: foreignLoopSpanID, state: "IDLE"}}
	if withWorker {
		states = append(states, cannedStateRecord{span: foreignWorkerSpanID, state: "IDLE"})
	}
	require.NoError(t, imp.ImportLogs(ctx, cannedAgentStateLogs(foreignTraceIDByte, states...)))
	require.NoError(t, imp.ImportLogs(ctx, cannedRestoreLogs(foreignTraceIDByte, withWorker)))
	require.NoError(t, imp.Seal(ctx))

	return NewASCIIReporterWithDB(io.Discard, db)
}

// TestAgentRestorePlanReadsTheFrontendsDB: the plan the CLI acts on is the
// projection of the frontend's OWN DB — the one the import landed in — and
// the live session's agents are left out of it, since re-hydrating an agent
// this session already holds is what Agent.rehydrate refuses.
func TestAgentRestorePlanReadsTheFrontendsDB(t *testing.T) {
	fe := restorableFrontend(t, true)

	var restorer AgentRestorer = fe
	plan := restorer.AgentRestorePlan()

	byID := map[string]dagui.AgentRestore{}
	for _, entry := range plan {
		byID[entry.ID] = entry
	}
	require.Len(t, plan, 2, "the plan must name both of the source trace's agents: %+v", plan)

	chief := byID[importChiefAgentID]
	require.Equal(t, "interactive", chief.Name)
	require.Equal(t, "IDLE", chief.State)
	require.Equal(t, cannedAnchorDigest, chief.SnapshotDigest,
		"the plan must anchor on the digest the trace published")
	require.True(t, chief.Restorable(), "chief unrestorable: %v", chief.Err)
	require.Empty(t, chief.ParentAgentID, "the chief has no agent above it")

	scout := byID[importScoutAgentID]
	require.Equal(t, importChiefAgentID, scout.ParentAgentID,
		"the worker's loop is nested under its chief's, which is what focus reads")
}

// TestEncodedIDForCallDigestRebuildsAnAnchor is the other half of the seam:
// an anchor becomes a conversation to load and re-hydrate from, rebuilt from
// the call payloads that rode the LOG channel — no span carries this digest,
// which is exactly the case §3.2's failure mode 2 describes and the one a
// resume anchor lands in.
func TestEncodedIDForCallDigestRebuildsAnAnchor(t *testing.T) {
	fe := restorableFrontend(t, false)

	var restorer AgentRestorer = fe
	encoded, err := restorer.EncodedIDForCallDigest(cannedAnchorDigest)
	require.NoError(t, err)
	require.NotEmpty(t, encoded)

	var id call.ID
	require.NoError(t, id.Decode(encoded))
	require.Contains(t, id.Display(), cannedAnchorPrompt,
		"the rebuilt handle must be the committed conversation, not a truncated chain")
}

// TestEncodedIDForCallDigestReportsAGap: a frame whose payload never reached
// this client makes the anchor unrebuildable, and the caller must hear about
// it — §5.3.3 turns exactly this into a failed restore naming the agent,
// rather than a handle that looks fine and re-hydrates an amnesiac twin.
func TestEncodedIDForCallDigestReportsAGap(t *testing.T) {
	fe := restorableFrontend(t, true)

	var restorer AgentRestorer = fe
	_, err := restorer.EncodedIDForCallDigest(cannedMissingDigest)
	require.ErrorContains(t, err, cannedMissingDigest)
	require.ErrorContains(t, err, "never reached this client")
}
