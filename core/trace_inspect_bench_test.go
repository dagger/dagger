package core

import (
	"database/sql"
	"fmt"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"

	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// searchSessionTraceID is the trace searchSessionRows' session runs in.
const searchSessionTraceID = "0f0e0d0c0b0a09080706050403020100"

// searchSessionRows builds an agent-session-shaped trace of about n spans,
// the shape FindSpans searches in a long-running engine:
//
//	root (running)
//	└── agent: chief (loop span, running)
//	    ├── tool call i (one per group, ~10 spans each)
//	    │   ├── Container.withExec
//	    │   │   ├── exec go build ./pkg<i>   (every 37th fails)
//	    │   │   └── dagql.publishResult (internal)
//	    │   ├── Directory.file
//	    │   ├── resume (cause-links the withExec)
//	    │   ├── ... filler calls
//	    │   └── [every 50th] go test: TestPkg<i>/case (test case span)
//	    └── [every 100th] redis exec (service "cache<i>")
//
// Each span carries a call-sized attribute, so rows weigh what real ones do.
// Rare names ("needle") appear a handful of times.
func searchSessionRows(tb testing.TB, n int) []clientdb.Span {
	tb.Helper()
	spanHex := func(i int) string { return fmt.Sprintf("%016x", i+1) }
	str := func(key, value string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{Key: key, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: value}}}
	}
	flag := func(key string) *otlpcommonv1.KeyValue {
		return &otlpcommonv1.KeyValue{Key: key, Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BoolValue{BoolValue: true}}}
	}
	attrs := func(kvs ...*otlpcommonv1.KeyValue) []byte {
		// A digest-like payload, as call spans carry.
		kvs = append(kvs, str("dagger.io/bench.payload", fmt.Sprintf("%0256x", len(kvs))))
		data, err := clientdb.MarshalProtoList(kvs)
		if err != nil {
			tb.Fatal(err)
		}
		return data
	}
	causeLink := func(target string) []byte {
		tid, _ := trace.TraceIDFromHex(searchSessionTraceID)
		sid, _ := trace.SpanIDFromHex(target)
		data, err := clientdb.MarshalProtoList(telemetry.SpanLinksToPB([]sdktrace.Link{{
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: tid, SpanID: sid}),
			Attributes:  []attribute.KeyValue{attribute.String(telemetry.LinkPurposeAttr, telemetry.LinkPurposeCause)},
		}}))
		if err != nil {
			tb.Fatal(err)
		}
		return data
	}
	plain := attrs()
	internal := attrs(flag(telemetry.UIInternalAttr))

	rows := make([]clientdb.Span, 0, n+16)
	next := 0
	add := func(parent int, name string, a []byte, mod func(*clientdb.Span)) int {
		i := next
		next++
		row := clientdb.Span{
			TraceID:    searchSessionTraceID,
			SpanID:     spanHex(i),
			Name:       name,
			StartTime:  int64(1000 + i*10),
			EndTime:    sql.NullInt64{Int64: int64(1000 + i*10 + 5), Valid: true},
			Attributes: a,
		}
		if parent >= 0 {
			row.ParentSpanID = validSpanID(spanHex(parent))
		}
		if mod != nil {
			mod(&row)
		}
		rows = append(rows, row)
		return i
	}
	running := func(row *clientdb.Span) { row.EndTime = sql.NullInt64{} }
	root := add(-1, "root", plain, running)
	chief := add(root, "agent: chief", attrs(flag(telemetryattrs.AgentAttr),
		str(telemetryattrs.AgentIDAttr, "chief-handle"), str(telemetryattrs.AgentNameAttr, "chief")), running)
	for g := 0; next < n; g++ {
		tool := add(chief, fmt.Sprintf("tool call %d", g), plain, nil)
		exec := add(tool, "Container.withExec", plain, nil)
		add(exec, fmt.Sprintf("exec go build ./pkg%d", g), plain, func(row *clientdb.Span) {
			if g%37 == 0 {
				row.StatusCode = int64(codes.Error)
				row.StatusMessage = "exit code: 1"
			}
		})
		add(exec, "dagql.publishResult", internal, nil)
		add(tool, "Directory.file", plain, nil)
		add(tool, "resume", plain, func(row *clientdb.Span) { row.Links = causeLink(spanHex(exec)) })
		name := "Container.from"
		if g%500 == 7 {
			name = "needle in a haystack"
		}
		add(tool, name, plain, nil)
		add(tool, "Directory.withNewFile", plain, nil)
		if g%50 == 0 {
			add(tool, "go test", attrs(str("test.case.name", fmt.Sprintf("TestPkg%d/case", g))), nil)
		}
		if g%100 == 0 {
			add(chief, "redis exec", attrs(flag(telemetryattrs.ServiceAttr),
				str(telemetryattrs.ServiceNameAttr, fmt.Sprintf("cache%d", g))), running)
		}
	}
	return rows
}

// searchSessionStore persists searchSessionRows(n) in a fresh store, kept
// open as a live session's is.
func searchSessionStore(tb testing.TB, n int) *clientdb.DB {
	tb.Helper()
	store, err := clientdb.NewDBs(tb.TempDir()).Open(tb.Context(), "search-bench")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { store.Close() })
	rows := searchSessionRows(tb, n)
	for start := 0; start < len(rows); start += 5000 {
		if _, err := store.AppendSpans(rows[start:min(start+5000, len(rows))]); err != nil {
			tb.Fatal(err)
		}
	}
	return store
}

// searchSessionSearches are the session-wide FindSpans an agent issues: a
// name, a path, failures, and the default listing.
var searchSessionSearches = []struct {
	name   string
	search spanSearch
}{
	{"name", spanSearch{Query: "needle", Limit: findSpansDefaultLimit}},
	{"path", spanSearch{Query: "tool call 7/", Match: spanMatchPath, Limit: findSpansDefaultLimit}},
	{"failed", spanSearch{Status: "failed", Limit: findSpansDefaultLimit}},
	{"all", spanSearch{Limit: findSpansDefaultLimit}},
}

// BenchmarkFindSpansSessionWide measures a root-less FindSpans over a
// session of N spans: what each search allocates, per op and per span.
// Set DAGGER_FIND_SPANS_BENCH_SPANS to scale the session.
func BenchmarkFindSpansSessionWide(b *testing.B) {
	n := envInt("DAGGER_FIND_SPANS_BENCH_SPANS", 20000)
	store := searchSessionStore(b, n)
	for _, tc := range searchSessionSearches {
		b.Run(tc.name, func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := findSpanPageIn(b.Context(), store, tc.search); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
