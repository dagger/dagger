package idtui

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Importing a foreign trace beside the live one
// (hack/designs/resume-from-trace.md §5.1). `dagger agent --trace` streams a
// past session's whole trace into the LIVE frontend's own exporters, so one DB
// holds both sessions: the old run's TUI, plus a live prompt. Everything below
// is driven by a canned OTLP capture — no Cloud, no engine — through the same
// importer slice 5's SSE client feeds.

const (
	liveTraceIDByte    byte = 1
	foreignTraceIDByte byte = 2

	liveRootSpanID byte = 1
	liveLoopSpanID byte = 2
	liveTurnSpanID byte = 3

	foreignRootSpanID   byte = 11
	foreignLoopSpanID   byte = 12
	foreignTurnSpanID   byte = 13
	foreignWorkerSpanID byte = 14

	importChiefAgentID = "agent-chief"
	importScoutAgentID = "agent-scout"
)

// The source session's timeline. It CRASHED: its root span, its chief's loop
// span and its worker's loop span never ended, which is the case §5.1.2 is
// about — the DB only cancels running spans when its OWN root ends, so
// without the seal these render as live work forever.
const (
	foreignRootStart   int64 = 100
	foreignLoopStart   int64 = 110
	foreignTurnStart   int64 = 120
	foreignTurnEnd     int64 = 130
	foreignWorkerStart int64 = 140
	// foreignSealedAt is the newest timestamp the capture carries, which is
	// what the unfinished spans are sealed to when the source root never
	// ended.
	foreignSealedAt int64 = foreignWorkerStart
)

// runningEndNano is how a still-running span's end time reaches a consumer:
// the zero time.Time, i.e. an end BEFORE the start (see
// otel.FilterLiveSpansExporter and dagui.Span.IsRunning). It is what the live
// span processor exports at span start, and what a crashed session's spans
// are frozen at forever.
var runningEndNano = uint64(time.Time{}.UnixNano()) //nolint:gosec

type cannedSpan struct {
	id     byte
	parent byte
	name   string
	start  int64
	end    int64 // zero: still running when the capture was taken
	attrs  []*commonpb.KeyValue
}

func (s cannedSpan) pb(traceID byte) *tracepb.Span {
	span := &tracepb.Span{
		TraceId:           cannedTraceID(traceID),
		SpanId:            cannedSpanID(s.id),
		Name:              s.name,
		StartTimeUnixNano: uint64(time.Unix(s.start, 0).UnixNano()), //nolint:gosec
		EndTimeUnixNano:   runningEndNano,
		Attributes:        s.attrs,
		Status:            &tracepb.Status{},
	}
	if s.parent != 0 {
		span.ParentSpanId = cannedSpanID(s.parent)
	}
	if s.end != 0 {
		span.EndTimeUnixNano = uint64(time.Unix(s.end, 0).UnixNano()) //nolint:gosec
	}
	return span
}

func cannedSpanID(id byte) []byte {
	return []byte{id, 0, 0, 0, 0, 0, 0, 0}
}

func cannedTraceID(id byte) []byte {
	return []byte{id, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
}

func cannedTrace(traceID byte, spans ...cannedSpan) *coltracepb.ExportTraceServiceRequest {
	pbSpans := make([]*tracepb.Span, 0, len(spans))
	for _, span := range spans {
		pbSpans = append(pbSpans, span.pb(traceID))
	}
	return &coltracepb.ExportTraceServiceRequest{
		ResourceSpans: []*tracepb.ResourceSpans{{
			Resource:   &resourcepb.Resource{},
			ScopeSpans: []*tracepb.ScopeSpans{{Spans: pbSpans}},
		}},
	}
}

func cannedStringAttr(key, val string) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: val}},
	}
}

func cannedBoolAttr(key string, val bool) *commonpb.KeyValue {
	return &commonpb.KeyValue{
		Key:   key,
		Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: val}},
	}
}

// cannedAgentAttrs is the identity an agent's loop span carries, stamped at
// span start.
func cannedAgentAttrs(id, name, digest string) []*commonpb.KeyValue {
	return []*commonpb.KeyValue{
		cannedBoolAttr(telemetryattrs.AgentAttr, true),
		cannedStringAttr(telemetryattrs.AgentIDAttr, id),
		cannedStringAttr(telemetryattrs.AgentNameAttr, name),
		cannedStringAttr(telemetryattrs.AgentCallDigestAttr, digest),
	}
}

func cannedMessageAttrs(role string) []*commonpb.KeyValue {
	return []*commonpb.KeyValue{cannedStringAttr(telemetry.LLMRoleAttr, role)}
}

// cannedStateRecord is one agent-state record of the capture.
type cannedStateRecord struct {
	span  byte
	state string
	// emptyBody models the record arriving with no body at all. The engine
	// emits an explicit empty-string body (core/agent_telemetry.go), but these
	// records are attribute-only and design §12 flags "does an empty body
	// survive the Cloud round trip" as unverified — so the import has to
	// tolerate its absence rather than take the CLI down mid-restore.
	emptyBody bool
}

// cannedAgentStateLogs is the state-record channel of the capture:
// attribute-only log records attributed to a loop span, exactly as the engine
// emits them. The request carries no Resource, which is legal OTLP (the field
// is optional) and what a payload with no resource info decodes to.
func cannedAgentStateLogs(traceID byte, records ...cannedStateRecord) *collogspb.ExportLogsServiceRequest {
	pbRecords := make([]*logspb.LogRecord, 0, len(records))
	for _, record := range records {
		pbRecord := &logspb.LogRecord{
			TimeUnixNano: uint64(time.Unix(foreignTurnEnd, 0).UnixNano()), //nolint:gosec
			TraceId:      cannedTraceID(traceID),
			SpanId:       cannedSpanID(record.span),
			Attributes: []*commonpb.KeyValue{
				cannedStringAttr(telemetryattrs.AgentStateAttr, record.state),
				cannedStringAttr(telemetryattrs.AgentWaitingOnAttr, ""),
				cannedStringAttr(telemetryattrs.AgentStopReasonAttr, ""),
			},
		}
		if !record.emptyBody {
			pbRecord.Body = &commonpb.AnyValue{
				Value: &commonpb.AnyValue_StringValue{StringValue: ""},
			}
		}
		pbRecords = append(pbRecords, pbRecord)
	}
	return &collogspb.ExportLogsServiceRequest{
		ResourceLogs: []*logspb.ResourceLogs{{
			ScopeLogs: []*logspb.ScopeLogs{{LogRecords: pbRecords}},
		}},
	}
}

// liveSessionTrace is the resuming CLI's OWN trace: the root it publishes
// into, the agent it re-hydrated (which republishes its identity into the new
// trace, §4.5), and one turn spoken since.
func liveSessionTrace() *coltracepb.ExportTraceServiceRequest {
	return cannedTrace(liveTraceIDByte,
		cannedSpan{id: liveRootSpanID, name: "dagger agent --trace", start: 1000},
		cannedSpan{
			id: liveLoopSpanID, parent: liveRootSpanID, name: "agent: interactive",
			start: 1010,
			attrs: cannedAgentAttrs(importChiefAgentID, "interactive", "sha256:chief"),
		},
		cannedSpan{
			id: liveTurnSpanID, parent: liveLoopSpanID, name: "live turn",
			start: 1020, end: 1030,
			attrs: cannedMessageAttrs("user"),
		},
	)
}

// foreignSessionTrace is the canned capture of the session being resumed. With
// a worker it holds a second agent that only ever lived in THAT session — the
// one whose loop is really dead.
func foreignSessionTrace(withWorker bool) *coltracepb.ExportTraceServiceRequest {
	spans := []cannedSpan{
		{id: foreignRootSpanID, name: "dagger agent", start: foreignRootStart},
		{
			id: foreignLoopSpanID, parent: foreignRootSpanID, name: "agent: interactive",
			start: foreignLoopStart,
			attrs: cannedAgentAttrs(importChiefAgentID, "interactive", "sha256:chief"),
		},
		{
			id: foreignTurnSpanID, parent: foreignLoopSpanID, name: "imported turn",
			start: foreignTurnStart, end: foreignTurnEnd,
			attrs: cannedMessageAttrs("assistant"),
		},
	}
	if withWorker {
		spans = append(spans, cannedSpan{
			id: foreignWorkerSpanID, parent: foreignLoopSpanID, name: "agent: scout",
			start: foreignWorkerStart,
			attrs: cannedAgentAttrs(importScoutAgentID, "scout", "sha256:scout"),
		})
	}
	return cannedTrace(foreignTraceIDByte, spans...)
}

// importedTraceDB is what a resuming client holds once the fetch has run: its
// own live trace, exported the ordinary way, plus the source trace folded in
// through the importer and sealed at stream end.
func importedTraceDB(t *testing.T, withWorker bool) *dagui.DB {
	t.Helper()
	ctx := context.Background()
	db := dagui.NewDB()

	// The live session is already publishing by the time the fetch runs: its
	// root is the DB's root and its primary span.
	require.NoError(t, db.ExportSpans(ctx, telemetry.SpansFromPB(liveSessionTrace().GetResourceSpans())))

	imp := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   db,
		Logs:    db.LogExporter(),
		Metrics: db.MetricExporter(),
	})
	require.NoError(t, imp.ImportSpans(ctx, foreignSessionTrace(withWorker)))
	states := []cannedStateRecord{{span: foreignLoopSpanID, state: "RUNNING"}}
	if withWorker {
		states = append(states, cannedStateRecord{
			span: foreignWorkerSpanID, state: "RUNNING", emptyBody: true,
		})
	}
	require.NoError(t, imp.ImportLogs(ctx, cannedAgentStateLogs(foreignTraceIDByte, states...)))
	require.NoError(t, imp.Seal(ctx))
	return db
}

func importedAgent(t *testing.T, db *dagui.DB, name string) *dagui.AgentNode {
	t.Helper()
	for _, agent := range db.Agents() {
		if agent.Name == name {
			return agent
		}
	}
	t.Fatalf("no agent named %q on the roster", name)
	return nil
}

// TestImportLeavesThePrimarySpanAlone is §5.1.1's first half. The reference
// trace client calls Frontend.SetPrimary; resume must NOT, because the live
// CLI's root is already the primary span and repointing it would zoom the
// session to a run that is over — and would take the restore plan's
// live-vs-imported discriminator (§13.3) with it.
func TestImportLeavesThePrimarySpanAlone(t *testing.T) {
	db := importedTraceDB(t, true)

	live := prettyTestSpanID(liveRootSpanID)
	require.Equal(t, live, db.PrimarySpan, "the import repointed the primary span")
	require.NotNil(t, db.RootSpan)
	require.Equal(t, live, db.RootSpan.ID, "the import repointed the trace root")
	require.True(t, db.Spans.Map[live].IsRunning(),
		"sealing the source trace must not seal the live one")
}

// TestImportSealsTheForeignTracesUnfinishedSpans is §5.1.2. The DB cancels
// still-running spans only when ITS OWN root ends, and the source root is not
// it — so a crashed session's never-ended spans would spin forever and report
// a dead agent as live.
func TestImportSealsTheForeignTracesUnfinishedSpans(t *testing.T) {
	db := importedTraceDB(t, true)

	sealedAt := time.Unix(foreignSealedAt, 0)
	for _, id := range []byte{foreignRootSpanID, foreignLoopSpanID, foreignWorkerSpanID} {
		span := db.Spans.Map[prettyTestSpanID(id)]
		require.NotNil(t, span, "span %d missing from the DB", id)
		require.False(t, span.IsRunning(), "%q still renders as live work", span.Name)
		require.True(t, span.Canceled, "%q was not sealed Canceled", span.Name)
		require.True(t, span.LeftRunning, "%q was not sealed LeftRunning", span.Name)
		require.True(t, span.EndTime.Equal(sealedAt),
			"%q sealed at %v, want the newest timestamp the capture carries (%v)",
			span.Name, span.EndTime, sealedAt)
	}

	// A span that DID end keeps its own end time: sealing is for the ones the
	// crash left open, not a rewrite of the trace's timing.
	turn := db.Spans.Map[prettyTestSpanID(foreignTurnSpanID)]
	require.NotNil(t, turn)
	require.True(t, turn.EndTime.Equal(time.Unix(foreignTurnEnd, 0)),
		"a finished span's end time was overwritten: %v", turn.EndTime)

	// The pathology stated in terms of the roster: the source trace's last
	// word on the worker is RUNNING, and it is not.
	scout := importedAgent(t, db, "scout")
	require.Equal(t, "RUNNING", scout.State, "fixture: the capture's last state record")
	require.False(t, scout.Live(), "an agent whose session died must not report as live")

	// ...while the agent this session re-hydrated is live, because its NEW
	// loop span is running. Sealing is scoped to the import.
	require.True(t, importedAgent(t, db, "interactive").Live(),
		"the restored agent's live loop span was sealed too")
}

// TestImportedRootRendersPassthrough is §5.1.1's second half. The imported
// root is simply a second parentless span; passthrough makes every walk render
// its children in its place, rather than a stale `dagger agent` row wrapping
// the whole of the old session.
func TestImportedRootRendersPassthrough(t *testing.T) {
	db := importedTraceDB(t, true)

	root := db.Spans.Map[prettyTestSpanID(foreignRootSpanID)]
	require.NotNil(t, root)
	require.True(t, root.Passthrough, "the imported root was not marked passthrough")
	// Encapsulate and Boundary would also hide the row -- by CONTAINING what
	// is beneath it, which is exactly what the imported conversation must not
	// be (§5.1.1, §5.1.3).
	require.False(t, root.Encapsulate, "the imported root must not contain its subtree")
	require.False(t, root.Boundary, "the imported root must not contain its subtree")

	view := db.RowsView(dagui.FrontendOpts{})
	require.NotContains(t, view.BySpan, prettyTestSpanID(foreignRootSpanID),
		"the imported root rendered as a row of its own")
	require.Contains(t, view.BySpan, prettyTestSpanID(foreignLoopSpanID),
		"the imported root's children must render in its place")
}

// TestImportMergesTheAgentsLoopSpans is what importing into the LIVE DB buys
// (§5.1, §4.5): a re-hydrated agent keeps its instance ID, so its old life and
// its new one fold into one roster entry rather than two agents with one name.
func TestImportMergesTheAgentsLoopSpans(t *testing.T) {
	db := importedTraceDB(t, false)

	agents := db.Agents()
	require.Len(t, agents, 1, "the restored agent's two lives split the roster")
	chief := agents[0]
	require.Equal(t, importChiefAgentID, chief.ID)
	require.Len(t, chief.Spans, 2, "expected the imported loop span and the live one")
	require.Equal(t, prettyTestSpanID(foreignLoopSpanID), chief.Spans[0].ID)
	require.Equal(t, prettyTestSpanID(liveLoopSpanID), chief.Spans[1].ID)
	require.Equal(t, "sha256:chief", chief.CallDigest, "the merged entry lost its handle")
}

// TestFocusedAgentTranscriptIncludesTheImportedTurns is §5.1.3, the case the
// whole-trace surfacing fix exists for: with ONE agent on the roster the strip
// is hidden and promotion falls back to the whole trace, which resolved its
// nil root to the LIVE root — so every message span hanging off the imported
// root was filed as contained and dropped, and the restored session opened
// with an empty scrollback.
func TestFocusedAgentTranscriptIncludesTheImportedTurns(t *testing.T) {
	db := importedTraceDB(t, false)
	require.Len(t, db.Agents(), 1,
		"fixture: one agent, so the strip is hidden and the transcript is whole-trace")

	handler := &focusShellHandler{target: importChiefAgentID}
	fe := focusTestFrontend(t, db, handler)
	fe.recalculateViewLocked()

	require.Equal(t, map[string]bool{"imported turn": true, "live turn": true},
		revealedNames(t, fe),
		"the promoted transcript must span both of the agent's lives")
}
