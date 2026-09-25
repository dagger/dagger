package telemetry

import (
	"fmt"
	"slices"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
)

// ResourceMetricsFromPB supplements otel-go's gauge-only decoder. Keep metric
// types, temporality, scope metadata, timestamps, and integer precision when
// telemetry crosses an engine or CLI forwarding hop.
func ResourceMetricsFromPB(pb *metricspb.ResourceMetrics) (*metricdata.ResourceMetrics, error) {
	out := &metricdata.ResourceMetrics{Resource: resource.NewWithAttributes(pb.GetSchemaUrl(), telemetry.AttributesFromProto(pb.GetResource().GetAttributes())...)}
	for _, scope := range pb.GetScopeMetrics() {
		sm := metricdata.ScopeMetrics{Scope: instrumentation.Scope{
			Name: scope.GetScope().GetName(), Version: scope.GetScope().GetVersion(), SchemaURL: scope.GetSchemaUrl(),
			Attributes: attribute.NewSet(telemetry.AttributesFromProto(scope.GetScope().GetAttributes())...),
		}}
		for _, m := range scope.GetMetrics() {
			data, err := aggregationsFromPB(m)
			if err != nil {
				return nil, fmt.Errorf("metric %q: %w", m.GetName(), err)
			}
			for _, aggregation := range data {
				sm.Metrics = append(sm.Metrics, metricdata.Metrics{Name: m.GetName(), Description: m.GetDescription(), Unit: m.GetUnit(), Data: aggregation})
			}
		}
		out.ScopeMetrics = append(out.ScopeMetrics, sm)
	}
	return out, nil
}

// ResourceMetricsToPB retains fields omitted by the current otel-go encoder.
func ResourceMetricsToPB(data *metricdata.ResourceMetrics) (*metricspb.ResourceMetrics, error) {
	if data.Resource == nil {
		copy := *data
		copy.Resource = resource.Empty()
		data = &copy
	}
	pb, err := telemetry.ResourceMetricsToPB(data)
	if err != nil {
		return nil, err
	}
	for i, scope := range data.ScopeMetrics {
		pb.ScopeMetrics[i].Scope.Attributes = telemetry.KeyValues(scope.Scope.Attributes.ToSlice())
		for j, m := range scope.Metrics {
			// otel-go converts a zero Go time with UnixNano, which wraps to
			// an unsigned pre-epoch value. Zero means unspecified in OTLP.
			wire := pb.ScopeMetrics[i].Metrics[j]
			fixTime := func(start, end *uint64) {
				zero := uint64(time.Time{}.UnixNano())
				if *start == zero {
					*start = 0
				}
				if *end == zero {
					*end = 0
				}
			}
			for _, p := range wire.GetGauge().GetDataPoints() {
				fixTime(&p.StartTimeUnixNano, &p.TimeUnixNano)
			}
			for _, p := range wire.GetSum().GetDataPoints() {
				fixTime(&p.StartTimeUnixNano, &p.TimeUnixNano)
			}
			for _, p := range wire.GetHistogram().GetDataPoints() {
				fixTime(&p.StartTimeUnixNano, &p.TimeUnixNano)
			}
			for _, p := range wire.GetExponentialHistogram().GetDataPoints() {
				fixTime(&p.StartTimeUnixNano, &p.TimeUnixNano)
			}
			for _, p := range wire.GetSummary().GetDataPoints() {
				fixTime(&p.StartTimeUnixNano, &p.TimeUnixNano)
			}
			switch h := m.Data.(type) {
			case metricdata.ExponentialHistogram[int64]:
				for k, p := range h.DataPoints {
					pb.ScopeMetrics[i].Metrics[j].GetExponentialHistogram().DataPoints[k].ZeroThreshold = p.ZeroThreshold
				}
			case metricdata.ExponentialHistogram[float64]:
				for k, p := range h.DataPoints {
					pb.ScopeMetrics[i].Metrics[j].GetExponentialHistogram().DataPoints[k].ZeroThreshold = p.ZeroThreshold
				}
			}
		}
	}
	return pb, nil
}

func metricTime(n uint64) time.Time {
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, int64(n))
}

func metricTemporality(t metricspb.AggregationTemporality) (metricdata.Temporality, error) {
	switch t {
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_DELTA:
		return metricdata.DeltaTemporality, nil
	case metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE:
		return metricdata.CumulativeTemporality, nil
	default:
		return 0, fmt.Errorf("unsupported aggregation temporality %v", t)
	}
}

func numberPoints(pb []*metricspb.NumberDataPoint) ([]metricdata.DataPoint[int64], []metricdata.DataPoint[float64], error) {
	var integers []metricdata.DataPoint[int64]
	var floats []metricdata.DataPoint[float64]
	for _, point := range pb {
		// The Go SDK has no representation for OTLP no-recorded-value points.
		// Refuse them rather than silently turn absence into a measurement.
		if point.Flags != 0 {
			return nil, nil, fmt.Errorf("unsupported data point flags %d", point.Flags)
		}
		attrs := attribute.NewSet(telemetry.AttributesFromProto(point.Attributes)...)
		switch v := point.Value.(type) {
		case *metricspb.NumberDataPoint_AsInt:
			integers = append(integers, metricdata.DataPoint[int64]{Attributes: attrs, StartTime: metricTime(point.StartTimeUnixNano), Time: metricTime(point.TimeUnixNano), Value: v.AsInt, Exemplars: metricExemplars[int64](point.Exemplars)})
		case *metricspb.NumberDataPoint_AsDouble:
			floats = append(floats, metricdata.DataPoint[float64]{Attributes: attrs, StartTime: metricTime(point.StartTimeUnixNano), Time: metricTime(point.TimeUnixNano), Value: v.AsDouble, Exemplars: metricExemplars[float64](point.Exemplars)})
		default:
			return nil, nil, fmt.Errorf("missing numeric data point value")
		}
	}
	return integers, floats, nil
}

func metricExemplars[N int64 | float64](points []*metricspb.Exemplar) []metricdata.Exemplar[N] {
	out := make([]metricdata.Exemplar[N], 0, len(points))
	for _, p := range points {
		var value N
		switch v := p.Value.(type) {
		case *metricspb.Exemplar_AsInt:
			value = N(v.AsInt)
		case *metricspb.Exemplar_AsDouble:
			value = N(v.AsDouble)
		}
		out = append(out, metricdata.Exemplar[N]{FilteredAttributes: telemetry.AttributesFromProto(p.FilteredAttributes), Time: metricTime(p.TimeUnixNano), Value: value, TraceID: slices.Clone(p.TraceId), SpanID: slices.Clone(p.SpanId)})
	}
	return out
}

func metricExtrema(v *float64) metricdata.Extrema[float64] {
	if v == nil {
		return metricdata.Extrema[float64]{}
	}
	return metricdata.NewExtrema(*v)
}

func aggregationsFromPB(m *metricspb.Metric) ([]metricdata.Aggregation, error) {
	var out []metricdata.Aggregation
	switch data := m.Data.(type) {
	case *metricspb.Metric_Gauge:
		ints, floats, err := numberPoints(data.Gauge.DataPoints)
		if err != nil {
			return nil, err
		}
		if len(ints) > 0 || len(floats) == 0 {
			out = append(out, metricdata.Gauge[int64]{DataPoints: ints})
		}
		if len(floats) > 0 {
			out = append(out, metricdata.Gauge[float64]{DataPoints: floats})
		}
	case *metricspb.Metric_Sum:
		temporality, err := metricTemporality(data.Sum.AggregationTemporality)
		if err != nil {
			return nil, err
		}
		ints, floats, err := numberPoints(data.Sum.DataPoints)
		if err != nil {
			return nil, err
		}
		if len(ints) > 0 || len(floats) == 0 {
			out = append(out, metricdata.Sum[int64]{DataPoints: ints, Temporality: temporality, IsMonotonic: data.Sum.IsMonotonic})
		}
		if len(floats) > 0 {
			out = append(out, metricdata.Sum[float64]{DataPoints: floats, Temporality: temporality, IsMonotonic: data.Sum.IsMonotonic})
		}
	case *metricspb.Metric_Histogram:
		temporality, err := metricTemporality(data.Histogram.AggregationTemporality)
		if err != nil {
			return nil, err
		}
		h := metricdata.Histogram[float64]{Temporality: temporality}
		for _, p := range data.Histogram.DataPoints {
			if p.Flags != 0 || p.Sum == nil {
				return nil, fmt.Errorf("SDK cannot represent histogram flags or absent sum")
			}
			h.DataPoints = append(h.DataPoints, metricdata.HistogramDataPoint[float64]{
				Attributes: attribute.NewSet(telemetry.AttributesFromProto(p.Attributes)...), StartTime: metricTime(p.StartTimeUnixNano), Time: metricTime(p.TimeUnixNano),
				Count: p.Count, Sum: p.GetSum(), Min: metricExtrema(p.Min), Max: metricExtrema(p.Max),
				Bounds: slices.Clone(p.ExplicitBounds), BucketCounts: slices.Clone(p.BucketCounts), Exemplars: metricExemplars[float64](p.Exemplars),
			})
		}
		out = append(out, h)
	case *metricspb.Metric_ExponentialHistogram:
		temporality, err := metricTemporality(data.ExponentialHistogram.AggregationTemporality)
		if err != nil {
			return nil, err
		}
		h := metricdata.ExponentialHistogram[float64]{Temporality: temporality}
		for _, p := range data.ExponentialHistogram.DataPoints {
			// Both standard OTLP exporters in the current SDK omit this
			// field. Refuse the form instead of changing bucket semantics.
			if p.ZeroThreshold != 0 {
				return nil, fmt.Errorf("OTLP SDK exporter cannot preserve a nonzero exponential histogram zero threshold")
			}
			if p.Flags != 0 || p.Sum == nil {
				return nil, fmt.Errorf("SDK cannot represent exponential histogram flags or absent sum")
			}
			h.DataPoints = append(h.DataPoints, metricdata.ExponentialHistogramDataPoint[float64]{
				Attributes: attribute.NewSet(telemetry.AttributesFromProto(p.Attributes)...), StartTime: metricTime(p.StartTimeUnixNano), Time: metricTime(p.TimeUnixNano),
				Count: p.Count, Sum: p.GetSum(), Min: metricExtrema(p.Min), Max: metricExtrema(p.Max), Scale: p.Scale, ZeroCount: p.ZeroCount, ZeroThreshold: p.ZeroThreshold,
				PositiveBucket: metricdata.ExponentialBucket{Offset: p.GetPositive().GetOffset(), Counts: slices.Clone(p.GetPositive().GetBucketCounts())},
				NegativeBucket: metricdata.ExponentialBucket{Offset: p.GetNegative().GetOffset(), Counts: slices.Clone(p.GetNegative().GetBucketCounts())}, Exemplars: metricExemplars[float64](p.Exemplars),
			})
		}
		out = append(out, h)
	case *metricspb.Metric_Summary:
		s := metricdata.Summary{}
		for _, p := range data.Summary.DataPoints {
			if p.Flags != 0 {
				return nil, fmt.Errorf("unsupported summary data point flags %d", p.Flags)
			}
			point := metricdata.SummaryDataPoint{Attributes: attribute.NewSet(telemetry.AttributesFromProto(p.Attributes)...), StartTime: metricTime(p.StartTimeUnixNano), Time: metricTime(p.TimeUnixNano), Count: p.Count, Sum: p.Sum}
			for _, q := range p.QuantileValues {
				point.QuantileValues = append(point.QuantileValues, metricdata.QuantileValue{Quantile: q.Quantile, Value: q.Value})
			}
			s.DataPoints = append(s.DataPoints, point)
		}
		out = append(out, s)
	default:
		return nil, fmt.Errorf("unsupported aggregation %T", data)
	}
	return out, nil
}
