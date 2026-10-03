package telemetry

import (
	"slices"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
)

// Normalizing OTLP this process did not produce, before otel-go converts it.
//
// DEGRADE, NEVER PANIC. Several shapes are legal OTLP — every message field
// in the protocol is optional — but are dereferenced blindly by otel-go's
// conversion (SpansFromPB, ReexportLogsFromPB, ResourceMetricsFromPB):
//
//   - a ResourceSpans/ResourceLogs/ResourceMetrics with no Resource
//     (otel.ResourceFromPB reads pb.Attributes);
//   - a log record with no Body (otel.LogValueFromPB switches on v.Value);
//   - an attribute KeyValue, array element or kvlist entry with no value
//     (otel's attrValue and LogValueFromPB, same switch);
//   - a gauge with no Gauge message (gaugeFromPB reads g.DataPoints).
//
// Any process that can reach an OTLP endpoint can send them: the engine's
// per-exec endpoint takes whatever a container's SDK posts, and one
// `tracetest.SpanStub{}.Snapshot()` span without a Resource, exported from a
// `go test` in a session container, was enough to take the whole engine down.
// Span conversion is lazy, so that panic did not even fire in the HTTP
// handler, where net/http would have contained it, but in the exporter's
// fan-out goroutine.
//
// So every boundary that decodes foreign OTLP — the engine's OTLP handlers,
// the client's stream from a scale-out engine, `dagger run`'s proxy, the
// trace importer — runs its request through these first. They fill the
// missing pieces in with their empty forms (an empty resource, an empty
// string) and drop nil list entries, so nothing that could be stored is lost.
//
// That belongs upstream, in otel-go's decode path. dagger/otel-go#17 proposed
// it but was CLOSED WITHOUT MERGING, and the otel-go main this repo pins
// (2bca4f5622cf) still dereferences these fields. Keep these guards until an
// upstream fix actually lands and the pin is bumped past it; then delete them,
// since a decode boundary that answers for its own optional fields is the real
// fix and duplicating it here only hides the next one.

// NormalizeTraceRequest makes req safe to convert with otel.SpansFromPB,
// in place.
func NormalizeTraceRequest(req *coltracepb.ExportTraceServiceRequest) {
	if req == nil {
		return
	}
	req.ResourceSpans = dropNil(req.ResourceSpans)
	for _, rs := range req.ResourceSpans {
		rs.Resource = normalizeResource(rs.Resource)
		rs.ScopeSpans = dropNil(rs.ScopeSpans)
		for _, ss := range rs.ScopeSpans {
			normalizeScope(ss.Scope)
			ss.Spans = dropNil(ss.Spans)
			for _, span := range ss.Spans {
				span.Attributes = normalizeKeyValues(span.Attributes)
				span.Events = dropNil(span.Events)
				for _, event := range span.Events {
					event.Attributes = normalizeKeyValues(event.Attributes)
				}
				span.Links = dropNil(span.Links)
				for _, link := range span.Links {
					link.Attributes = normalizeKeyValues(link.Attributes)
				}
			}
		}
	}
}

// NormalizeLogsRequest makes req safe to convert with
// otel.ReexportLogsFromPB, in place. A record with no body gets an empty
// string one: attribute-only records are legal, and common (call payload
// and agent state records ride on their attributes).
func NormalizeLogsRequest(req *collogspb.ExportLogsServiceRequest) {
	if req == nil {
		return
	}
	req.ResourceLogs = dropNil(req.ResourceLogs)
	for _, rl := range req.ResourceLogs {
		rl.Resource = normalizeResource(rl.Resource)
		rl.ScopeLogs = dropNil(rl.ScopeLogs)
		for _, sl := range rl.ScopeLogs {
			normalizeScope(sl.Scope)
			sl.LogRecords = dropNil(sl.LogRecords)
			for _, record := range sl.LogRecords {
				record.Body = normalizeValue(record.Body)
				record.Attributes = normalizeKeyValues(record.Attributes)
			}
		}
	}
}

// NormalizeMetricsRequest makes req safe to convert with
// otel.ResourceMetricsFromPB, in place.
func NormalizeMetricsRequest(req *colmetricspb.ExportMetricsServiceRequest) {
	if req == nil {
		return
	}
	req.ResourceMetrics = dropNil(req.ResourceMetrics)
	for _, rm := range req.ResourceMetrics {
		rm.Resource = normalizeResource(rm.Resource)
		rm.ScopeMetrics = dropNil(rm.ScopeMetrics)
		for _, sm := range rm.ScopeMetrics {
			normalizeScope(sm.Scope)
			sm.Metrics = dropNil(sm.Metrics)
			for _, metric := range sm.Metrics {
				switch data := metric.Data.(type) {
				case *metricspb.Metric_Gauge:
					if data == nil {
						continue
					}
					if data.Gauge == nil {
						data.Gauge = &metricspb.Gauge{}
					}
					data.Gauge.DataPoints = normalizeNumberDataPoints(data.Gauge.DataPoints)
				case *metricspb.Metric_Sum:
					if data == nil {
						continue
					}
					if data.Sum == nil {
						data.Sum = &metricspb.Sum{}
					}
					data.Sum.DataPoints = normalizeNumberDataPoints(data.Sum.DataPoints)
				}
			}
		}
	}
}

func normalizeNumberDataPoints(points []*metricspb.NumberDataPoint) []*metricspb.NumberDataPoint {
	points = dropNil(points)
	for _, point := range points {
		point.Attributes = normalizeKeyValues(point.Attributes)
		point.Exemplars = dropNil(point.Exemplars)
		for _, exemplar := range point.Exemplars {
			exemplar.FilteredAttributes = normalizeKeyValues(exemplar.FilteredAttributes)
		}
	}
	return points
}

func normalizeResource(res *resourcepb.Resource) *resourcepb.Resource {
	if res == nil {
		return &resourcepb.Resource{}
	}
	res.Attributes = normalizeKeyValues(res.Attributes)
	return res
}

func normalizeScope(scope *commonpb.InstrumentationScope) {
	if scope != nil {
		scope.Attributes = normalizeKeyValues(scope.Attributes)
	}
}

func normalizeKeyValues(kvs []*commonpb.KeyValue) []*commonpb.KeyValue {
	kvs = dropNil(kvs)
	for _, kv := range kvs {
		kv.Value = normalizeValue(kv.Value)
	}
	return kvs
}

// normalizeValue fills in a missing or unset value as the empty string, the
// closest typed stand-in for OTLP's "empty value", and recurses into arrays
// and kvlists, whose elements are converted the same way.
func normalizeValue(value *commonpb.AnyValue) *commonpb.AnyValue {
	if value == nil {
		return &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{}}
	}
	switch x := value.Value.(type) {
	case nil:
		value.Value = &commonpb.AnyValue_StringValue{}
	case *commonpb.AnyValue_ArrayValue:
		if x != nil && x.ArrayValue != nil {
			for i, elem := range x.ArrayValue.Values {
				x.ArrayValue.Values[i] = normalizeValue(elem)
			}
		}
	case *commonpb.AnyValue_KvlistValue:
		if x != nil && x.KvlistValue != nil {
			x.KvlistValue.Values = normalizeKeyValues(x.KvlistValue.Values)
		}
	}
	return value
}

// dropNil removes nil entries, which proto decoding never produces but a
// request assembled in Go can carry, so the walks above (and the code after
// them) need not check.
func dropNil[T any](items []*T) []*T {
	return slices.DeleteFunc(items, func(item *T) bool { return item == nil })
}
