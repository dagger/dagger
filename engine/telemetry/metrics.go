package telemetry

import (
	"context"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"

	"dagger.io/dagger/telemetry"
)

func ReexportMetricsFromPB(ctx context.Context, exp sdkmetric.Exporter, req *colmetricspb.ExportMetricsServiceRequest) error {
	processor := &collectLogProcessor{}

	for _, rl := range req.GetResourceMetrics() {
		resource := telemetry.ResourceFromPB(rl.GetSchemaUrl(), rl.GetResource())

		provider := sdkmetric.NewMeterProvider(
			sdkmetric.WithResource(resource),
			sdkmetric.WithProcessor(processor),
		)

		/*
			for _, scopeLog := range rl.GetScopeMetrics() {
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
		*/
	}
	return exp.Export(ctx, processor.logs)
}
