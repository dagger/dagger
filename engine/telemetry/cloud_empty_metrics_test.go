package telemetry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	"go.opentelemetry.io/otel/sdk/resource"
)

type emptyMetricExportProbe struct {
	ctx       context.Context
	metrics   *metricdata.ResourceMetrics
	exports   int
	flushes   int
	shutdowns int
	err       error
}

func (*emptyMetricExportProbe) Temporality(sdkmetric.InstrumentKind) metricdata.Temporality {
	return metricdata.CumulativeTemporality
}

func (*emptyMetricExportProbe) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return sdkmetric.DefaultAggregationSelector(kind)
}

func (p *emptyMetricExportProbe) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	p.ctx, p.metrics = ctx, metrics
	p.exports++
	return p.err
}

func (p *emptyMetricExportProbe) ForceFlush(context.Context) error {
	p.flushes++
	return p.err
}

func (p *emptyMetricExportProbe) Shutdown(context.Context) error {
	p.shutdowns++
	return p.err
}

func TestSequencedMetricsSkipEmptyCollection(t *testing.T) {
	probe := &emptyMetricExportProbe{}
	exporter := &sequencedMetricExporter{sequencer: newExportSequencer(), exporter: probe}
	for _, data := range []*metricdata.ResourceMetrics{{}, {Resource: resource.Empty()}} {
		require.NoError(t, exporter.Export(t.Context(), data))
	}
	require.Zero(t, probe.exports)
	require.Zero(t, exporter.sequencer.sequence.Load())

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	require.ErrorIs(t, exporter.Export(ctx, &metricdata.ResourceMetrics{}), context.Canceled)
	require.Zero(t, probe.exports)
	require.Zero(t, exporter.sequencer.sequence.Load())

	require.NoError(t, exporter.Shutdown(t.Context()))
	require.ErrorIs(t, exporter.Export(t.Context(), &metricdata.ResourceMetrics{}), sdkmetric.ErrExporterShutdown)
	require.Zero(t, probe.exports)
	require.Zero(t, exporter.sequencer.sequence.Load())
}

func TestSequencedMetricsPreserveNonemptyAndNilCollections(t *testing.T) {
	wantErr := errors.New("export failed")
	probe := &emptyMetricExportProbe{err: wantErr}
	exporter := &sequencedMetricExporter{sequencer: newExportSequencer(), exporter: probe}
	// An explicitly present scope is forwarded even if it has no data points.
	// The optimization only recognizes the SDK's no-scope final collection.
	data := &metricdata.ResourceMetrics{ScopeMetrics: []metricdata.ScopeMetrics{{}}}
	require.ErrorIs(t, exporter.Export(t.Context(), data), wantErr)
	require.Same(t, data, probe.metrics)
	metadata := probe.ctx.Value(exportSequenceContextKey{}).(exportSequenceMetadata)
	require.Equal(t, uint64(1), metadata.sequence)
	require.Equal(t, exporter.sequencer.writerID, metadata.writerID)

	require.ErrorIs(t, exporter.Export(t.Context(), nil), wantErr)
	require.Nil(t, probe.metrics)
	require.Equal(t, 2, probe.exports)
	require.Equal(t, uint64(2), exporter.sequencer.sequence.Load())
	require.ErrorIs(t, exporter.ForceFlush(t.Context()), wantErr)
	require.ErrorIs(t, exporter.Shutdown(t.Context()), wantErr)
	require.Equal(t, 1, probe.flushes)
	require.Equal(t, 1, probe.shutdowns)
}

func TestSequencedMetricsFinalSDKCollection(t *testing.T) {
	for _, record := range []bool{false, true} {
		name := "empty"
		if record {
			name = "counter"
		}
		t.Run(name, func(t *testing.T) {
			probe := &emptyMetricExportProbe{}
			exporter := &sequencedMetricExporter{sequencer: newExportSequencer(), exporter: probe}
			reader := sdkmetric.NewPeriodicReader(exporter, sdkmetric.WithInterval(time.Hour))
			provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
			if record {
				counter, err := provider.Meter("test").Int64Counter("value")
				require.NoError(t, err)
				counter.Add(t.Context(), 7)
			}
			require.NoError(t, provider.Shutdown(t.Context()))
			require.Equal(t, 1, probe.shutdowns)
			if !record {
				require.Zero(t, probe.exports)
				require.Zero(t, exporter.sequencer.sequence.Load())
				return
			}
			require.Equal(t, 1, probe.exports)
			require.Len(t, probe.metrics.ScopeMetrics, 1)
			require.Len(t, probe.metrics.ScopeMetrics[0].Metrics, 1)
			sum, ok := probe.metrics.ScopeMetrics[0].Metrics[0].Data.(metricdata.Sum[int64])
			require.True(t, ok)
			require.Len(t, sum.DataPoints, 1)
			require.Equal(t, int64(7), sum.DataPoints[0].Value)
			require.Equal(t, uint64(1), exporter.sequencer.sequence.Load())
		})
	}
}
