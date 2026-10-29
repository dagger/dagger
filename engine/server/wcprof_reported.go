package server

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
	"github.com/dagger/dagger/engine/wcprof/contract"
)

// recordReportedSpans records, for wcprof's native dump, the spans in a batch
// that clients and programs inside execs report under the wcprof reporting
// contract (engine/wcprof/contract). Engine spans are recorded natively where
// they happen and are skipped here.
func recordReportedSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) {
	for _, span := range spans {
		if !reportsToWcprof(span) {
			continue
		}
		cs := contractSpan(span)
		rep, err := contract.Decode(cs)
		if err != nil {
			slog.Warn("dropping malformed wcprof-reported span", "span", cs.SpanID, "name", span.Name(), "err", err)
			continue
		}
		if c := rep.Complete; c != nil {
			wcprof.RecordProducerComplete(ctx, c.Producer, c.SpanCount, c.Dropped)
			continue
		}
		if op, ok := contract.Record(cs, span.Name(), rep); ok {
			wcprof.RecordReported(ctx, op)
		}
	}
}

// reportsToWcprof is a cheap check for a reported span: a reported op kind, a
// producer's ID or a producer's completeness carrier, and not an engine span.
func reportsToWcprof(span sdktrace.ReadOnlySpan) bool {
	reported := false
	for _, kv := range span.Attributes() {
		switch kv.Key {
		case telemetryattrs.WcprofEngineSpanAttr:
			return false
		case telemetryattrs.WcprofOpKindAttr:
			reported = reported || contract.IsReportedKind(kv.Value.AsString())
		case telemetryattrs.WcprofProducerAttr, telemetryattrs.WcprofProducerCompleteAttr:
			reported = true
		}
	}
	return reported
}

// contractSpan is the contract's view of span.
func contractSpan(span sdktrace.ReadOnlySpan) contract.Span {
	sc := span.SpanContext()
	cs := contract.Span{
		TraceID:     sc.TraceID().String(),
		SpanID:      sc.SpanID().String(),
		StartUnixNS: span.StartTime().UnixNano(),
		Attrs:       attrMap(span.Attributes()),
	}
	if p := span.Parent(); p.IsValid() {
		cs.ParentSpanID = p.SpanID().String()
	}
	// A live snapshot's unset end can arrive as a time before its start
	// (protobuf round-trips the zero time to one): still running.
	if end := span.EndTime(); !end.IsZero() && !end.Before(span.StartTime()) {
		cs.EndUnixNS = end.UnixNano()
	}
	for _, l := range span.Links() {
		cs.Links = append(cs.Links, contract.Link{
			TraceID: l.SpanContext.TraceID().String(),
			SpanID:  l.SpanContext.SpanID().String(),
			Attrs:   attrMap(l.Attributes),
		})
	}
	return cs
}

func attrMap(kvs []attribute.KeyValue) map[string]any {
	m := make(map[string]any, len(kvs))
	for _, kv := range kvs {
		m[string(kv.Key)] = kv.Value.AsInterface()
	}
	return m
}
