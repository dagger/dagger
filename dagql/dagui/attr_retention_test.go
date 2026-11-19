package dagui

import (
	"context"
	"fmt"
	"runtime"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"

	"github.com/dagger/dagger/dagql/call/callpbv1"
	"github.com/dagger/dagger/dagql/internal/spanfootprint"
)

// wireSpans is spanfootprint's trace with the attributes the engine stamps
// on dagql call spans beyond the footprint fixture's -- dag.inputs on every
// call (core/telemetry.go), dag.output naming the call's own recipe for an
// object result, and module function attributes on module calls -- delivered
// as the TUI receives it: OTLP protobuf, decoded batch by batch
// (engine/client's SpansFromPB), so every string the DB keeps is one the
// decoder allocated.
type wireSpans struct {
	*spanfootprint.Trace
}

var retentionModules = []string{
	"github.com/dagger/dagger/modules/go@v0.18.2",
	"github.com/dagger/dagger/modules/wolfi@v0.18.2",
	"github.com/shykes/daggerverse/hello@3b7a7e8d2c51f0a4c2b6a1e9d8f7c6b5a4e3d2c1",
	"github.com/dagger/dagger/.dagger@local",
}

func (w wireSpans) ExportSpans(ctx context.Context, exp sdktrace.SpanExporter) error {
	return w.Export(ctx, wireExporter{w, exp}, 512, nil)
}

type wireExporter struct {
	w   wireSpans
	exp sdktrace.SpanExporter
}

func (e wireExporter) Shutdown(context.Context) error { return nil }

func (e wireExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	enriched := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, span := range spans {
		enriched[i] = e.w.enrich(span)
	}
	req := &coltracepb.ExportTraceServiceRequest{ResourceSpans: telemetry.SpansToPB(enriched)}
	payload, err := proto.Marshal(req)
	if err != nil {
		return err
	}
	var decoded coltracepb.ExportTraceServiceRequest
	if err := proto.Unmarshal(payload, &decoded); err != nil {
		return err
	}
	return e.exp.ExportSpans(ctx, telemetry.SpansFromPB(decoded.GetResourceSpans()))
}

// enrich adds the engine's call attributes to a dagql call span of the
// footprint trace.
func (w wireSpans) enrich(span sdktrace.ReadOnlySpan) sdktrace.ReadOnlySpan {
	stub := tracetest.SpanStubFromReadOnlySpan(span)
	var digest, payload string
	for _, kv := range stub.Attributes {
		switch string(kv.Key) {
		case telemetry.DagDigestAttr:
			digest = kv.Value.AsString()
		case telemetry.DagCallAttr:
			payload = kv.Value.AsString()
		}
	}
	if payload == "" {
		return span
	}
	var frame callpbv1.Call
	if err := frame.Decode(payload); err != nil {
		panic(err)
	}
	// The call's inputs: its receiver and an ID argument, which the frame
	// references too.
	arg := fmt.Sprintf("xxh3:%016x", stub.SpanContext.SpanID()[7]%64)
	frame.Args = append(frame.Args, &callpbv1.Argument{
		Name:  "directory",
		Value: &callpbv1.Literal{Value: &callpbv1.Literal_CallDigest{CallDigest: arg}},
	})
	encoded, err := frame.Encode()
	if err != nil {
		panic(err)
	}
	attrs := make([]attribute.KeyValue, 0, len(stub.Attributes)+8)
	for _, kv := range stub.Attributes {
		if string(kv.Key) == telemetry.DagCallAttr {
			kv = attribute.String(telemetry.DagCallAttr, encoded)
		}
		if string(kv.Key) == telemetry.DagOutputAttr {
			// An object result's recipe is the call's own.
			kv = attribute.String(telemetry.DagOutputAttr, digest)
		}
		attrs = append(attrs, kv)
	}
	attrs = append(attrs, attribute.StringSlice(telemetry.DagInputsAttr, []string{frame.ReceiverDigest, arg}))
	if id := stub.SpanContext.SpanID(); id[7]%5 == 0 {
		mod := retentionModules[int(id[6])%len(retentionModules)]
		attrs = append(attrs,
			attribute.String(telemetry.ModuleRefAttr, mod),
			attribute.String(telemetry.ModuleFunctionCallNameAttr, "Go.build"),
			attribute.String(telemetry.ModuleCallerRefAttr, retentionModules[3]),
			attribute.String(telemetry.ModuleCallerFunctionCallNameAttr, "Dagger.check"),
		)
	}
	stub.Attributes = attrs
	return stub.Snapshot()
}

// BenchmarkSpanAttrRetention reports the heap a DB retains per span after
// ingesting the footprint trace as the TUI does, off OTLP protobuf, with the
// engine's call attributes:
//
//	go test ./dagql/dagui -run '^$' -bench SpanAttrRetention -benchtime 1x
//
// For a breakdown, add -memprofile and read it with go tool pprof
// -sample_index=inuse_space.
func BenchmarkSpanAttrRetention(b *testing.B) {
	const n = 100_000
	var bytesPerSpan, objsPerSpan float64
	for b.Loop() {
		w := wireSpans{spanfootprint.New(n)}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		db := NewDB()
		if err := w.ExportSpans(context.Background(), db); err != nil {
			b.Fatal(err)
		}
		runtime.GC()
		runtime.ReadMemStats(&after)
		if len(db.Spans.Order) != n {
			b.Fatalf("ingested %d spans, want %d", len(db.Spans.Order), n)
		}
		footprintDB = db
		bytesPerSpan = float64(int64(after.HeapAlloc)-int64(before.HeapAlloc)) / n
		objsPerSpan = float64(int64(after.HeapObjects)-int64(before.HeapObjects)) / n
	}
	b.ReportMetric(bytesPerSpan, "B/span")
	b.ReportMetric(objsPerSpan, "objs/span")
}
