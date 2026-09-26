package idtui

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/agentcontrol"
	enginetel "github.com/dagger/dagger/engine/telemetry"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	otellog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
)

// Importing a foreign trace beside the live one
// (hack/designs/resume-from-trace.md §5.1). `dagger agent -r` streams a
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
var runningEndNano = uint64(time.Time{}.UnixNano())

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
		StartTimeUnixNano: uint64(time.Unix(s.start, 0).UnixNano()),
		EndTimeUnixNano:   runningEndNano,
		Attributes:        s.attrs,
		Status:            &tracepb.Status{},
	}
	if s.parent != 0 {
		span.ParentSpanId = cannedSpanID(s.parent)
	}
	if s.end != 0 {
		span.EndTimeUnixNano = uint64(time.Unix(s.end, 0).UnixNano())
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

// cannedAgent is one agent's control revision in a capture.
type cannedAgent struct {
	id, name, callDigest, parent, state, digest string
	// emptyBody models the record arriving with no body at all. The engine
	// emits an explicit empty-string body, but these records are
	// attribute-only and design §12 flags "does an empty body survive the
	// Cloud round trip" as unverified — so the import has to tolerate its
	// absence rather than take the CLI down mid-restore.
	emptyBody bool
}

// cannedAgentControlLogs is the agent control channel of the source capture:
// attribute-only log records, exactly as the engine emits them. The request
// carries no Resource, which is legal OTLP (the field is optional) and what a
// payload with no resource info decodes to.
func cannedAgentControlLogs(agents ...cannedAgent) *collogspb.ExportLogsServiceRequest {
	pbRecords := make([]*logspb.LogRecord, 0, len(agents))
	for _, agent := range agents {
		control := agentcontrol.Agent{
			Key: agentcontrol.Key{
				Namespace: agentcontrol.Namespace{
					Session:     "session",
					Trace:       trace.TraceID{foreignTraceIDByte}.String(),
					Incarnation: "incarnation",
				},
				Handle: agent.id,
			},
			Revision: 1, Name: agent.name, CallDigest: agent.callDigest, Parent: agent.parent,
			State: agent.state, Digest: agent.digest, Activity: time.Unix(foreignTurnEnd, 0),
		}
		rec := control.Record()
		var attrs []*commonpb.KeyValue
		rec.WalkAttributes(func(kv otellog.KeyValue) bool {
			switch kv.Value.Kind() {
			case otellog.KindString:
				attrs = append(attrs, cannedStringAttr(kv.Key, kv.Value.AsString()))
			case otellog.KindInt64:
				attrs = append(attrs, &commonpb.KeyValue{
					Key:   kv.Key,
					Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: kv.Value.AsInt64()}},
				})
			default:
				panic("unexpected control attribute kind " + kv.Value.Kind().String())
			}
			return true
		})
		pbRecord := &logspb.LogRecord{
			TimeUnixNano: uint64(time.Unix(foreignTurnEnd, 0).UnixNano()),
			TraceId:      cannedTraceID(foreignTraceIDByte),
			Attributes:   attrs,
		}
		if !agent.emptyBody {
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

// foreignAgentControlLogs is the source session's agent control channel: the
// chief anchored on the conversation whose payloads cannedRestoreLogs carries,
// and with a worker, a scout anchored on one whose payload never arrived.
func foreignAgentControlLogs(withWorker bool, state string) *collogspb.ExportLogsServiceRequest {
	agents := []cannedAgent{{
		id: importChiefAgentID, name: "interactive", callDigest: "sha256:chief",
		state: state, digest: cannedAnchorDigest,
	}}
	if withWorker {
		agents = append(agents, cannedAgent{
			id: importScoutAgentID, name: "scout", callDigest: "sha256:scout",
			parent: importChiefAgentID, state: state, digest: cannedMissingDigest,
			emptyBody: true,
		})
	}
	return cannedAgentControlLogs(agents...)
}

// liveSessionTrace is the resuming CLI's OWN trace: the root it publishes
// into, the agent it re-hydrated (which republishes its identity into the new
// trace, §4.5), and one turn spoken since.
func liveSessionTrace() *coltracepb.ExportTraceServiceRequest {
	return cannedTrace(liveTraceIDByte, append(unspokenLiveSessionSpans(),
		cannedSpan{
			id: liveTurnSpanID, parent: liveLoopSpanID, name: "live turn",
			start: 1020, end: 1030,
			attrs: cannedMessageAttrs("user"),
		},
	)...)
}

// unspokenLiveSessionSpans is the resuming CLI's trace the moment the restore
// lands: the root and the re-hydrated agent, with nothing said in this session
// yet -- what the user is looking at before their first message.
func unspokenLiveSessionSpans() []cannedSpan {
	return []cannedSpan{
		{id: liveRootSpanID, name: "dagger agent -r", start: 1000},
		{
			id: liveLoopSpanID, parent: liveRootSpanID, name: "agent: interactive",
			start: 1010,
			attrs: cannedAgentAttrs(importChiefAgentID, "interactive", "sha256:chief"),
		},
	}
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
	return importedTraceDBBeside(t, liveSessionTrace(), withWorker)
}

// importedTraceDBBeside is importedTraceDB with the live session's own trace
// supplied by the caller.
func importedTraceDBBeside(t *testing.T, live *coltracepb.ExportTraceServiceRequest, withWorker bool) *dagui.DB {
	t.Helper()
	ctx := context.Background()
	db := dagui.NewDB()

	// The live session is already publishing by the time the fetch runs: its
	// root is the DB's root and its primary span.
	require.NoError(t, db.ExportSpans(ctx, telemetry.SpansFromPB(live.GetResourceSpans())))

	imp := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   db,
		Logs:    db.LogExporter(),
		Metrics: db.MetricExporter(),
	})
	require.NoError(t, imp.ImportSpans(ctx, foreignSessionTrace(withWorker)))
	require.NoError(t, imp.ImportLogs(ctx, foreignAgentControlLogs(withWorker, "RUNNING")))
	require.NoError(t, imp.Seal(ctx))

	// The chief was re-hydrated into the live session, which republishes its
	// control under a new incarnation there.
	ingestAgentControl(t, db, agentcontrol.Agent{
		Key: agentcontrol.Key{
			Namespace: agentcontrol.Namespace{
				Session:     "live",
				Trace:       trace.TraceID{liveTraceIDByte}.String(),
				Incarnation: "live",
			},
			Handle: importChiefAgentID,
		},
		Revision: 1, Name: "interactive", CallDigest: "sha256:chief",
		State: "IDLE", Digest: cannedAnchorDigest, Activity: time.Unix(1010, 0),
	})
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
	require.Equal(t, "RUNNING", scout.State, "fixture: the capture's last control record")
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

// TestImportKeepRootsLeavesTheRootAsPrimary is the `dagger trace` shape: the
// imported trace is the only trace the DB holds, so its root must stay a real
// root -- the DB's own root and primary span, rendering its children the way a
// live session's root does -- rather than a passthrough second root.
func TestImportKeepRootsLeavesTheRootAsPrimary(t *testing.T) {
	ctx := context.Background()
	db := dagui.NewDB()

	imp := enginetel.NewTraceImporter(enginetel.TraceImportSinks{
		Spans:   db,
		Logs:    db.LogExporter(),
		Metrics: db.MetricExporter(),
	})
	imp.KeepRoots = true
	require.NoError(t, imp.ImportSpans(ctx, foreignSessionTrace(true)))
	require.NoError(t, imp.Seal(ctx))

	rootID := prettyTestSpanID(foreignRootSpanID)
	root := db.Spans.Map[rootID]
	require.NotNil(t, root)
	require.False(t, root.Passthrough, "KeepRoots must not stamp the root passthrough")
	require.Equal(t, rootID, db.PrimarySpan, "the imported root must become the primary span")
	require.NotNil(t, db.RootSpan)
	require.Equal(t, rootID, db.RootSpan.ID)

	// Sealing still applies: the capture's unfinished spans do not spin.
	require.False(t, root.IsRunning(), "the imported root was not sealed")
	require.True(t, root.Canceled)

	// Zoomed to the root, the view is the root's children -- what a
	// passthrough root would replace with its revealed spans only.
	view := db.RowsView(dagui.FrontendOpts{ZoomedSpan: rootID})
	require.Contains(t, view.BySpan, prettyTestSpanID(foreignLoopSpanID),
		"the root's children must render beneath the zoomed root")
}

// TestImportMergesTheAgentsLoopSpans is what importing into the LIVE DB buys
// (§5.1, §4.5): a re-hydrated agent keeps its runtime handle, so its old life and
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
// whole-trace surfacing fix exists for: with ONE agent the roster is not
// switchable and promotion falls back to the whole trace, which resolved its
// nil root to the LIVE root — so every message span hanging off the imported
// root was filed as contained and dropped, and the restored session opened
// with an empty scrollback.
func TestFocusedAgentTranscriptIncludesTheImportedTurns(t *testing.T) {
	db := importedTraceDB(t, false)
	require.Len(t, db.Agents(), 1,
		"fixture: one agent, so the roster cannot switch and the transcript is whole-trace")

	handler := &focusShellHandler{target: importChiefAgentID}
	fe := focusTestFrontend(t, db, handler)
	fe.recalculateViewLocked()

	require.Equal(t, map[string]bool{"imported turn": true, "live turn": true},
		revealedNames(t, fe),
		"the promoted transcript must span both of the agent's lives")
}

// TestRestoredTranscriptShowsBeforeTheFirstMessage is the moment right after
// a restore lands, before the user has said anything: the live session's own
// trace holds no message yet, so every turn on screen hangs off the IMPORTED
// root. Gating promotion on the live root's subtree left the scrollback empty
// until the first message gave that subtree something to surface.
func TestRestoredTranscriptShowsBeforeTheFirstMessage(t *testing.T) {
	live := cannedTrace(liveTraceIDByte, unspokenLiveSessionSpans()...)

	t.Run("single agent", func(t *testing.T) {
		db := importedTraceDBBeside(t, live, false)
		handler := &focusShellHandler{target: importChiefAgentID}
		fe := focusTestFrontend(t, db, handler)
		fe.recalculateViewLocked()

		require.Equal(t, map[string]bool{"imported turn": true}, revealedNames(t, fe),
			"the restored transcript must render before this session says anything")
	})

	t.Run("focused agent", func(t *testing.T) {
		db := importedTraceDBBeside(t, live, true)
		handler := &focusShellHandler{target: importChiefAgentID}
		fe := focusTestFrontend(t, db, handler)
		fe.updateAgentRoster()
		require.True(t, fe.agentRoster.Switchable(), "fixture: two agents, so the transcript scopes to focus")
		fe.recalculateViewLocked()

		require.Equal(t, map[string]bool{"imported turn": true}, revealedNames(t, fe),
			"the focused agent's restored transcript must render before this session says anything")
	})
}
