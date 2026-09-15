package telemetry

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	logapi "go.opentelemetry.io/otel/log"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// fakeSpanDest counts what reaches one fan-out destination and can be made to
// fail, simulating one per-client store writer among several (a client's own
// DB next to its ancestors').
type fakeSpanDest struct {
	exportErr   error // returned from every ExportSpans when set
	shutdownErr error // returned from Shutdown when set

	mu       sync.Mutex
	exported int
	shutdown bool
}

var _ sdktrace.SpanExporter = (*fakeSpanDest)(nil)

func (d *fakeSpanDest) ExportSpans(_ context.Context, spans []sdktrace.ReadOnlySpan) error {
	d.mu.Lock()
	d.exported += len(spans)
	d.mu.Unlock()
	return d.exportErr
}

func (d *fakeSpanDest) Shutdown(context.Context) error {
	d.mu.Lock()
	d.shutdown = true
	d.mu.Unlock()
	return d.shutdownErr
}

func (d *fakeSpanDest) exportedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exported
}

func (d *fakeSpanDest) wasShutdown() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.shutdown
}

func testSpanSnapshot() sdktrace.ReadOnlySpan {
	return tracetest.SpanStub{
		Name: "fanout-span",
		SpanContext: trace.NewSpanContext(trace.SpanContextConfig{
			TraceID:    trace.TraceID{1},
			SpanID:     trace.SpanID{1},
			TraceFlags: trace.FlagsSampled,
		}),
		StartTime: time.Now(),
		EndTime:   time.Now(),
	}.Snapshot()
}

// TestSpanFanOutExporterDeliversToAll proves the property the memory fix
// depends on: one export call behind one shared queue still lands every span
// on every destination store, exactly as the per-destination processors did.
func TestSpanFanOutExporterDeliversToAll(t *testing.T) {
	t.Parallel()

	dests := []*fakeSpanDest{{}, {}, {}}
	exp := NewSpanFanOutExporter(dests[0], dests[1], dests[2])

	spans := []sdktrace.ReadOnlySpan{testSpanSnapshot(), testSpanSnapshot()}
	require.NoError(t, exp.ExportSpans(context.Background(), spans))

	for i, d := range dests {
		require.Equal(t, len(spans), d.exportedCount(),
			"destination %d must receive every span", i)
	}
}

// TestSpanFanOutExporterErrorIsolation proves a failing destination cannot
// starve the others: with per-destination processors a store failure was
// naturally isolated, and the shared queue must not regress that. The
// failure still surfaces, joined with any others.
func TestSpanFanOutExporterErrorIsolation(t *testing.T) {
	t.Parallel()

	errA := errors.New("dest A failed")
	errC := errors.New("dest C failed")
	dests := []*fakeSpanDest{{exportErr: errA}, {}, {exportErr: errC}}
	exp := NewSpanFanOutExporter(dests[0], dests[1], dests[2])

	err := exp.ExportSpans(context.Background(), []sdktrace.ReadOnlySpan{testSpanSnapshot()})
	require.ErrorIs(t, err, errA)
	require.ErrorIs(t, err, errC)

	// The healthy destination in the middle still received the span.
	require.Equal(t, 1, dests[1].exportedCount(),
		"an error from one destination must not prevent export to others")
	// And so did the one after the first failure.
	require.Equal(t, 1, dests[2].exportedCount())
}

// TestSpanFanOutExporterShutdown proves Shutdown reaches every destination
// even when one fails, with the failure surfaced.
func TestSpanFanOutExporterShutdown(t *testing.T) {
	t.Parallel()

	errB := errors.New("dest B shutdown failed")
	dests := []*fakeSpanDest{{}, {shutdownErr: errB}, {}}
	exp := NewSpanFanOutExporter(dests[0], dests[1], dests[2])

	err := exp.Shutdown(context.Background())
	require.ErrorIs(t, err, errB)
	for i, d := range dests {
		require.True(t, d.wasShutdown(), "destination %d must be shut down", i)
	}
}

// fakeLogDest is the log analog of fakeSpanDest.
type fakeLogDest struct {
	exportErr   error // returned from every Export when set
	shutdownErr error // returned from Shutdown when set
	flushErr    error // returned from ForceFlush when set

	mu       sync.Mutex
	exported int
	shutdown bool
	flushed  bool
}

var _ sdklog.Exporter = (*fakeLogDest)(nil)

func (d *fakeLogDest) Export(_ context.Context, recs []sdklog.Record) error {
	d.mu.Lock()
	d.exported += len(recs)
	d.mu.Unlock()
	return d.exportErr
}

func (d *fakeLogDest) Shutdown(context.Context) error {
	d.mu.Lock()
	d.shutdown = true
	d.mu.Unlock()
	return d.shutdownErr
}

func (d *fakeLogDest) ForceFlush(context.Context) error {
	d.mu.Lock()
	d.flushed = true
	d.mu.Unlock()
	return d.flushErr
}

func (d *fakeLogDest) exportedCount() int {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.exported
}

func (d *fakeLogDest) wasShutdown() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.shutdown
}

func (d *fakeLogDest) wasFlushed() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.flushed
}

func testLogRecords(n int) []sdklog.Record {
	recs := make([]sdklog.Record, n)
	for i := range recs {
		recs[i].SetTimestamp(time.Now())
		recs[i].SetBody(logapi.StringValue("fanout"))
	}
	return recs
}

// TestLogFanOutExporterDeliversToAll proves every record reaches every
// destination store through the single shared processor queue.
func TestLogFanOutExporterDeliversToAll(t *testing.T) {
	t.Parallel()

	dests := []*fakeLogDest{{}, {}, {}}
	exp := NewLogFanOutExporter(dests[0], dests[1], dests[2])

	recs := testLogRecords(3)
	require.NoError(t, exp.Export(context.Background(), recs))

	for i, d := range dests {
		require.Equal(t, len(recs), d.exportedCount(),
			"destination %d must receive every record", i)
	}
}

// TestLogFanOutExporterErrorIsolation proves a failing destination cannot
// starve the others, and its error surfaces joined.
func TestLogFanOutExporterErrorIsolation(t *testing.T) {
	t.Parallel()

	errA := errors.New("dest A failed")
	dests := []*fakeLogDest{{exportErr: errA}, {}}
	exp := NewLogFanOutExporter(dests[0], dests[1])

	err := exp.Export(context.Background(), testLogRecords(1))
	require.ErrorIs(t, err, errA)
	require.Equal(t, 1, dests[1].exportedCount(),
		"an error from one destination must not prevent export to others")
}

// TestLogFanOutExporterShutdownAndFlush proves Shutdown and ForceFlush reach
// every destination even when one fails, with failures surfaced.
func TestLogFanOutExporterShutdownAndFlush(t *testing.T) {
	t.Parallel()

	errFlush := errors.New("dest A flush failed")
	errShutdown := errors.New("dest B shutdown failed")
	dests := []*fakeLogDest{{flushErr: errFlush}, {shutdownErr: errShutdown}, {}}
	exp := NewLogFanOutExporter(dests[0], dests[1], dests[2])

	err := exp.ForceFlush(context.Background())
	require.ErrorIs(t, err, errFlush)
	for i, d := range dests {
		require.True(t, d.wasFlushed(), "destination %d must be flushed", i)
	}

	err = exp.Shutdown(context.Background())
	require.ErrorIs(t, err, errShutdown)
	for i, d := range dests {
		require.True(t, d.wasShutdown(), "destination %d must be shut down", i)
	}
}
