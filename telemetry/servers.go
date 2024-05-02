package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	colllogsv1 "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	collmetricsv1 "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	colltracev1 "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlptracev1 "go.opentelemetry.io/proto/otlp/trace/v1"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"

	"github.com/dagger/dagger/engine/slog"
	"github.com/dagger/dagger/telemetry/sdklog"
	logtransform "github.com/dagger/dagger/telemetry/sdklog/otlploghttp/transform"
)

type TraceServer struct {
	PubSub *PubSub

	*colltracev1.UnimplementedTraceServiceServer
	*UnimplementedTracesSourceServer
}

func (e *TraceServer) Export(ctx context.Context, req *colltracev1.ExportTraceServiceRequest) (*colltracev1.ExportTraceServiceResponse, error) {
	err := e.PubSub.ExportSpans(ctx, SpansFromProto(req.GetResourceSpans()))
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

type MetricsServer struct {
	PubSub *PubSub

	*collmetricsv1.UnimplementedMetricsServiceServer
	*UnimplementedMetricsSourceServer
}

func (e *MetricsServer) Export(ctx context.Context, req *collmetricsv1.ExportMetricsServiceRequest) (*collmetricsv1.ExportMetricsServiceResponse, error) {
	// TODO
	slog.Warn("MetricsServer.Export ignoring export (TODO)")
	return &collmetricsv1.ExportMetricsServiceResponse{}, nil
}

func (e *MetricsServer) Subscribe(req *TelemetryRequest, srv MetricsSource_SubscribeServer) error {
	return status.Errorf(codes.Unimplemented, "Subscribe not implemented")
}

func TransformPBLogs(resLogs []*otlplogsv1.ResourceLogs) []*sdklog.LogData {
	logs := []*sdklog.LogData{}
	for _, rl := range resLogs {
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
	case *otlpcommonv1.AnyValue_ArrayValue:
		vals := make([]attribute.Value, 0, len(x.ArrayValue.GetValues()))
		types := map[attribute.Type]int{}
		for _, v := range x.ArrayValue.GetValues() {
			val := attrValue(v)
			types[val.Type()]++
			vals = append(vals, val)
		}
		switch len(types) {
		case 0:
			slog.Error("otlpcommonv1.AnyValue -> attribute.Value: empty array; assuming string slice")
			return attribute.StringSliceValue(nil)
		case 1:
			for t := range types {
				switch t {
				case attribute.STRING:
					strs := make([]string, 0, len(vals))
					for _, v := range vals {
						strs = append(strs, v.AsString())
					}
					return attribute.StringSliceValue(strs)
				case attribute.INT64:
					ints := make([]int64, 0, len(vals))
					for _, v := range vals {
						ints = append(ints, v.AsInt64())
					}
					return attribute.Int64SliceValue(ints)
				case attribute.FLOAT64:
					floats := make([]float64, 0, len(vals))
					for _, v := range vals {
						floats = append(floats, v.AsFloat64())
					}
					return attribute.Float64SliceValue(floats)
				case attribute.BOOL:
					bools := make([]bool, 0, len(vals))
					for _, v := range vals {
						bools = append(bools, v.AsBool())
					}
					return attribute.BoolSliceValue(bools)
				default:
					slog.Error("otlpcommonv1.AnyValue -> attribute.Value: unhandled array value type conversion", "type", fmt.Sprintf("%T", x))
					return attribute.StringValue(fmt.Sprintf("UNHANDLED ARRAY ELEM TYPE: %+v (%s)", vals, t))
				}
			}
			panic("unreachable")
		default:
			slog.Error("otlpcommonv1.AnyValue -> attribute.Value: mixed types in array attribute", "types", fmt.Sprintf("%v", types))
			return attribute.StringValue(fmt.Sprintf("%v", vals))
		}
	case *otlpcommonv1.AnyValue_BytesValue:
		return attribute.StringValue(string(x.BytesValue))
	default:
		slog.Error("otlpcommonv1.AnyValue -> attribute.Value: unhandled type conversion", "type", fmt.Sprintf("%T", x))
		return attribute.StringValue(fmt.Sprintf("UNHANDLED ATTR TYPE: %v (%T)", x, x))
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
		kvs := make([]log.KeyValue, 0, len(x.KvlistValue.GetValues()))
		for _, kv := range x.KvlistValue.GetValues() {
			kvs = append(kvs, logKeyValue(kv))
		}
		return log.MapValue(kvs...)
	case *otlpcommonv1.AnyValue_ArrayValue:
		vals := make([]log.Value, 0, len(x.ArrayValue.GetValues()))
		for _, v := range x.ArrayValue.GetValues() {
			vals = append(vals, logValue(v))
		}
		return log.SliceValue(vals...)
	case *otlpcommonv1.AnyValue_BytesValue:
		return log.BytesValue(x.BytesValue)
	default:
		slog.Error("unhandled otlpcommonv1.AnyValue -> log.Value conversion", "type", fmt.Sprintf("%T", x))
		return log.StringValue(fmt.Sprintf("UNHANDLED LOG VALUE TYPE: %v (%T)", x, x))
	}
}
