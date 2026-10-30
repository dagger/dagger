package server

import (
	"reflect"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	otlpmetricsv1 "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

func TestCloudMetricSnapshotsOwnAllReaderBuffers(t *testing.T) {
	cases := []metricdata.Aggregation{
		metricdata.Gauge[int64]{DataPoints: []metricdata.DataPoint[int64]{{Value: 7, Exemplars: []metricdata.Exemplar[int64]{{Value: 3, SpanID: []byte{1}, TraceID: []byte{2}, FilteredAttributes: []attribute.KeyValue{attribute.String("source", "reader")}}}}}},
		metricdata.Gauge[float64]{DataPoints: []metricdata.DataPoint[float64]{{Value: 1.5}}},
		metricdata.Sum[int64]{Temporality: metricdata.CumulativeTemporality, IsMonotonic: true, DataPoints: []metricdata.DataPoint[int64]{{Value: 7}}},
		metricdata.Sum[float64]{Temporality: metricdata.DeltaTemporality, DataPoints: []metricdata.DataPoint[float64]{{Value: 1.5}}},
		metricdata.Histogram[int64]{Temporality: metricdata.CumulativeTemporality, DataPoints: []metricdata.HistogramDataPoint[int64]{{Count: 3, Bounds: []float64{10}, BucketCounts: []uint64{1, 2}}}},
		metricdata.Histogram[float64]{Temporality: metricdata.CumulativeTemporality, DataPoints: []metricdata.HistogramDataPoint[float64]{{Count: 3, Bounds: []float64{10}, BucketCounts: []uint64{1, 2}}}},
		metricdata.ExponentialHistogram[int64]{Temporality: metricdata.CumulativeTemporality, DataPoints: []metricdata.ExponentialHistogramDataPoint[int64]{{Count: 3, PositiveBucket: metricdata.ExponentialBucket{Counts: []uint64{1}}, NegativeBucket: metricdata.ExponentialBucket{Counts: []uint64{2}}}}},
		metricdata.ExponentialHistogram[float64]{Temporality: metricdata.CumulativeTemporality, DataPoints: []metricdata.ExponentialHistogramDataPoint[float64]{{Count: 3, PositiveBucket: metricdata.ExponentialBucket{Counts: []uint64{1}}, NegativeBucket: metricdata.ExponentialBucket{Counts: []uint64{2}}}}},
		metricdata.Summary{DataPoints: []metricdata.SummaryDataPoint{{Count: 1, Sum: 2, QuantileValues: []metricdata.QuantileValue{{Quantile: 0.5, Value: 2}}}}},
	}
	for _, data := range cases {
		t.Run(reflect.TypeOf(data).String(), func(t *testing.T) {
			input := gaugeMetrics(0)
			input.ScopeMetrics[0].Metrics[0].Data = data
			before, err := telemetry.ResourceMetricsToPB(input)
			require.NoError(t, err)
			before = proto.Clone(before).(*otlpmetricsv1.ResourceMetrics)
			snapshot, err := snapshotCloudMetrics(input)
			require.NoError(t, err)
			clearMetricBufferSlices(reflect.ValueOf(data))
			input.ScopeMetrics[0].Metrics[0].Name = "reused reader buffer"
			after, err := telemetry.ResourceMetricsToPB(snapshot)
			require.NoError(t, err)
			require.True(t, proto.Equal(before, after), "reader buffer reuse changed the queued snapshot")
		})
	}
}

// Mutate nested reader-owned slices, including exemplars and histogram
// buckets. Immutable attribute sets and resources are deliberately untouched.
func clearMetricBufferSlices(value reflect.Value) {
	switch value.Kind() {
	case reflect.Interface, reflect.Pointer:
		if !value.IsNil() {
			clearMetricBufferSlices(value.Elem())
		}
	case reflect.Struct:
		for i := range value.NumField() {
			if value.Type().Field(i).IsExported() {
				clearMetricBufferSlices(value.Field(i))
			}
		}
	case reflect.Slice:
		for i := range value.Len() {
			element := value.Index(i)
			clearMetricBufferSlices(element)
			if element.CanSet() {
				element.SetZero()
			}
		}
	}
}
