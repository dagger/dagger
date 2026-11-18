package core

import (
	"fmt"
	"regexp"
	"runtime"
	"runtime/metrics"
	"sync/atomic"
	"testing"
	"time"

	telemetry "github.com/dagger/otel-go"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/telemetryattrs"
)

// callSessionFrames builds an agent session's call frames, about n of them:
// container chains with string arguments (one in 500 names a "needle"), and
// agents' conversations -- LLM chains, each stamped by its owner, the
// caller's ("me") among them.
func callSessionFrames(n int) []*callpbv1.Call {
	digest := func(i int) string { return fmt.Sprintf("xxh3:%016x", i+1) }
	str := func(name, value string) *callpbv1.Argument {
		return &callpbv1.Argument{Name: name, Value: &callpbv1.Literal{Value: &callpbv1.Literal_String_{String_: value}}}
	}
	typ := func(name string) *callpbv1.Type { return &callpbv1.Type{NamedType: name, NonNull: true} }
	frames := make([]*callpbv1.Call, 0, n+8)
	add := func(recv int, t, field string, args ...*callpbv1.Argument) int {
		i := len(frames)
		frame := &callpbv1.Call{Digest: digest(i), Field: field, Type: typ(t), Args: args}
		if recv >= 0 {
			frame.ReceiverDigest = digest(recv)
		}
		frames = append(frames, frame)
		return i
	}
	for g := 0; len(frames) < n; g++ {
		if g%10 == 9 {
			// An agent's turn: a stamped conversation grown by a few calls.
			owner := []string{"me", "worker", "other"}[g%3]
			c := add(-1, "LLM", "llm")
			c = add(c, "LLM", "__withOwnerAgent", str("handle", owner))
			c = add(c, "LLM", "withPrompt", str("prompt", fmt.Sprintf("turn %d: look into the needle %d", g, g)))
			add(c, "LLM", "withResponse", str("text", fmt.Sprintf("thought %d %0200x", g, g)))
			continue
		}
		c := add(-1, "Container", "container")
		c = add(c, "Container", "from", str("address", fmt.Sprintf("alpine:%d", g)))
		path := fmt.Sprintf("/src/pkg%d", g)
		if g%500 == 7 {
			path = "/src/needle"
		}
		c = add(c, "Container", "withWorkdir", str("path", path))
		add(c, "Container", "withExec", &callpbv1.Argument{Name: "args", Value: &callpbv1.Literal{Value: &callpbv1.Literal_List{List: &callpbv1.List{Values: []*callpbv1.Literal{
			{Value: &callpbv1.Literal_String_{String_: "go"}},
			{Value: &callpbv1.Literal_String_{String_: "build"}},
			{Value: &callpbv1.Literal_String_{String_: fmt.Sprintf("./pkg%d/...", g)}},
		}}}}})
	}
	return frames
}

// callSessionLogs encodes frames as the call-payload log records the
// engine delivers them in.
func callSessionLogs(tb testing.TB, frames []*callpbv1.Call) []clientdb.Log {
	tb.Helper()
	attrs, err := clientdb.MarshalProtoList([]*otlpcommonv1.KeyValue{{
		Key:   telemetry.ContentTypeAttr,
		Value: &otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_StringValue{StringValue: telemetryattrs.CallPayloadContentType}},
	}})
	if err != nil {
		tb.Fatal(err)
	}
	logs := make([]clientdb.Log, len(frames))
	for i, frame := range frames {
		payload, err := proto.Marshal(frame)
		if err != nil {
			tb.Fatal(err)
		}
		body, err := proto.Marshal(&otlpcommonv1.AnyValue{Value: &otlpcommonv1.AnyValue_BytesValue{BytesValue: payload}})
		if err != nil {
			tb.Fatal(err)
		}
		logs[i] = clientdb.Log{
			TraceID:    validSpanID(searchSessionTraceID),
			SpanID:     validSpanID("0000000000000001"),
			Body:       body,
			Attributes: attrs,
		}
	}
	return logs
}

// callSessionStore persists callSessionFrames(n) in a fresh store.
func callSessionStore(tb testing.TB, n int) *clientdb.DB {
	tb.Helper()
	store, err := clientdb.NewDBs(tb.TempDir()).Open(tb.Context(), "call-bench")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() { store.Close() })
	logs := callSessionLogs(tb, callSessionFrames(n))
	for start := 0; start < len(logs); start += 5000 {
		if _, err := store.AppendLogs(logs[start:min(start+5000, len(logs))]); err != nil {
			tb.Fatal(err)
		}
	}
	return store
}

// sampleHeapPeak starts sampling the heap's object bytes above a freshly
// collected baseline; stop returns the highest sample. Garbage not yet
// collected counts too, so it bounds what a call holds from above.
func sampleHeapPeak() (stop func() uint64) {
	runtime.GC()
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	read := func() uint64 {
		metrics.Read(sample)
		return sample[0].Value.Uint64()
	}
	base := read()
	var peak atomic.Uint64
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(50 * time.Microsecond)
		defer ticker.Stop()
		for {
			if v := read(); v > base && v-base > peak.Load() {
				peak.Store(v - base)
			}
			select {
			case <-done:
				return
			case <-ticker.C:
			}
		}
	}()
	return func() uint64 {
		close(done)
		<-finished
		return peak.Load()
	}
}

// BenchmarkFindCallsSessionWide measures a FindCalls over a session of N
// calls: what each search allocates. Set DAGGER_FIND_CALLS_BENCH_CALLS to
// scale the session.
func BenchmarkFindCallsSessionWide(b *testing.B) {
	n := envInt("DAGGER_FIND_CALLS_BENCH_CALLS", 20000)
	store := callSessionStore(b, n)
	for _, tc := range []struct {
		name    string
		re      string
		exclude string
	}{
		{"rare", "needle", ""},
		{"rare-excluding-self", "needle", "me"},
		{"common", "withExec", ""},
	} {
		b.Run(tc.name, func(b *testing.B) {
			re := regexp.MustCompile(tc.re)
			b.ReportAllocs()
			var peak uint64
			for b.Loop() {
				b.StopTimer()
				stop := sampleHeapPeak()
				b.StartTimer()
				if _, err := findCallsIn(b.Context(), store, re, 200, tc.exclude); err != nil {
					b.Fatal(err)
				}
				b.StopTimer()
				peak = max(peak, stop())
				b.StartTimer()
			}
			b.ReportMetric(float64(peak), "peak-heap-B")
		})
	}
}
