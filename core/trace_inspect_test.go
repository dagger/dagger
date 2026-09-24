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
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"

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
		ids["build"] + "  ERROR  go build",
		ids["test"] + "  ok     go test",
	}, "\n")+"\n", got)

	// A service is found by its hostname (an attribute, not the span name),
	// tagged as such, and reported running.
	got, err = findSpansIn(ctx, store, "cache", "", 0)
	require.NoError(t, err)
	require.Equal(t, ids["exec"]+"  run    redis exec  [service cache]\n", got)

	// Scoped to a subtree: the root itself plus everything beneath, over
	// child edges and cause links; nothing from outside it.
	got, err = findSpansIn(ctx, store, "", ids["build"], 0)
	require.NoError(t, err)
	require.Contains(t, got, "go build")
	require.Contains(t, got, "compile")
	require.Contains(t, got, "link")
	require.NotContains(t, got, "go test")
	require.NotContains(t, got, "root")

	// The limit keeps the newest matches and counts the dropped ones.
	got, err = findSpansIn(ctx, store, "", "", 2)
	require.NoError(t, err)
	require.Contains(t, got, "... 5 earlier matching spans omitted")
	require.Contains(t, got, "redis exec")
	require.NotContains(t, got, "go build")

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
