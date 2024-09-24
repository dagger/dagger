package telemetry

import (
	"context"
	"time"

	"dagger.io/dagger/telemetry"
	"go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"go.opentelemetry.io/otel/trace"
	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
)

func ReexportLogsFromPB(ctx context.Context, exp sdklog.Exporter, req *collogspb.ExportLogsServiceRequest) error {
	processor := &collectLogProcessor{}

	for _, rl := range req.GetResourceLogs() {
		resource := telemetry.ResourceFromPB(rl.GetSchemaUrl(), rl.GetResource())

		provider := sdklog.NewLoggerProvider(
			sdklog.WithResource(resource),
			sdklog.WithProcessor(processor),
		)

		for _, scopeLog := range rl.GetScopeLogs() {
			logger := provider.Logger(scopeLog.GetScope().GetName(),
				log.WithInstrumentationVersion(scopeLog.GetScope().GetVersion()),
				log.WithInstrumentationAttributes(
					telemetry.AttributesFromProto(scopeLog.GetScope().GetAttributes())...,
				),
				log.WithSchemaURL(scopeLog.GetSchemaUrl()),
			)
			for _, rec := range scopeLog.GetLogRecords() {
				var logRec log.Record
				tid := trace.TraceID(rec.GetTraceId())
				sid := trace.SpanID(rec.GetSpanId())
				emitCtx := trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
					TraceID: tid,
					SpanID:  sid,
				}))
				logRec.SetTimestamp(time.Unix(0, int64(rec.GetTimeUnixNano())))
				logRec.SetBody(telemetry.LogValueFromPB(rec.GetBody()))
				logRec.SetSeverity(log.Severity(rec.GetSeverityNumber()))
				logRec.SetSeverityText(rec.GetSeverityText())
				logRec.SetObservedTimestamp(time.Unix(0, int64(rec.GetObservedTimeUnixNano())))
				logRec.AddAttributes(telemetry.LogKeyValuesFromPB(rec.GetAttributes())...)
				logger.Emit(emitCtx, logRec)
			}
		}
	}
	return exp.Export(ctx, processor.logs)
}

type collectLogProcessor struct {
	logs []sdklog.Record
}

var _ sdklog.Processor = (*collectLogProcessor)(nil)

func (p *collectLogProcessor) OnEmit(ctx context.Context, record sdklog.Record) error {
	p.logs = append(p.logs, record)
	return nil
}

func (p *collectLogProcessor) Enabled(ctx context.Context, record sdklog.Record) bool {
	return true
}

func (p *collectLogProcessor) Shutdown(ctx context.Context) error {
	return nil
}

func (p *collectLogProcessor) ForceFlush(ctx context.Context) error {
	return nil
}
