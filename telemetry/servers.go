package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/moby/buildkit/util/tracing/transform"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	colllogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colltracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"

	"github.com/dagger/dagger/telemetry/sdklog"
	logtransform "github.com/dagger/dagger/telemetry/sdklog/otlploghttp/transform"
	"github.com/dagger/dagger/tracing"
)

type Flusher struct {
	PubSub *PubSub

	*UnimplementedFlusherServer
}

func (f *Flusher) Flush(ctx context.Context, req *TelemetryRequest) (*FlushResponse, error) {
	tracing.FlushLiveProcessors(ctx)
	f.PubSub.Drain(trace.TraceID(req.GetTraceId()))
	return &FlushResponse{}, nil
}

type TraceServer struct {
	PubSub *PubSub

	*colltracev1.UnimplementedTraceServiceServer
	*UnimplementedTracesSourceServer
}

func (e *TraceServer) Export(ctx context.Context, req *colltracev1.ExportTraceServiceRequest) (*colltracev1.ExportTraceServiceResponse, error) {
	err := e.PubSub.ExportSpans(ctx, transform.Spans(req.GetResourceSpans()))
	if err != nil {
		return nil, err
	}
	return &colltracev1.ExportTraceServiceResponse{}, nil
}

func (e *TraceServer) Subscribe(req *TelemetryRequest, srv TracesSource_SubscribeServer) error {
	exp, err := otlptrace.New(srv.Context(), &traceStreamExporter{stream: srv})
	if err != nil {
		return err
	}
	return e.PubSub.SubscribeToSpans(srv.Context(), trace.TraceID(req.TraceId), exp)
}

type traceStreamExporter struct {
	stream TracesSource_SubscribeServer
}

var _ otlptrace.Client = (*traceStreamExporter)(nil)

func (s *traceStreamExporter) Start(ctx context.Context) error {
	return nil
}

func (s *traceStreamExporter) Stop(ctx context.Context) error {
	return nil
}

func (s *traceStreamExporter) UploadTraces(ctx context.Context, spans []*otlptracev1.ResourceSpans) error {
	return s.stream.Send(&otlptracev1.TracesData{
		ResourceSpans: spans,
	})
}

type LogsServer struct {
	PubSub *PubSub

	*colllogsv1.UnimplementedLogsServiceServer
	*UnimplementedLogsSourceServer
}

func (e *LogsServer) Export(ctx context.Context, req *colllogsv1.ExportLogsServiceRequest) (*colllogsv1.ExportLogsServiceResponse, error) {
	err := e.PubSub.ExportLogs(ctx, TransformPBLogs(req.GetResourceLogs()))
	if err != nil {
		return nil, err
	}
	return &colllogsv1.ExportLogsServiceResponse{}, nil
}

func (e *LogsServer) Subscribe(req *TelemetryRequest, stream LogsSource_SubscribeServer) error {
	return e.PubSub.SubscribeToLogs(stream.Context(), trace.TraceID(req.TraceId), &logStreamExporter{
		stream: stream,
	})
}

type logStreamExporter struct {
	stream LogsSource_SubscribeServer
}

var _ sdklog.LogExporter = (*logStreamExporter)(nil)

func (s *logStreamExporter) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	return s.stream.Send(&otlplogsv1.LogsData{
		ResourceLogs: logtransform.Logs(logs),
	})
}

func (s *logStreamExporter) Shutdown(ctx context.Context) error {
	return nil
}

func TransformPBLogs(resLogs []*otlplogsv1.ResourceLogs) []*sdklog.LogData {
	logs := []*sdklog.LogData{}
	for _, rl := range resLogs {
		attrs := rl.GetResource().GetAttributes()
		attrKV := make([]attribute.KeyValue, 0, len(attrs))
		for _, kv := range rl.GetResource().GetAttributes() {
			attrKV = append(attrKV, attribute.String(kv.GetKey(), kv.GetValue().GetStringValue()))
		}
		res := resource.NewWithAttributes(rl.GetSchemaUrl(), attrKVs(rl.GetResource().GetAttributes())...)
		for _, scopeLog := range rl.GetScopeLogs() {
			scope := scopeLog.GetScope()
			for _, rec := range scopeLog.GetLogRecords() {
				var logRec log.Record
				// spare me my life!
				// spare me my life!
				logRec.SetTimestamp(time.Unix(0, int64(rec.GetTimeUnixNano())))
				logRec.SetBody(logValue(rec.GetBody()))
				logRec.AddAttributes(logKVs(rec.GetAttributes())...)
				logRec.SetSeverity(log.Severity(rec.GetSeverityNumber()))
				logRec.SetSeverityText(rec.GetSeverityText())
				logRec.SetObservedTimestamp(time.Unix(0, int64(rec.GetObservedTimeUnixNano())))
				logs = append(logs, &sdklog.LogData{
					Record:   logRec,
					Resource: res,
					InstrumentationScope: instrumentation.Scope{
						Name:      scope.GetName(),
						Version:   scope.GetVersion(),
						SchemaURL: scopeLog.GetSchemaUrl(),
					},
					TraceID: trace.TraceID(rec.GetTraceId()),
					SpanID:  trace.SpanID(rec.GetSpanId()),
				})
			}
		}
	}
	return logs
}

func logKVs(kvs []*otlpcommonv1.KeyValue) []log.KeyValue {
	res := make([]log.KeyValue, len(kvs))
	for i, kv := range kvs {
		res[i] = logKeyValue(kv)
	}
	return res
}

func logKeyValue(v *otlpcommonv1.KeyValue) log.KeyValue {
	return log.KeyValue{
		Key:   v.GetKey(),
		Value: logValue(v.GetValue()),
	}
}

func attrKVs(kvs []*otlpcommonv1.KeyValue) []attribute.KeyValue {
	res := make([]attribute.KeyValue, len(kvs))
	for i, kv := range kvs {
		res[i] = attrKeyValue(kv)
	}
	return res
}

func attrKeyValue(v *otlpcommonv1.KeyValue) attribute.KeyValue {
	return attribute.KeyValue{
		Key:   attribute.Key(v.GetKey()),
		Value: attrValue(v.GetValue()),
	}
}

func attrValue(v *otlpcommonv1.AnyValue) attribute.Value {
	switch x := v.Value.(type) {
	case *otlpcommonv1.AnyValue_StringValue:
		return attribute.StringValue(v.GetStringValue())
	case *otlpcommonv1.AnyValue_DoubleValue:
		return attribute.Float64Value(v.GetDoubleValue())
	case *otlpcommonv1.AnyValue_IntValue:
		return attribute.Int64Value(v.GetIntValue())
	case *otlpcommonv1.AnyValue_BoolValue:
		return attribute.BoolValue(v.GetBoolValue())
	default:
		// TODO slices, bleh
		return attribute.StringValue(fmt.Sprintf("UNHANDLED ATTR TYPE: %v", x))
	}
}

func logValue(v *otlpcommonv1.AnyValue) log.Value {
	switch x := v.Value.(type) {
	case *otlpcommonv1.AnyValue_StringValue:
		return log.StringValue(v.GetStringValue())
	case *otlpcommonv1.AnyValue_DoubleValue:
		return log.Float64Value(v.GetDoubleValue())
	case *otlpcommonv1.AnyValue_IntValue:
		return log.Int64Value(v.GetIntValue())
	case *otlpcommonv1.AnyValue_BoolValue:
		return log.BoolValue(v.GetBoolValue())
	case *otlpcommonv1.AnyValue_KvlistValue:
		kvs := make([]log.KeyValue, len(x.KvlistValue.GetValues()))
		for _, kv := range x.KvlistValue.GetValues() {
			kvs = append(kvs, logKeyValue(kv))
		}
		return log.MapValue(kvs...)
	case *otlpcommonv1.AnyValue_ArrayValue:
		vals := make([]log.Value, len(x.ArrayValue.GetValues()))
		for _, v := range x.ArrayValue.GetValues() {
			vals = append(vals, logValue(v))
		}
		return log.SliceValue(vals...)
	case *otlpcommonv1.AnyValue_BytesValue:
		return log.BytesValue(x.BytesValue)
	default:
		panic(fmt.Sprintf("unknown value type: %T", x))
	}
}
