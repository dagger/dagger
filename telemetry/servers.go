package telemetry

import (
	"context"
	"fmt"
	"time"

	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/moby/buildkit/util/tracing/transform"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	logsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	tracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type TraceServer struct {
	*tracev1.UnimplementedTraceServiceServer

	Exporter sdktrace.SpanExporter
}

func (e *TraceServer) Export(ctx context.Context, req *tracev1.ExportTraceServiceRequest) (*tracev1.ExportTraceServiceResponse, error) {
	if e.Exporter == nil {
		return nil, status.Errorf(codes.Unavailable, "trace collector not configured")
	}
	err := e.Exporter.ExportSpans(ctx, transform.Spans(req.GetResourceSpans()))
	if err != nil {
		return nil, err
	}
	return &tracev1.ExportTraceServiceResponse{}, nil
}

type LogsServer struct {
	*logsv1.UnimplementedLogsServiceServer

	Exporter sdklog.LogExporter
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

func (e *LogsServer) Export(ctx context.Context, req *logsv1.ExportLogsServiceRequest) (*logsv1.ExportLogsServiceResponse, error) {
	if e.Exporter == nil {
		return nil, status.Errorf(codes.Unavailable, "log collector not configured")
	}
	err := e.Exporter.ExportLogs(ctx, TransformPBLogs(req.GetResourceLogs()))
	if err != nil {
		return nil, err
	}
	return &logsv1.ExportLogsServiceResponse{}, nil
}

func logKVs(kvs []*commonv1.KeyValue) []log.KeyValue {
	res := make([]log.KeyValue, len(kvs))
	for i, kv := range kvs {
		res[i] = logKeyValue(kv)
	}
	return res
}

func logKeyValue(v *commonv1.KeyValue) log.KeyValue {
	return log.KeyValue{
		Key:   v.GetKey(),
		Value: logValue(v.GetValue()),
	}
}

func attrKVs(kvs []*commonv1.KeyValue) []attribute.KeyValue {
	res := make([]attribute.KeyValue, len(kvs))
	for i, kv := range kvs {
		res[i] = attrKeyValue(kv)
	}
	return res
}

func attrKeyValue(v *commonv1.KeyValue) attribute.KeyValue {
	return attribute.KeyValue{
		Key:   attribute.Key(v.GetKey()),
		Value: attrValue(v.GetValue()),
	}
}

func attrValue(v *commonv1.AnyValue) attribute.Value {
	switch x := v.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return attribute.StringValue(v.GetStringValue())
	case *commonv1.AnyValue_DoubleValue:
		return attribute.Float64Value(v.GetDoubleValue())
	case *commonv1.AnyValue_IntValue:
		return attribute.Int64Value(v.GetIntValue())
	case *commonv1.AnyValue_BoolValue:
		return attribute.BoolValue(v.GetBoolValue())
	default:
		// TODO slices, bleh
		return attribute.StringValue(fmt.Sprintf("UNHANDLED ATTR TYPE: %v", x))
	}
}

func logValue(v *commonv1.AnyValue) log.Value {
	switch x := v.Value.(type) {
	case *commonv1.AnyValue_StringValue:
		return log.StringValue(v.GetStringValue())
	case *commonv1.AnyValue_DoubleValue:
		return log.Float64Value(v.GetDoubleValue())
	case *commonv1.AnyValue_IntValue:
		return log.Int64Value(v.GetIntValue())
	case *commonv1.AnyValue_BoolValue:
		return log.BoolValue(v.GetBoolValue())
	case *commonv1.AnyValue_KvlistValue:
		kvs := make([]log.KeyValue, len(x.KvlistValue.GetValues()))
		for _, kv := range x.KvlistValue.GetValues() {
			kvs = append(kvs, logKeyValue(kv))
		}
		return log.MapValue(kvs...)
	case *commonv1.AnyValue_ArrayValue:
		vals := make([]log.Value, len(x.ArrayValue.GetValues()))
		for _, v := range x.ArrayValue.GetValues() {
			vals = append(vals, logValue(v))
		}
		return log.SliceValue(vals...)
	case *commonv1.AnyValue_BytesValue:
		return log.BytesValue(x.BytesValue)
	default:
		panic(fmt.Sprintf("unknown value type: %T", x))
	}
}
