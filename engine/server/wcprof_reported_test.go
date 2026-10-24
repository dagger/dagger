package server

import (
	"bytes"
	"context"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/dagger/dagger/engine/telemetryattrs"
	"github.com/dagger/dagger/engine/wcprof"
)

func TestRecordReportedSpans(t *testing.T) {
	sr := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sr))
	tr := tp.Tracer("test")
	ctx, group := tr.Start(context.Background(), "walk", trace.WithAttributes(
		attribute.String(telemetryattrs.WcprofOpKindAttr, "task_group"),
		attribute.String(telemetryattrs.WcprofProducerAttr, "p1")))
	_, dep := tr.Start(ctx, "package", trace.WithAttributes(
		attribute.String(telemetryattrs.WcprofOpKindAttr, "task"),
		attribute.String(telemetryattrs.WcprofLabelAttr, "example.com/dep"),
		attribute.String(telemetryattrs.WcprofProducerAttr, "p1")))
	dep.End()
	_, pkg := tr.Start(ctx, "package",
		trace.WithAttributes(
			attribute.String(telemetryattrs.WcprofOpKindAttr, "task"),
			attribute.String(telemetryattrs.WcprofLabelAttr, "example.com/a"),
			attribute.String(telemetryattrs.WcprofProducerAttr, "p1")),
		trace.WithLinks(trace.Link{SpanContext: dep.SpanContext(), Attributes: []attribute.KeyValue{
			attribute.String(telemetry.LinkPurposeAttr, telemetryattrs.LinkPurposeWait),
			attribute.String(telemetryattrs.WcprofWaitReasonAttr, "dependency"),
			attribute.String(telemetryattrs.WcprofWaitStartUnixNanoAttr, "1"),
			attribute.String(telemetryattrs.WcprofWaitEndUnixNanoAttr, "2"),
		}}))
	pkg.End()
	group.End()
	_, carrier := tr.Start(context.Background(), "complete", trace.WithAttributes(
		attribute.Bool(telemetryattrs.WcprofProducerCompleteAttr, true),
		attribute.String(telemetryattrs.WcprofProducerAttr, "p1"),
		attribute.String(telemetryattrs.WcprofProducerSpanCountAttr, "3")))
	carrier.End()
	// An engine span with a reported kind is the engine's own: skipped.
	_, eng := tr.Start(context.Background(), "engine", trace.WithAttributes(
		attribute.String(telemetryattrs.WcprofOpKindAttr, "task"),
		attribute.Bool(telemetryattrs.WcprofEngineSpanAttr, true)))
	eng.End()

	r := wcprof.NewRecorder(0)
	restore := wcprof.SetActiveForTest(r, true)
	defer restore()
	recordReportedSpans(context.Background(), sr.Ended())

	var buf bytes.Buffer
	if err := r.WriteDump(&buf, true); err != nil {
		t.Fatal(err)
	}
	header, events, err := wcprof.ReadDump(&buf)
	if err != nil {
		t.Fatal(err)
	}
	var tasks, groups, waits int
	for _, ev := range events {
		switch {
		case ev.Type == "op" && ev.OpKind == "task":
			tasks++
		case ev.Type == "op" && ev.OpKind == "task_group":
			groups++
		case ev.Type == "wait" && ev.Reason == "dependency" && ev.TargetID != 0:
			waits++
		}
	}
	if tasks != 2 || groups != 1 || waits != 1 {
		t.Errorf("tasks %d groups %d resolved dependency waits %d; events %+v", tasks, groups, waits, events)
	}
	if len(header.Producers) != 1 || header.Producers[0] != (wcprof.DumpProducer{Producer: "p1", Received: 3, Declared: 3, Complete: true}) {
		t.Errorf("producers %+v", header.Producers)
	}
}
