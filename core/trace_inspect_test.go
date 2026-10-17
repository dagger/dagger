package core

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	otellog "go.opentelemetry.io/otel/log"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/engine/agentcontrol"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// traceInspectStore builds a small session in a telemetry store:
//
//	root (1s)
//	├── build (ERROR "exit code: 1", origin: compile)
//	│   ├── compile (internal, ERROR)
//	│   └── link
//	├── test (0.5s)
//	└── Container.asService (install span)
//	    └── redis exec (service, hostname "cache", running; cause-links install)
//
// Every span is a real hex ID: link targets round-trip through OTLP.
func traceInspectStore(t *testing.T) (*clientdb.DB, map[string]string) {
	t.Helper()
	ctx := context.Background()
	store, err := clientdb.NewDBs(t.TempDir()).Open(ctx, "inspect-test")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })

	ids := map[string]string{
		"root":    "0000000000000001",
		"build":   "0000000000000002",
		"compile": "0000000000000003",
		"link":    "0000000000000004",
		"test":    "0000000000000005",
		"install": "0000000000000006",
		"exec":    "0000000000000007",
	}
	const traceID = "000102030405060708090a0b0c0d0e0f"
	start := time.Unix(100, 0)
	at := func(d time.Duration) int64 { return start.Add(d).UnixNano() }
	ended := func(d time.Duration) sql.NullInt64 { return sql.NullInt64{Int64: at(d), Valid: true} }
	parent := func(name string) sql.NullString { return validSpanID(ids[name]) }

	stringAttr := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{
			Key:   key,
			Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}},
		}
	}
	// The root is still running -- as a session's root span is while an
	// agent works in it -- so dagui doesn't mark its running descendants as
	// left behind (canceled) when it ends.
	spans := []clientdb.Span{
		{TraceID: traceID, SpanID: ids["root"], Name: "root", StartTime: at(0), Attributes: marshalSpanAttrs(t)},
		{TraceID: traceID, SpanID: ids["build"], ParentSpanID: parent("root"), Name: "go build", StartTime: at(0), EndTime: ended(800 * time.Millisecond),
			StatusCode: int64(codes.Error), StatusMessage: "exit code: 1", Attributes: marshalSpanAttrs(t),
			Links: marshalPurposeLink(t, traceID, ids["compile"], telemetry.LinkPurposeErrorOrigin)},
		{TraceID: traceID, SpanID: ids["compile"], ParentSpanID: parent("build"), Name: "compile", StartTime: at(0), EndTime: ended(700 * time.Millisecond),
			StatusCode: int64(codes.Error), StatusMessage: "exit code: 1", Attributes: marshalSpanAttrs(t, boolAttr(telemetry.UIInternalAttr))},
		{TraceID: traceID, SpanID: ids["link"], ParentSpanID: parent("build"), Name: "link", StartTime: at(700 * time.Millisecond), EndTime: ended(800 * time.Millisecond), Attributes: marshalSpanAttrs(t)},
		{TraceID: traceID, SpanID: ids["test"], ParentSpanID: parent("root"), Name: "go test", StartTime: at(0), EndTime: ended(500 * time.Millisecond), Attributes: marshalSpanAttrs(t)},
		{TraceID: traceID, SpanID: ids["install"], ParentSpanID: parent("root"), Name: "Container.asService", StartTime: at(0), EndTime: ended(10 * time.Millisecond), Attributes: marshalSpanAttrs(t)},
		{TraceID: traceID, SpanID: ids["exec"], ParentSpanID: parent("install"), Name: "redis exec", StartTime: at(10 * time.Millisecond),
			Attributes: marshalSpanAttrs(t, boolAttr(telemetryattrs.ServiceAttr), stringAttr(telemetryattrs.ServiceNameAttr, "cache")),
			Links:      marshalCauseLink(t, traceID, ids["install"])},
	}
	for i := range spans {
		// Well-formed empty payloads, so decoding into dagui doesn't log
		// a warning per row.
		if spans[i].Links == nil {
			spans[i].Links = []byte("[]")
		}
		spans[i].Events = []byte("[]")
		spans[i].Resource = []byte("{}")
		spans[i].InstrumentationScope = []byte("{}")
	}
	_, err = store.AppendSpans(spans)
	require.NoError(t, err)
	return store, ids
}

func TestFindSpansIn(t *testing.T) {
	ctx := context.Background()
	store, ids := traceInspectStore(t)

	// Session-wide by name: only the matches, in start-time order, with the
	// span's own status.
	got, err := findSpansIn(ctx, store, "go ", "", 0)
	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		ids["build"] + "  ERROR  go build  [path=root/go build]",
		ids["test"] + "  ok     go test  [path=root/go test]",
	}, "\n")+"\n", got)

	// Matching on the path too reaches everything beneath a named parent.
	got, err = findSpanPageIn(ctx, store, spanSearch{Query: "go ", Match: spanMatchPath})
	require.NoError(t, err)
	require.Equal(t, strings.Join([]string{
		ids["build"] + "  ERROR  go build  [path=root/go build]",
		ids["compile"] + "  ERROR  compile  [path=root/go build/compile]",
		ids["test"] + "  ok     go test  [path=root/go test]",
		ids["link"] + "  ok     link  [path=root/go build/link]",
	}, "\n")+"\n", got)
	_, err = findSpanPageIn(ctx, store, spanSearch{Match: "bogus"})
	require.ErrorContains(t, err, "invalid match")

	// A service is found by its hostname (an attribute, not the span name),
	// tagged as such, and reported running.
	got, err = findSpansIn(ctx, store, "cache", "", 0)
	require.NoError(t, err)
	require.Equal(t, ids["exec"]+"  run    redis exec  [service cache]  [path=root/Container.asService/redis exec]\n", got)

	// Scoped to a subtree: the root itself plus everything beneath, over
	// child edges and cause links; nothing from outside it.
	got, err = findSpansIn(ctx, store, "", ids["build"], 0)
	require.NoError(t, err)
	require.Contains(t, got, "go build")
	require.Contains(t, got, "compile")
	require.Contains(t, got, "link")
	require.NotContains(t, got, "go test")
	require.NotContains(t, got, ids["root"]+"  ")

	// The limit keeps the newest matches and counts the dropped ones.
	got, err = findSpansIn(ctx, store, "", "", 2)
	require.NoError(t, err)
	require.Contains(t, got, "... 5 earlier matching spans omitted")
	require.Contains(t, got, "redis exec")
	require.NotContains(t, got, ids["build"]+"  ")

	// No match says how much was searched and where.
	got, err = findSpansIn(ctx, store, "nope", "", 0)
	require.NoError(t, err)
	require.Equal(t, `(no spans matching "nope" among the 7 in this session)`, got)
	got, err = findSpansIn(ctx, store, "nope", ids["test"], 0)
	require.NoError(t, err)
	require.Equal(t, `(no spans matching "nope" among the 1 in the subtree of span `+ids["test"]+")", got)

	// An unknown root is an error, not an empty listing.
	_, err = findSpansIn(ctx, store, "", "00000000000000ff", 0)
	require.ErrorContains(t, err, "no span")
}

func TestFindSpansFullTestIdentity(t *testing.T) {
	store, ids := traceInspectStore(t)
	attr := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{Key: key, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}}}
	}
	rows := []clientdb.Span{
		{SpanID: "0000000000000011", Name: "cleanup", ParentSpanID: validSpanID(ids["test"]), Attributes: marshalSpanAttrs(t, attr("test.case.name", "TestProvision/docker/cleanup"), attr("test.status", "failure")), StatusCode: int64(codes.Error)},
		{SpanID: "0000000000000012", Name: "cleanup", ParentSpanID: validSpanID(ids["test"]), Attributes: marshalSpanAttrs(t, attr("test.case.name", "TestProvision/podman/cleanup"), attr("test.status", "success"))},
		{SpanID: "0000000000000013", Name: "continue", ParentSpanID: validSpanID("0000000000000011"), Attributes: marshalSpanAttrs(t), StatusCode: int64(codes.Error)},
	}
	for i := range rows {
		rows[i].TraceID = "000102030405060708090a0b0c0d0e0f"
		rows[i].StartTime = int64(100 + i)
		rows[i].EndTime = sql.NullInt64{Int64: 200, Valid: true}
		rows[i].Links = []byte("[]")
	}
	_, err := store.AppendSpans(rows)
	require.NoError(t, err)
	// By name, a test's full identity names it -- not its descendants.
	got, err := findSpanPageIn(t.Context(), store, spanSearch{Query: "TestProvision/docker/cleanup"})
	require.NoError(t, err)
	require.Equal(t, "0000000000000011  ERROR  cleanup  [path=TestProvision/docker/cleanup]\n", got)
	// By path, everything beneath it too.
	search := spanSearch{Query: "TestProvision/docker/cleanup", Status: "failed", Match: spanMatchPath, Limit: 1}
	got, err = findSpanPageIn(t.Context(), store, search)
	require.NoError(t, err)
	require.Contains(t, got, "0000000000000013  ERROR  continue")
	require.Contains(t, got, "path=TestProvision/docker/cleanup/continue")
	require.Contains(t, got, `match: "path", offset: 1, limit: 1`)
	require.NotContains(t, got, "podman")
	search.Offset = 1
	got, err = findSpanPageIn(t.Context(), store, search)
	require.NoError(t, err)
	require.Contains(t, got, "0000000000000011  ERROR  cleanup")
	require.NotContains(t, got, "continue")
	_, err = findSpanPageIn(t.Context(), store, spanSearch{Status: "bogus", Limit: 1})
	require.Error(t, err)
}

func TestFindSpansImportedOrder(t *testing.T) {
	ctx := t.Context()
	live, err := clientdb.NewDBs(t.TempDir()).Open(ctx, "live")
	require.NoError(t, err)
	defer live.Close()
	// Trace-ID order opposes chronology. Tied starts must sort by trace ID
	// before span ID, regardless of store or row ingestion order.
	newer, older := "11111111111111111111111111111111", "22222222222222222222222222222222"
	groups := [][]clientdb.Span{
		{
			{TraceID: newer, SpanID: "0000000000000002", Name: "newest", StartTime: 300},
			{TraceID: newer, SpanID: "0000000000000005", Name: "tie second", StartTime: 200},
			{TraceID: newer, SpanID: "0000000000000004", Name: "tie first", StartTime: 200},
		},
		{
			{TraceID: older, SpanID: "0000000000000003", Name: "tie third", StartTime: 200},
			{TraceID: older, SpanID: "0000000000000001", Name: "oldest", StartTime: 100},
		},
	}
	for _, rows := range groups {
		for i := range rows {
			rows[i].EndTime = sql.NullInt64{Int64: rows[i].StartTime + 1, Valid: true}
			rows[i].Attributes, rows[i].Links, rows[i].Events = []byte("[]"), []byte("[]"), []byte("[]")
			rows[i].Resource, rows[i].InstrumentationScope = []byte("{}"), []byte("{}")
		}
		_, err := live.ImportTrace(ctx, rows[0].TraceID, func(dst *clientdb.DB) error {
			_, err := dst.AppendSpans(rows)
			return err
		})
		require.NoError(t, err)
	}
	imports := live.InspectionStores()[1:]
	require.Len(t, imports, 2)
	want := "0000000000000001  ok     oldest\n" +
		"0000000000000004  ok     tie first\n" +
		"0000000000000005  ok     tie second\n" +
		"0000000000000003  ok     tie third\n" +
		"0000000000000002  ok     newest\n"
	for _, stores := range [][]*clientdb.DB{imports, {imports[1], imports[0]}} {
		got, err := findSpansIn(ctx, live, "", "", 0, stores...)
		require.NoError(t, err)
		require.Equal(t, want, got)
		got, err = findSpansIn(ctx, live, "", "", 2, stores...)
		require.NoError(t, err)
		require.Contains(t, got, "... 3 earlier matching spans omitted")
		require.True(t, strings.HasSuffix(got, "0000000000000003  ok     tie third\n0000000000000002  ok     newest\n"), got)
		require.NotContains(t, got, "oldest")
	}
}

func TestInspectSpanIn(t *testing.T) {
	ctx := context.Background()
	store, ids := traceInspectStore(t)

	// The failed span: its error, the origin resolved by name, the chain up,
	// and the children one level down with their own status and flags.
	got, err := inspectSpanIn(ctx, store, ids["build"])
	require.NoError(t, err)
	for _, want := range []string{
		"span:     " + ids["build"] + "  go build",
		"status:   ERROR",
		"error:    exit code: 1",
		"error origins:\n  " + ids["compile"] + "  compile",
		"duration: 800ms",
		"parents (nearest first):\n  " + ids["root"] + "  root",
		"children (loaded): 2",
		"  " + ids["compile"] + "  ERROR  compile  [internal]",
		"  " + ids["link"] + "  ok     link",
	} {
		require.Contains(t, got, want)
	}
	// Grandchildren are not loaded: one edge down only.
	rootDetail, err := inspectSpanIn(ctx, store, ids["root"])
	require.NoError(t, err)
	require.Contains(t, rootDetail, "children (loaded): 3")
	require.NotContains(t, rootDetail, "compile")

	// A cause-linked child counts as a child of the span it links to.
	installDetail, err := inspectSpanIn(ctx, store, ids["install"])
	require.NoError(t, err)
	require.Contains(t, installDetail, "  "+ids["exec"]+"  run    redis exec  [service=cache]")

	_, err = inspectSpanIn(ctx, store, "00000000000000ff")
	require.ErrorContains(t, err, "no span")
}

func TestSpanTimingsIn(t *testing.T) {
	ctx := context.Background()
	store, ids := traceInspectStore(t)
	now := time.Unix(100, 0).Add(2 * time.Second)

	got, err := spanTimingsIn(ctx, store, ids["build"], 0, 0, now)
	require.NoError(t, err)
	for _, want := range []string{
		"root: " + ids["build"] + `  "go build"`,
		ids["build"] + "  " + ids["root"] + "  0s  800ms  \"go build\"",
		// Internal spans are included: timings are raw, not the UI's view.
		ids["compile"] + "  " + ids["build"] + "  0s  700ms  \"compile\"",
		ids["link"] + "  " + ids["build"] + "  700ms  100ms  \"link\"",
		"shown: 3; omitted: 0",
	} {
		require.Contains(t, got, want)
	}
	require.NotContains(t, got, "go test")

	// The whole trace: the running spans report elapsed time so far, and
	// minDuration prunes the short ones.
	got, err = spanTimingsIn(ctx, store, ids["root"], 600*time.Millisecond, 0, now)
	require.NoError(t, err)
	require.Contains(t, got, "0s  2s (so far)  \"root\"")
	require.Contains(t, got, "10ms  1.99s (so far)  \"redis exec\"")
	require.NotContains(t, got, "\"link\"")
	require.Contains(t, got, "shown: 4; omitted: 3 (3 below minDuration, 0 over limit); loaded subtree: 7")

	_, err = spanTimingsIn(ctx, store, "00000000000000ff", 0, 0, now)
	require.ErrorContains(t, err, "no span")
}

// agentInspectTraceID is the trace agentInspectStore's session runs in.
const agentInspectTraceID = "000102030405060708090a0b0c0d0e0f"

// agentInspectStore builds a session where a chief agent spawned a worker
// that has finished its turn:
//
//	root
//	└── agent: chief (loop span, RUNNING)
//	    ├── FindSpans (the chief's tool call)
//	    └── Staff.spawn
//	        └── agent: doer (loop span, still open; IDLE)
//	            └── git rev-parse
//	                └── dagql.publishResult (internal)
//
// The loop spans never end while their agents live; only the control records
// say the worker is idle.
func agentInspectStore(t *testing.T) (*clientdb.DB, map[string]string) {
	t.Helper()
	ctx := context.Background()
	store, err := clientdb.NewDBs(t.TempDir()).Open(ctx, "agent-inspect-test")
	require.NoError(t, err)
	t.Cleanup(func() { store.Close() })
	ids := map[string]string{
		"root":    "0000000000000001",
		"chief":   "0000000000000002",
		"tool":    "0000000000000003",
		"spawn":   "0000000000000004",
		"doer":    "0000000000000005",
		"git":     "0000000000000006",
		"publish": "0000000000000007",
	}
	str := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{Key: key, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}}}
	}
	agent := func(handle, name string) []byte {
		return marshalSpanAttrs(t, boolAttr(telemetryattrs.AgentAttr),
			str(telemetryattrs.AgentIDAttr, handle), str(telemetryattrs.AgentNameAttr, name))
	}
	ended := sql.NullInt64{Int64: 200, Valid: true}
	spans := []clientdb.Span{
		{SpanID: ids["root"], Name: "root", StartTime: 100, Attributes: marshalSpanAttrs(t)},
		{SpanID: ids["chief"], ParentSpanID: validSpanID(ids["root"]), Name: "agent: chief", StartTime: 101, Attributes: agent("chief-handle", "chief")},
		{SpanID: ids["tool"], ParentSpanID: validSpanID(ids["chief"]), Name: "FindSpans", StartTime: 102, EndTime: ended, Attributes: marshalSpanAttrs(t)},
		{SpanID: ids["spawn"], ParentSpanID: validSpanID(ids["chief"]), Name: "Staff.spawn", StartTime: 103, EndTime: ended, Attributes: marshalSpanAttrs(t)},
		{SpanID: ids["doer"], ParentSpanID: validSpanID(ids["spawn"]), Name: "agent: doer", StartTime: 104, Attributes: agent("doer-handle", "doer")},
		{SpanID: ids["git"], ParentSpanID: validSpanID(ids["doer"]), Name: "git rev-parse", StartTime: 105, EndTime: ended, Attributes: marshalSpanAttrs(t)},
		{SpanID: ids["publish"], ParentSpanID: validSpanID(ids["git"]), Name: "dagql.publishResult", StartTime: 106, EndTime: ended, Attributes: marshalSpanAttrs(t, boolAttr(telemetry.UIInternalAttr))},
	}
	for i := range spans {
		spans[i].TraceID = agentInspectTraceID
		spans[i].Links, spans[i].Events = []byte("[]"), []byte("[]")
		spans[i].Resource, spans[i].InstrumentationScope = []byte("{}"), []byte("{}")
	}
	_, err = store.AppendSpans(spans)
	require.NoError(t, err)

	control := func(handle, name, state string, revision int64) clientdb.Log {
		a := agentcontrol.Agent{
			Key: agentcontrol.Key{
				Namespace: agentcontrol.Namespace{Session: "session", Trace: agentInspectTraceID, Incarnation: "incarnation"},
				Handle:    handle,
			},
			Revision: revision, Name: name, State: state, Digest: "xxh3:conversation",
			Activity: time.Unix(0, 100+revision),
		}
		rec := a.Record()
		var attrs []*otlpcommonv1.KeyValue
		rec.WalkAttributes(func(kv otellog.KeyValue) bool {
			attrs = append(attrs, &otlpcommonv1.KeyValue{Key: kv.Key, Value: telemetry.LogValueToPB(kv.Value)})
			return true
		})
		encoded, err := clientdb.MarshalProtoJSONs(attrs)
		require.NoError(t, err)
		body, err := proto.Marshal(telemetry.LogValueToPB(rec.Body()))
		require.NoError(t, err)
		return clientdb.Log{TraceID: validSpanID(agentInspectTraceID), Attributes: encoded, Body: body}
	}
	_, err = store.AppendLogs([]clientdb.Log{
		control("chief-handle", "chief", "RUNNING", 1),
		control("doer-handle", "doer", "RUNNING", 1),
		control("doer-handle", "doer", "IDLE", 2),
	})
	require.NoError(t, err)
	return store, ids
}

func TestFindSpansAgents(t *testing.T) {
	ctx := t.Context()
	store, ids := agentInspectStore(t)

	// An agent is found by its own name, not every span beneath it, and its
	// idle loop span reads idle rather than run.
	got, err := findSpanPageIn(ctx, store, spanSearch{Query: "doer"})
	require.NoError(t, err)
	require.Equal(t, ids["doer"]+"  idle   agent: doer  [path=root/agent: chief/Staff.spawn/agent: doer]\n", got)
	got, err = findSpanPageIn(ctx, store, spanSearch{Query: "doer", Match: spanMatchPath})
	require.NoError(t, err)
	require.Contains(t, got, "git rev-parse")
	require.Contains(t, got, "dagql.publishResult")

	// The status filter goes by the agents' observed state.
	got, err = findSpanPageIn(ctx, store, spanSearch{Status: "run"})
	require.NoError(t, err)
	require.Contains(t, got, ids["chief"]+"  run    agent: chief")
	require.NotContains(t, got, ids["doer"])
	got, err = findSpanPageIn(ctx, store, spanSearch{Status: "idle"})
	require.NoError(t, err)
	require.Contains(t, got, ids["doer"]+"  idle   agent: doer")
	require.NotContains(t, got, ids["chief"])

	// The caller's own spans are left out and counted -- but not those of
	// the worker it spawned, which run beneath its loop span too.
	chief := traceCaller{AgentID: "chief-handle", TraceID: agentInspectTraceID}
	got, err = findSpanPageIn(ctx, store, spanSearch{Caller: chief})
	require.NoError(t, err)
	require.NotContains(t, got, ids["chief"])
	require.NotContains(t, got, ids["tool"])
	require.Contains(t, got, ids["doer"]+"  idle   agent: doer")
	require.Contains(t, got, "git rev-parse")
	require.Contains(t, got, "(3 matching spans of your own -- your tool calls and messages -- hidden; includeSelf: true lists them)")
	got, err = findSpanPageIn(ctx, store, spanSearch{Caller: chief, IncludeSelf: true})
	require.NoError(t, err)
	require.Contains(t, got, ids["chief"]+"  run    agent: chief")
	require.NotContains(t, got, "hidden")
	got, err = findSpanPageIn(ctx, store, spanSearch{Query: "FindSpans", Caller: chief})
	require.NoError(t, err)
	require.Equal(t, `(no spans matching "FindSpans" among the 7 in this session)`+"\n"+
		"(1 matching spans of your own -- your tool calls and messages -- hidden; includeSelf: true lists them)", got)
	// The same handle in another trace is a predecessor, not the caller.
	elsewhere := traceCaller{AgentID: "chief-handle", TraceID: "ffffffffffffffffffffffffffffffff"}
	got, err = findSpanPageIn(ctx, store, spanSearch{Query: "FindSpans", Caller: elsewhere})
	require.NoError(t, err)
	require.Contains(t, got, ids["tool"])

	// Inspecting the worker's loop span says what the agent is doing.
	detail, err := inspectSpanIn(ctx, store, ids["doer"])
	require.NoError(t, err)
	require.Contains(t, detail, "status:   idle\nagent:    doer, last reported IDLE\n")
	require.Contains(t, detail, "ended:    (agent loop still open; idle between turns)")
	detail, err = inspectSpanIn(ctx, store, ids["spawn"])
	require.NoError(t, err)
	require.Contains(t, detail, "  "+ids["doer"]+"  idle   agent: doer")
}

func TestRenderSpanPath(t *testing.T) {
	segs := func(names ...string) []spanPathSeg {
		var path []spanPathSeg
		for _, name := range names {
			path = append(path, spanPathSeg{name: name, agent: strings.HasPrefix(name, "agent: ")})
		}
		return path
	}
	// Short paths are left whole.
	require.Equal(t, "root/go build/compile", renderSpanPath(segs("root", "go build", "compile")))

	// Long ones keep the root, the nearest agent and the two leaf-most
	// levels, counting what each elision dropped.
	long := segs("root", "POST /query", "Agent.send", "agent: agent", "thinking", "staff_spawn",
		"Staff.spawn", "staff:Staff.spawn", "POST /query", "Agent.send", "agent: doer",
		"withExec", "Container.withExec", "git rev-parse")
	require.Equal(t, "root/…(9)…/agent: doer/…(1)…/Container.withExec/git rev-parse", renderSpanPath(long))
	// An agent's own loop span keeps the agent above it: its spawner.
	require.Equal(t, "root/…(2)…/agent: agent/…(5)…/Agent.send/agent: doer", renderSpanPath(long[:11]))

	// Without an agent, the middle goes in one run; huge segments are clamped.
	huge := strings.Repeat("x", 500)
	got := renderSpanPath(segs("root", "a", "b", "c", huge, "leaf"))
	require.True(t, strings.HasPrefix(got, "root/…(3)…/xxxx"), got)
	require.True(t, strings.HasSuffix(got, "/leaf"), got)
	require.Less(t, len(got), 2*spanPathMaxBytes)
}
