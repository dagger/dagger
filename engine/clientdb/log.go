package clientdb

import (
	"log/slog"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkresource "go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/trace"
	otlpcommonv1 "go.opentelemetry.io/proto/otlp/common/v1"
	otlplogsv1 "go.opentelemetry.io/proto/otlp/logs/v1"
	otlpresourcev1 "go.opentelemetry.io/proto/otlp/resource/v1"
	"google.golang.org/protobuf/proto"
)

// LogsToPB converts log rows to OTLP, grouped by resource and
// instrumentation scope. Each distinct resource and scope is decoded once per
// call, as in SpansToPB.
func LogsToPB(dbLog []Log) []*otlplogsv1.ResourceLogs {
	if len(dbLog) == 0 {
		return nil
	}

	rsm := make(map[attribute.Distinct]*otlplogsv1.ResourceLogs)

	type key struct {
		r  attribute.Distinct
		is instrumentation.Scope
	}
	ssm := make(map[key]*otlplogsv1.ScopeLogs)

	type resourceKey struct {
		resource, schemaURL string
	}
	type decodedResource struct {
		res *sdkresource.Resource
		pb  *otlpresourcev1.Resource
		err error
	}
	resourceCache := map[resourceKey]decodedResource{}
	type decodedScope struct {
		scope instrumentation.Scope
		pb    *otlpcommonv1.InstrumentationScope
		err   error
	}
	scopeCache := map[string]decodedScope{}

	var resources int
	for _, sd := range dbLog {
		rk := resourceKey{string(sd.Resource), sd.ResourceSchemaURL}
		decodedRes, ok := resourceCache[rk]
		if !ok {
			decodedRes.pb = &otlpresourcev1.Resource{}
			if decodedRes.err = proto.Unmarshal(sd.Resource, decodedRes.pb); decodedRes.err == nil {
				decodedRes.res = telemetry.ResourceFromPB(sd.ResourceSchemaURL, decodedRes.pb)
			}
			resourceCache[rk] = decodedRes
		}
		if decodedRes.err != nil {
			slog.Error("failed to unmarshal log resource", "error", decodedRes.err, "log", sd)
			continue
		}
		res := decodedRes.res
		decodedSc, ok := scopeCache[string(sd.InstrumentationScope)]
		if !ok {
			decodedSc.pb = &otlpcommonv1.InstrumentationScope{}
			if decodedSc.err = proto.Unmarshal(sd.InstrumentationScope, decodedSc.pb); decodedSc.err == nil {
				decodedSc.scope = telemetry.InstrumentationScopeFromPB(decodedSc.pb)
			}
			scopeCache[string(sd.InstrumentationScope)] = decodedSc
		}
		if decodedSc.err != nil {
			slog.Error("failed to unmarshal instrumentation scope", "error", decodedSc.err, "log", sd)
			continue
		}
		scope := decodedSc.scope
		rKey := res.Equivalent()
		k := key{
			r:  rKey,
			is: scope,
		}
		scopeLog, iOk := ssm[k]
		if !iOk {
			// Either the resource or instrumentation scope were unknown.
			scopeLog = &otlplogsv1.ScopeLogs{
				Scope:      proto.CloneOf(decodedSc.pb),
				LogRecords: []*otlplogsv1.LogRecord{},
				SchemaUrl:  scope.SchemaURL,
			}
		}
		var bodyPb otlpcommonv1.AnyValue
		if err := proto.Unmarshal(sd.Body, &bodyPb); err != nil {
			slog.Warn("failed to unmarshal log body", "error", err, "log", sd)
			continue
		}
		var attrs []*otlpcommonv1.KeyValue
		if err := UnmarshalProtoList(sd.Attributes, &otlpcommonv1.KeyValue{}, &attrs); err != nil {
			slog.Warn("failed to unmarshal log attributes", "error", err)
			continue
		}
		if !sd.TraceID.Valid || !sd.SpanID.Valid {
			slog.Error("log record has invalid trace or span ID", "log", sd)
			continue
		}
		tid, err := trace.TraceIDFromHex(sd.TraceID.String)
		if err != nil {
			slog.Error("failed to unmarshal trace id", "error", err)
			continue
		}
		sid, err := trace.SpanIDFromHex(sd.SpanID.String)
		if err != nil {
			slog.Error("failed to unmarshal span id", "error", err)
			continue
		}
		scopeLog.LogRecords = append(scopeLog.LogRecords, &otlplogsv1.LogRecord{
			TimeUnixNano:   uint64(sd.Timestamp),
			SeverityNumber: otlplogsv1.SeverityNumber(sd.SeverityNumber),
			SeverityText:   sd.SeverityText,
			Body:           &bodyPb,
			Attributes:     attrs,
			TraceId:        tid[:],
			SpanId:         sid[:],
		})
		ssm[k] = scopeLog

		rs, rOk := rsm[rKey]
		if !rOk {
			resources++
			// The resource was unknown.
			rs = &otlplogsv1.ResourceLogs{
				Resource:  decodedRes.pb,
				ScopeLogs: []*otlplogsv1.ScopeLogs{scopeLog},
				SchemaUrl: res.SchemaURL(),
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
	rss := make([]*otlplogsv1.ResourceLogs, 0, resources)
	for _, rs := range rsm {
		rss = append(rss, rs)
	}
	return rss
}
