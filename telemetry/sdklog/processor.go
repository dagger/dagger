package sdklog

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
)

type LogData struct {
	log.Record

	Resource             *resource.Resource
	InstrumentationScope instrumentation.Scope

	TraceID trace.TraceID
	SpanID  trace.SpanID
}

type LogProcessor interface {
	OnEmit(context.Context, *LogData)
	Shutdown(context.Context) error
}

var _ LogProcessor = &simpleLogProcessor{}

type simpleLogProcessor struct {
	exporter LogExporter
}

func NewSimpleLogProcessor(exporter LogExporter) LogProcessor {
	return &simpleLogProcessor{
		exporter: exporter,
	}
}

func (p *simpleLogProcessor) OnEmit(ctx context.Context, log *LogData) {
	if err := p.exporter.ExportLogs(ctx, []*LogData{log}); err != nil {
		otel.Handle(err)
	}
}

func (p *simpleLogProcessor) Shutdown(ctx context.Context) error {
	return nil
}
