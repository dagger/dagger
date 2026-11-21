package spanfootprint

import (
	"context"
	"encoding/binary"
	"slices"
	"strconv"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
)

// WaitChains passes the trace on to next with a wait link on an earlier
// span on 6% of the spans that ran 10ms or more, as the engine's always-on
// wait edges add them.
func (tr *Trace) WaitChains(next sdktrace.SpanExporter) sdktrace.SpanExporter {
	return waitChains{tr, next}
}

type waitChains struct {
	tr   *Trace
	next sdktrace.SpanExporter
}

func (e waitChains) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		out[i] = s
		id := s.SpanContext().SpanID()
		n := int(binary.BigEndian.Uint64(id[:])) - 1
		h := uint64(n)*0x9e3779b97f4a7c15 ^ 0xabcdef
		h ^= h >> 29
		dur := s.EndTime().Sub(s.StartTime())
		if n <= LongLived || h%100 >= 6 || s.EndTime().Before(s.StartTime()) || dur < 10*time.Millisecond {
			continue
		}
		start, end := s.StartTime().Add(dur/10), s.StartTime().Add(dur*6/10)
		stub := tracetest.SpanStubFromReadOnlySpan(s)
		stub.Links = append(slices.Clip(stub.Links), sdktrace.Link{
			SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: s.SpanContext().TraceID(), SpanID: e.tr.SpanID(int(h>>8) % n)}),
			Attributes: []attribute.KeyValue{
				attribute.String(telemetry.LinkPurposeAttr, telemetryattrs.LinkPurposeWait),
				attribute.String(telemetryattrs.WcprofWaitReasonAttr, "singleflight"),
				attribute.String(telemetryattrs.WcprofWaitStartUnixNanoAttr, strconv.FormatInt(start.UnixNano(), 10)),
				attribute.String(telemetryattrs.WcprofWaitEndUnixNanoAttr, strconv.FormatInt(end.UnixNano(), 10)),
			},
		})
		out[i] = stub.Snapshot()
	}
	return e.next.ExportSpans(ctx, out)
}

func (e waitChains) Shutdown(context.Context) error { return nil }

// Nested passes a trace on to next in the nested-engine shape: every span
// beneath an agent loop is a tool call (a boundary, rendered collapsed), as
// tool calls running nested engines are, and the call trees beneath them
// are the nested engines' work.
func Nested(next sdktrace.SpanExporter) sdktrace.SpanExporter {
	return nested{next}
}

type nested struct{ next sdktrace.SpanExporter }

func (e nested) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	for i, s := range spans {
		out[i] = s
		p := s.Parent().SpanID()
		if !p.IsValid() {
			continue
		}
		if pi := binary.BigEndian.Uint64(p[:]) - 1; pi == 0 || pi >= LongLived {
			continue
		}
		stub := tracetest.SpanStubFromReadOnlySpan(s)
		stub.Attributes = append(slices.Clip(stub.Attributes),
			attribute.Bool(telemetry.UIBoundaryAttr, true),
			attribute.String(telemetry.LLMToolAttr, "run_tests"),
			attribute.String(telemetry.LLMRoleAttr, "assistant"))
		out[i] = stub.Snapshot()
	}
	return e.next.ExportSpans(ctx, out)
}

func (e nested) Shutdown(context.Context) error { return nil }

// SpanIndex is the index of span id in the trace, as SpanID assigns them.
func SpanIndex(id trace.SpanID) int {
	return int(binary.BigEndian.Uint64(id[:])) - 1
}
