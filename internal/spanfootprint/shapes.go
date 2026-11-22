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

// ContainedWaits passes the trace on to next with the waits real sessions
// mostly carry, on spans beneath their carrier: a quarter of the spans with
// children wait (call_exec) on each of them, up to 64, as a glob waits on
// its matches; half of those name their first child after themselves, their
// execution twin, as a call's own execution is; a third of them end ahead
// of the children they waited on that started meanwhile.
func (tr *Trace) ContainedWaits(next sdktrace.SpanExporter) sdktrace.SpanExporter {
	children := make([][]int32, tr.N)
	for i := 1; i < tr.N; i++ {
		p := tr.parents[i]
		children[p] = append(children[p], int32(i))
	}
	return &containedWaits{tr: tr, children: children, next: next}
}

type containedWaits struct {
	tr       *Trace
	children [][]int32
	next     sdktrace.SpanExporter
}

func (e *containedWaits) carrier(i int) bool {
	return i > LongLived && len(e.children[i]) > 0 && hash(i, 7)%4 == 0
}

func (e *containedWaits) twin(i int) bool {
	return e.carrier(i) && hash(i, 8)%2 == 0
}

// name is span i's name: its twin parent's, if it's one's first child.
func (e *containedWaits) name(i int) string {
	if i > 0 {
		if p := int(e.tr.parents[i]); e.twin(p) && int(e.children[p][0]) == i {
			return e.name(p)
		}
	}
	return e.tr.Stub(i, false).Name()
}

func (e *containedWaits) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	out := make([]sdktrace.ReadOnlySpan, len(spans))
	early := make([]bool, len(spans))
	for k, s := range spans {
		out[k] = s
		i := SpanIndex(s.SpanContext().SpanID())
		done := !s.EndTime().Before(s.StartTime())
		// a third arrive ahead of the children they waited on that started
		// in the batch they end in, as the spans of another process
		// exporting later (a nested engine's) do
		early[k] = done && e.carrier(i) && hash(i, 9)%3 == 0
		renamed := i > 0 && e.twin(int(e.tr.parents[i])) && int(e.children[e.tr.parents[i]][0]) == i
		waits := done && e.carrier(i)
		if !renamed && !waits {
			continue
		}
		stub := tracetest.SpanStubFromReadOnlySpan(s)
		if renamed {
			stub.Name = e.name(i)
		}
		if waits {
			dur := s.EndTime().Sub(s.StartTime())
			start, end := s.StartTime().Add(dur/10), s.StartTime().Add(dur*6/10)
			kids := e.children[i]
			stub.Links = slices.Clip(stub.Links)
			for _, c := range kids[:min(len(kids), 64)] {
				if int(c) >= (i/512+2)*512 {
					// not started yet when the carrier ends (see Export)
					break
				}
				stub.Links = append(stub.Links, sdktrace.Link{
					SpanContext: trace.NewSpanContext(trace.SpanContextConfig{TraceID: s.SpanContext().TraceID(), SpanID: e.tr.SpanID(int(c))}),
					Attributes: []attribute.KeyValue{
						attribute.String(telemetry.LinkPurposeAttr, telemetryattrs.LinkPurposeWait),
						attribute.String(telemetryattrs.WcprofWaitReasonAttr, "call_exec"),
						attribute.String(telemetryattrs.WcprofWaitStartUnixNanoAttr, strconv.FormatInt(start.UnixNano(), 10)),
						attribute.String(telemetryattrs.WcprofWaitEndUnixNanoAttr, strconv.FormatInt(end.UnixNano(), 10)),
					},
				})
			}
		}
		out[k] = stub.Snapshot()
	}
	ordered := make([]sdktrace.ReadOnlySpan, 0, len(out))
	for k, s := range out {
		if early[k] {
			ordered = append(ordered, s)
		}
	}
	for k, s := range out {
		if !early[k] {
			ordered = append(ordered, s)
		}
	}
	return e.next.ExportSpans(ctx, ordered)
}

func (e *containedWaits) Shutdown(context.Context) error { return nil }

// SpanIndex is the index of span id in the trace, as SpanID assigns them.
func SpanIndex(id trace.SpanID) int {
	return int(binary.BigEndian.Uint64(id[:])) - 1
}
