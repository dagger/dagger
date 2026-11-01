package server

import (
	"fmt"
	"slices"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// Metric readers reuse their collection buffers after Export returns. Resource
// and attribute sets are immutable; all mutable collection slices need copies.
func snapshotCloudMetrics(in *metricdata.ResourceMetrics) (*metricdata.ResourceMetrics, error) {
	out := *in
	out.ScopeMetrics = slices.Clone(in.ScopeMetrics)
	for i := range out.ScopeMetrics {
		scope := &out.ScopeMetrics[i]
		scope.Metrics = slices.Clone(scope.Metrics)
		for j := range scope.Metrics {
			data, err := snapshotMetricAggregation(scope.Metrics[j].Data)
			if err != nil {
				return nil, err
			}
			scope.Metrics[j].Data = data
		}
	}
	return &out, nil
}

func snapshotMetricAggregation(in metricdata.Aggregation) (metricdata.Aggregation, error) {
	switch data := in.(type) {
	case metricdata.Gauge[int64]:
		data.DataPoints = snapshotNumberPoints(data.DataPoints)
		return data, nil
	case metricdata.Gauge[float64]:
		data.DataPoints = snapshotNumberPoints(data.DataPoints)
		return data, nil
	case metricdata.Sum[int64]:
		data.DataPoints = snapshotNumberPoints(data.DataPoints)
		return data, nil
	case metricdata.Sum[float64]:
		data.DataPoints = snapshotNumberPoints(data.DataPoints)
		return data, nil
	case metricdata.Histogram[int64]:
		data.DataPoints = snapshotHistogramPoints(data.DataPoints)
		return data, nil
	case metricdata.Histogram[float64]:
		data.DataPoints = snapshotHistogramPoints(data.DataPoints)
		return data, nil
	case metricdata.ExponentialHistogram[int64]:
		data.DataPoints = snapshotExponentialPoints(data.DataPoints)
		return data, nil
	case metricdata.ExponentialHistogram[float64]:
		data.DataPoints = snapshotExponentialPoints(data.DataPoints)
		return data, nil
	case metricdata.Summary:
		data.DataPoints = slices.Clone(data.DataPoints)
		for i := range data.DataPoints {
			data.DataPoints[i].QuantileValues = slices.Clone(data.DataPoints[i].QuantileValues)
		}
		return data, nil
	default:
		return nil, fmt.Errorf("unsupported metric aggregation %T", in)
	}
}

func snapshotNumberPoints[N int64 | float64](in []metricdata.DataPoint[N]) []metricdata.DataPoint[N] {
	out := slices.Clone(in)
	for i := range out {
		out[i].Exemplars = snapshotMetricExemplars(out[i].Exemplars)
	}
	return out
}

func snapshotHistogramPoints[N int64 | float64](in []metricdata.HistogramDataPoint[N]) []metricdata.HistogramDataPoint[N] {
	out := slices.Clone(in)
	for i := range out {
		out[i].Bounds = slices.Clone(out[i].Bounds)
		out[i].BucketCounts = slices.Clone(out[i].BucketCounts)
		out[i].Exemplars = snapshotMetricExemplars(out[i].Exemplars)
	}
	return out
}

func snapshotExponentialPoints[N int64 | float64](in []metricdata.ExponentialHistogramDataPoint[N]) []metricdata.ExponentialHistogramDataPoint[N] {
	out := slices.Clone(in)
	for i := range out {
		out[i].PositiveBucket.Counts = slices.Clone(out[i].PositiveBucket.Counts)
		out[i].NegativeBucket.Counts = slices.Clone(out[i].NegativeBucket.Counts)
		out[i].Exemplars = snapshotMetricExemplars(out[i].Exemplars)
	}
	return out
}

func snapshotMetricExemplars[N int64 | float64](in []metricdata.Exemplar[N]) []metricdata.Exemplar[N] {
	out := slices.Clone(in)
	for i := range out {
		out[i].FilteredAttributes = slices.Clone(out[i].FilteredAttributes)
		out[i].SpanID = slices.Clone(out[i].SpanID)
		out[i].TraceID = slices.Clone(out[i].TraceID)
	}
	return out
}
