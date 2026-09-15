package telemetry

import (
	"context"
	"fmt"
	"net/http"
	"sync/atomic"

	"github.com/google/uuid"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const cloudExportHeader = "X-Dagger-Export"

type exportSequenceContextKey struct{}

type exportSequenceMetadata struct {
	writerID string
	sequence uint64
}

type exportSequencer struct {
	writerID string
	sequence atomic.Uint64
}

func newExportSequencer() *exportSequencer {
	return &exportSequencer{writerID: uuid.NewString()}
}

func (s *exportSequencer) nextContext(ctx context.Context) context.Context {
	return context.WithValue(ctx, exportSequenceContextKey{}, exportSequenceMetadata{
		writerID: s.writerID,
		sequence: s.sequence.Add(1),
	})
}

func (s *exportSequencer) httpClient() *http.Client {
	return &http.Client{Transport: exportSequenceTransport{base: http.DefaultTransport}}
}

type exportSequenceTransport struct {
	base http.RoundTripper
}

func (t exportSequenceTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	metadata, ok := req.Context().Value(exportSequenceContextKey{}).(exportSequenceMetadata)
	if !ok {
		return t.base.RoundTrip(req)
	}

	request := req.Clone(req.Context())
	request.Header = req.Header.Clone()
	request.Header.Set(cloudExportHeader, fmt.Sprintf("%s/%d", metadata.writerID, metadata.sequence))
	return t.base.RoundTrip(request)
}

type sequencedSpanExporter struct {
	sequencer *exportSequencer
	exporter  sdktrace.SpanExporter
}

var _ sdktrace.SpanExporter = (*sequencedSpanExporter)(nil)

func (e *sequencedSpanExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	return e.exporter.ExportSpans(e.sequencer.nextContext(ctx), spans)
}

func (e *sequencedSpanExporter) Shutdown(ctx context.Context) error {
	return e.exporter.Shutdown(ctx)
}

type sequencedLogExporter struct {
	sequencer *exportSequencer
	exporter  sdklog.Exporter
}

var _ sdklog.Exporter = (*sequencedLogExporter)(nil)

func (e *sequencedLogExporter) Export(ctx context.Context, logs []sdklog.Record) error {
	return e.exporter.Export(e.sequencer.nextContext(ctx), logs)
}

func (e *sequencedLogExporter) Shutdown(ctx context.Context) error {
	return e.exporter.Shutdown(ctx)
}

func (e *sequencedLogExporter) ForceFlush(ctx context.Context) error {
	return e.exporter.ForceFlush(ctx)
}

type sequencedMetricExporter struct {
	sequencer *exportSequencer
	exporter  sdkmetric.Exporter
}

var _ sdkmetric.Exporter = (*sequencedMetricExporter)(nil)

func (e *sequencedMetricExporter) Temporality(kind sdkmetric.InstrumentKind) metricdata.Temporality {
	return e.exporter.Temporality(kind)
}

func (e *sequencedMetricExporter) Aggregation(kind sdkmetric.InstrumentKind) sdkmetric.Aggregation {
	return e.exporter.Aggregation(kind)
}

func (e *sequencedMetricExporter) Export(ctx context.Context, metrics *metricdata.ResourceMetrics) error {
	return e.exporter.Export(e.sequencer.nextContext(ctx), metrics)
}

func (e *sequencedMetricExporter) Shutdown(ctx context.Context) error {
	return e.exporter.Shutdown(ctx)
}

func (e *sequencedMetricExporter) ForceFlush(ctx context.Context) error {
	return e.exporter.ForceFlush(ctx)
}
