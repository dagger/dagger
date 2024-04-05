package transform

import (
	"github.com/dagger/dagger/telemetry/sdklog"
	"go.opentelemetry.io/otel/attribute"
	olog "go.opentelemetry.io/otel/log"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
)

// Spans transforms a slice of OpenTelemetry spans into a slice of OTLP
// ResourceSpans.
func Logs(sdl []*sdklog.LogData) []*logspb.ResourceLogs {
	if len(sdl) == 0 {
		return nil
	}

	rsm := make(map[attribute.Distinct]*logspb.ResourceLogs)

	type key struct {
		r  attribute.Distinct
		is instrumentation.Scope
	}
	ssm := make(map[key]*logspb.ScopeLogs)

	var resources int
	for _, sd := range sdl {
		if sd == nil {
			continue
		}

		rKey := sd.Resource.Equivalent()
		k := key{
			r:  rKey,
			is: sd.InstrumentationScope,
		}
		scopeLog, iOk := ssm[k]
		if !iOk {
			// Either the resource or instrumentation scope were unknown.
			scopeLog = &logspb.ScopeLogs{
				Scope:      InstrumentationScope(sd.InstrumentationScope),
				LogRecords: []*logspb.LogRecord{},
				SchemaUrl:  sd.InstrumentationScope.SchemaURL,
			}
		}
		scopeLog.LogRecords = append(scopeLog.LogRecords, logRecord(sd))
		ssm[k] = scopeLog

		rs, rOk := rsm[rKey]
		if !rOk {
			resources++
			// The resource was unknown.
			rs = &logspb.ResourceLogs{
				Resource:  Resource(sd.Resource),
				ScopeLogs: []*logspb.ScopeLogs{scopeLog},
			}
			if sd.Resource != nil {
				rs.SchemaUrl = sd.Resource.SchemaURL()
			}
			rsm[rKey] = rs
			continue
		}

		// The resource has been seen before. Check if the instrumentation
		// library lookup was unknown because if so we need to add it to the
		// ResourceSpans. Otherwise, the instrumentation library has already
		// been seen and the append we did above will be included it in the
		// ScopeSpans reference.
		if !iOk {
			rs.ScopeLogs = append(rs.ScopeLogs, scopeLog)
		}
	}

	// Transform the categorized map into a slice
	rss := make([]*logspb.ResourceLogs, 0, resources)
	for _, rs := range rsm {
		rss = append(rss, rs)
	}
	return rss
}

func InstrumentationScope(il instrumentation.Scope) *commonpb.InstrumentationScope {
	if il == (instrumentation.Scope{}) {
		return nil
	}
	return &commonpb.InstrumentationScope{
		Name:    il.Name,
		Version: il.Version,
	}
}

// Value transforms an attribute Value into an OTLP AnyValue.
func logValue(v olog.Value) *commonpb.AnyValue {
	av := new(commonpb.AnyValue)
	switch v.Kind() {
	case olog.KindBool:
		av.Value = &commonpb.AnyValue_BoolValue{
			BoolValue: v.AsBool(),
		}
	case olog.KindInt64:
		av.Value = &commonpb.AnyValue_IntValue{
			IntValue: v.AsInt64(),
		}
	case olog.KindFloat64:
		av.Value = &commonpb.AnyValue_DoubleValue{
			DoubleValue: v.AsFloat64(),
		}
	case olog.KindString:
		av.Value = &commonpb.AnyValue_StringValue{
			StringValue: v.AsString(),
		}
	case olog.KindSlice:
		array := &commonpb.ArrayValue{}
		for _, e := range v.AsSlice() {
			array.Values = append(array.Values, logValue(e))
		}
		av.Value = &commonpb.AnyValue_ArrayValue{
			ArrayValue: array,
		}
	case olog.KindMap:
		kvList := &commonpb.KeyValueList{}
		for _, e := range v.AsMap() {
			kvList.Values = append(kvList.Values, &commonpb.KeyValue{
				Key:   e.Key,
				Value: logValue(e.Value),
			})
		}
		av.Value = &commonpb.AnyValue_KvlistValue{
			KvlistValue: kvList,
		}
	default:
		av.Value = &commonpb.AnyValue_StringValue{
			StringValue: "INVALID",
		}
	}
	return av
}

// span transforms a Span into an OTLP span.
func logRecord(l *sdklog.LogData) *logspb.LogRecord {
	if l == nil {
		return nil
	}

	attrs := []*commonpb.KeyValue{}
	l.WalkAttributes(func(kv olog.KeyValue) bool {
		attrs = append(attrs, &commonpb.KeyValue{
			Key:   kv.Key,
			Value: logValue(kv.Value),
		})
		return true
	})

	s := &logspb.LogRecord{
		TimeUnixNano:   uint64(l.Timestamp().UnixNano()),
		SeverityNumber: logspb.SeverityNumber(l.Severity()),
		SeverityText:   l.SeverityText(),
		Body:           logValue(l.Body()),
		Attributes:     attrs,
		// DroppedAttributesCount: 0,
		// Flags: 0,
		TraceId: l.TraceID[:],
		SpanId:  l.SpanID[:],
	}

	return s
}
