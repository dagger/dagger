package dagui

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"runtime"
	"strconv"
	"testing"
	"time"
	"unsafe"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	semconv "go.opentelemetry.io/otel/semconv/v1.40.0"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// footprintTrace generates a synthetic trace shaped like a long `dagger
// agent` session, for measuring how much memory the DB holds per span.
//
// Spans are generated on demand, one export batch at a time, and every string
// is freshly allocated per span, the way OTLP decoding hands them to the DB:
// whatever the DB retains is charged to it, and whatever it doesn't is
// garbage by the time the heap is measured.
//
// Most spans are dagql calls (digest, base64 call payload, output, cache
// evidence, origin client), many of them internal; the rest are execs, LLM
// messages, tool calls, tests and checks. Spans sit in deep, narrow chains
// under a few wide agent-loop parents; some carry causal links, some fail
// with error-origin links.
type footprintTrace struct {
	n       int
	traceID trace.TraceID
	epoch   time.Time
	parents []int32
}

// footprintLongLived spans (the root and the agent loops) run for the whole
// session.
const footprintLongLived = 9

func newFootprintTrace(n int) *footprintTrace {
	tr := &footprintTrace{
		n:       n,
		traceID: trace.TraceID{1},
		epoch:   time.Unix(1_700_000_000, 0),
		parents: make([]int32, n),
	}
	for i := 1; i < n; i++ {
		h := footprintHash(i, 0)
		var p int
		switch r := h % 100; {
		case i < footprintLongLived:
			p = 0 // agent loops under the root
		case r < 2:
			p = 0
		case r < 10:
			p = 1 + int(h>>8)%(footprintLongLived-1) // a wide agent loop
		default:
			p = max(0, i-1-int(h>>8)%48) // deep, narrow call chains
		}
		tr.parents[i] = int32(p)
	}
	return tr
}

// footprintHash is a splitmix64 hash of a span index and a salt, so every
// span's random choices are reproducible without keeping generator state.
func footprintHash(i, salt int) uint64 {
	z := uint64(i)*0x9e3779b97f4a7c15 + uint64(salt)*0xbf58476d1ce4e5b9
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

func (tr *footprintTrace) spanID(i int) trace.SpanID {
	var id trace.SpanID
	binary.BigEndian.PutUint64(id[:], uint64(i)+1)
	return id
}

func (tr *footprintTrace) spanContext(i int) trace.SpanContext {
	return trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: tr.traceID,
		SpanID:  tr.spanID(i),
	})
}

func footprintDigest(kind string, i int) string {
	return fmt.Sprintf("xxh3:%016x", footprintHash(i, len(kind)))
}

var footprintCalls = []struct{ typ, field string }{
	{"Container", "withExec"},
	{"Container", "withDirectory"},
	{"Container", "withMountedCache"},
	{"Directory", "file"},
	{"Directory", "withNewFile"},
	{"Query", "container"},
	{"Query", "host"},
	{"File", "contents"},
	{"Workspace", "directory"},
	{"Env", "withStringInput"},
}

// attr clones key and value so the span owns them, like a decoded OTLP span.
func footprintString(key, val string) attribute.KeyValue {
	return attribute.String(string([]byte(key)), string([]byte(val)))
}

func footprintBool(key string, val bool) attribute.KeyValue {
	return attribute.Bool(string([]byte(key)), val)
}

// stub returns span i as exported when it starts (done=false) or ends.
func (tr *footprintTrace) stub(i int, done bool) sdktrace.ReadOnlySpan {
	h := footprintHash(i, 1)
	start := tr.epoch.Add(time.Duration(i) * time.Millisecond)
	stub := tracetest.SpanStub{
		SpanContext: tr.spanContext(i),
		StartTime:   start,
	}
	if i > 0 {
		stub.Parent = tr.spanContext(int(tr.parents[i]))
	}
	if done {
		stub.EndTime = start.Add(time.Duration(1+h%5000) * time.Millisecond)
	}
	attrs := []attribute.KeyValue{
		footprintString(telemetryattrs.TelemetryOriginClientIDAttr, "pbhbtxrpvpl1sxuvaqm5skyrl"),
	}
	switch kind := h % 100; {
	case i == 0:
		stub.Name = "dagger agent"
	case i < footprintLongLived:
		stub.Name = fmt.Sprintf("agent loop %d", i)
		attrs = append(attrs,
			footprintBool(telemetryattrs.AgentAttr, true),
			footprintString(telemetryattrs.AgentIDAttr, fmt.Sprintf("agent-%d", i)),
			footprintString(telemetryattrs.AgentNameAttr, fmt.Sprintf("worker-%d", i)),
			footprintString(telemetryattrs.AgentCallDigestAttr, footprintDigest("agent", i)),
		)
	case kind < 6:
		// LLM message
		stub.Name = "LLM response"
		attrs = append(attrs,
			footprintString(telemetry.LLMRoleAttr, telemetry.LLMRoleAssistant),
			footprintString(telemetry.UIActorEmojiAttr, "🤖"),
			footprintString(telemetry.UIMessageAttr, telemetry.UIMessageReceived),
			footprintString(telemetryattrs.LLMCallDigestAttr, footprintDigest("llm", i)),
		)
	case kind < 10:
		// tool call
		stub.Name = "read"
		attrs = append(attrs,
			footprintString(telemetry.LLMToolAttr, "read"),
			footprintBool(telemetry.UIBoundaryAttr, true),
			attribute.StringSlice(telemetry.LLMToolArgNamesAttr, []string{"filePath"}),
			attribute.StringSlice(telemetry.LLMToolArgValuesAttr, []string{fmt.Sprintf("dagql/file%d.go", i)}),
		)
	case kind < 12:
		// test case
		stub.Name = fmt.Sprintf("TestThing/case_%d", i)
		attrs = append(attrs,
			footprintString(string(semconv.TestCaseNameKey), stub.Name),
			footprintString(string(semconv.TestSuiteNameKey), "TestThing"),
		)
		if done {
			attrs = append(attrs, footprintString(string(semconv.TestCaseResultStatusKey), "pass"))
		}
	case kind < 13:
		stub.Name = fmt.Sprintf("check-%d", i%40)
		attrs = append(attrs, footprintString(telemetry.CheckNameAttr, stub.Name))
		if done {
			attrs = append(attrs, footprintBool(telemetry.CheckPassedAttr, true))
		}
	case kind < 25:
		// exec
		stub.Name = fmt.Sprintf("exec go test ./pkg%d", i%300)
		attrs = append(attrs,
			footprintString(telemetryattrs.WcprofOpKindAttr, "exec"),
			footprintString(telemetry.DagDigestAttr, footprintDigest("exec", i)),
			footprintString(telemetryattrs.ExecutionContentPreferredDigestAttr, footprintDigest("content", i)),
		)
	default:
		// dagql call
		call := footprintCalls[h>>8%uint64(len(footprintCalls))]
		dig := footprintDigest("call", i)
		stub.Name = call.typ + "." + call.field
		payload, err := (&callpbv1.Call{
			ReceiverDigest: footprintDigest("call", int(tr.parents[i])),
			Type:           &callpbv1.Type{NamedType: call.typ},
			Field:          call.field,
			Args: []*callpbv1.Argument{{
				Name: "path",
				Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{
					String_: fmt.Sprintf("/src/path/%d", i),
				}},
			}},
			Digest: dig,
		}).Encode()
		if err != nil {
			panic(err)
		}
		attrs = append(attrs,
			footprintString(telemetry.DagDigestAttr, dig),
			footprintString(telemetry.DagCallAttr, payload),
			footprintString(telemetryattrs.DagContentPreferredDigestAttr, footprintDigest("content", i)),
		)
		if kind < 70 {
			attrs = append(attrs, footprintBool(telemetry.UIInternalAttr, true))
		}
		if done {
			attrs = append(attrs,
				footprintString(telemetry.DagOutputAttr, footprintDigest("output", i)),
				footprintString(telemetryattrs.CacheContractAttr, "v1"),
				footprintString(telemetryattrs.CacheOutcomeAttr, "hit"),
				footprintString(telemetryattrs.CacheResultIDAttr, strconv.Itoa(i)),
				footprintString(telemetryattrs.CacheTypeAttr, call.typ),
			)
			if h>>16%4 == 0 {
				attrs = append(attrs, footprintBool(telemetry.CachedAttr, true))
			}
		}
	}
	if h>>20%100 < 5 && i > footprintLongLived {
		// a causal link to an earlier span
		stub.Links = append(stub.Links, sdktrace.Link{
			SpanContext: tr.spanContext(int(h>>28) % i),
		})
	}
	if done && h>>24%100 == 0 && i > footprintLongLived {
		// a failure, with an error origin
		stub.Status = sdktrace.Status{Code: codes.Error, Description: "exit code 1"}
		stub.Links = append(stub.Links, sdktrace.Link{
			SpanContext: tr.spanContext(i - 1),
			Attributes: []attribute.KeyValue{
				attribute.String(telemetry.LinkPurposeAttr, telemetry.LinkPurposeErrorOrigin),
			},
		})
	}
	stub.Attributes = attrs
	return stub.Snapshot()
}

// ingest feeds the whole trace through DB.ExportSpans the way a live session
// does: each batch exports the spans that just started (still running) and
// completes the previous batch's spans. The root and agent loops end last.
func (tr *footprintTrace) ingest(ctx context.Context, db *DB, batch int) error {
	for lo := 0; lo < tr.n+batch; lo += batch {
		spans := make([]sdktrace.ReadOnlySpan, 0, 2*batch)
		for i := lo; i < min(lo+batch, tr.n); i++ {
			spans = append(spans, tr.stub(i, false))
		}
		for i := max(lo-batch, footprintLongLived); i < min(lo, tr.n); i++ {
			spans = append(spans, tr.stub(i, true))
		}
		if len(spans) == 0 {
			continue
		}
		if err := db.ExportSpans(ctx, spans); err != nil {
			return err
		}
	}
	final := make([]sdktrace.ReadOnlySpan, 0, footprintLongLived)
	for i := footprintLongLived - 1; i >= 0; i-- {
		final = append(final, tr.stub(i, true))
	}
	return db.ExportSpans(ctx, final)
}

type footprint struct {
	spans          int
	bytesPerSpan   float64
	objectsPerSpan float64
}

var footprintDB *DB

// measureFootprint ingests an n-span footprintTrace into a fresh DB and
// reports the live heap the DB holds per span, measured after a GC.
func measureFootprint(tb testing.TB, n int) footprint {
	tb.Helper()
	tr := newFootprintTrace(n)
	var before, after runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&before)
	db := NewDB()
	if err := tr.ingest(context.Background(), db, 512); err != nil {
		tb.Fatal(err)
	}
	runtime.GC()
	runtime.ReadMemStats(&after)
	if len(db.Spans.Order) != n {
		tb.Fatalf("ingested %d spans, want %d", len(db.Spans.Order), n)
	}
	// Keep the DB reachable past the benchmark, so -memprofile's inuse
	// samples show what it retains.
	footprintDB = db
	return footprint{
		spans:          n,
		bytesPerSpan:   float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / float64(n),
		objectsPerSpan: float64(int64(after.HeapObjects)-int64(before.HeapObjects)) / float64(n),
	}
}

// BenchmarkSpanFootprint reports the DB's retained heap per span after
// ingesting a large synthetic agent-session trace:
//
//	go test ./dagql/dagui -run '^$' -bench SpanFootprint -benchtime 1x
func BenchmarkSpanFootprint(b *testing.B) {
	for _, n := range []int{100_000} {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			var fp footprint
			for b.Loop() {
				fp = measureFootprint(b, n)
			}
			b.ReportMetric(fp.bytesPerSpan, "B/span")
			b.ReportMetric(fp.objectsPerSpan, "objs/span")
		})
	}
}

// TestSpanSize guards the size of Span, which every span in a trace pays:
// with its fields packed to avoid alignment padding, it fits the 1024-byte
// allocation size class (the next is 1152). Objects over 512 bytes that hold
// pointers carry an 8-byte malloc header, so that takes 1016 bytes or less.
func TestSpanSize(t *testing.T) {
	if unsafe.Sizeof(uintptr(0)) != 8 {
		t.Skip("sizes are for 64-bit platforms")
	}
	for _, typ := range []reflect.Type{reflect.TypeFor[Span](), reflect.TypeFor[SpanSnapshot]()} {
		var padding, end uintptr
		for i := range typ.NumField() {
			field := typ.Field(i)
			padding += field.Offset - end
			end = field.Offset + field.Type.Size()
		}
		padding += typ.Size() - end
		t.Logf("%s: %d bytes, %d of them padding", typ.Name(), typ.Size(), padding)
		// the bools never fill a whole word, so allow for one partial one
		if padding >= 8 {
			t.Errorf("%s has %d bytes of padding; group its small fields together", typ.Name(), padding)
		}
	}
	if size := unsafe.Sizeof(Span{}); size > 1024-8 {
		t.Errorf("Span is %d bytes, past the 1024-byte size class (with its 8-byte malloc header)", size)
	}
}

// TestFootprintTrace keeps the footprint generator honest: the trace it
// produces ingests into a coherent tree with the span kinds it claims.
func TestFootprintTrace(t *testing.T) {
	const n = 5_000
	tr := newFootprintTrace(n)
	db := NewDB()
	if err := tr.ingest(context.Background(), db, 512); err != nil {
		t.Fatal(err)
	}
	if len(db.Spans.Order) != n {
		t.Fatalf("ingested %d spans, want %d", len(db.Spans.Order), n)
	}
	var calls, payloads, llm, tests, failed, running int
	for _, span := range db.Spans.Order {
		if span.CallDigest != "" {
			calls++
		}
		if span.Call() != nil {
			payloads++
		}
		if span.LLMRole != "" {
			llm++
		}
		if span.TestCaseName != "" {
			tests++
		}
		if span.IsFailed() {
			failed++
		}
		if span.IsRunning() {
			running++
		}
	}
	if calls == 0 || payloads == 0 || llm == 0 || tests == 0 || failed == 0 {
		t.Fatalf("missing span kinds: calls=%d payloads=%d llm=%d tests=%d failed=%d",
			calls, payloads, llm, tests, failed)
	}
	if running != 0 {
		t.Fatalf("%d spans still running after the trace completed", running)
	}
	if db.RootSpan == nil || db.RootSpan.ChildSpans.Len() == 0 {
		t.Fatal("root span has no children")
	}
}
