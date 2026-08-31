package telemetry

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	collogspb "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	colmetricspb "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	coltracepb "go.opentelemetry.io/proto/otlp/collector/trace/v1"
)

// ArchiveHighWater is the immutable final cursor of each archive signal.
type ArchiveHighWater struct {
	Spans   int64
	Logs    int64
	Metrics int64
}

// ArchiveCut identifies one immutable version of a telemetry archive. The
// bootstrap and all three remainder streams must present this same cut.
type ArchiveCut struct {
	Generation string
	HighWater  ArchiveHighWater
	SealAt     time.Time
}

// ArchiveSignal identifies one independently streamed OTLP signal.
type ArchiveSignal string

const (
	ArchiveSpans   ArchiveSignal = "spans"
	ArchiveLogs    ArchiveSignal = "logs"
	ArchiveMetrics ArchiveSignal = "metrics"
)

var (
	ErrArchiveCutMismatch   = errors.New("archive import cut mismatch")
	ErrArchiveSignalClosed  = errors.New("archive import signal is closed")
	ErrArchiveSpanAbandoned = errors.New("archive span import was permanently abandoned")
)

// ArchiveImportBatch carries exactly one OTLP export request. A bootstrap and
// its remainder intentionally use the same batch type and importer.
type ArchiveImportBatch struct {
	Spans   *coltracepb.ExportTraceServiceRequest
	Logs    *collogspb.ExportLogsServiceRequest
	Metrics *colmetricspb.ExportMetricsServiceRequest
}

func (batch ArchiveImportBatch) signal() (ArchiveSignal, error) {
	var signal ArchiveSignal
	count := 0
	if batch.Spans != nil {
		signal = ArchiveSpans
		count++
	}
	if batch.Logs != nil {
		signal = ArchiveLogs
		count++
	}
	if batch.Metrics != nil {
		signal = ArchiveMetrics
		count++
	}
	if count != 1 {
		return "", fmt.Errorf("archive import batch must contain exactly one signal, got %d", count)
	}
	return signal, nil
}

// ArchiveTraceImporter imports a bootstrap and its bounded remainder against
// one fixed source cut. All exporter and barrier calls are serialized so a
// successful ImportAndWait is an acknowledgment that the frontend has applied
// that batch, not merely accepted it into an asynchronous dispatch queue.
type ArchiveTraceImporter struct {
	cut     ArchiveCut
	trace   *TraceImporter
	barrier TraceImportBarrier

	mu        sync.Mutex
	closed    map[ArchiveSignal]bool
	abandoned map[ArchiveSignal]bool
}

func NewArchiveTraceImporter(sinks TraceImportSinks, cut ArchiveCut) (*ArchiveTraceImporter, error) {
	if cut.Generation == "" {
		return nil, errors.New("archive import generation is empty")
	}
	if cut.SealAt.IsZero() {
		return nil, errors.New("archive import seal time is zero")
	}
	if cut.HighWater.Spans < 0 || cut.HighWater.Logs < 0 || cut.HighWater.Metrics < 0 {
		return nil, fmt.Errorf("archive import high-water contains a negative cursor: %+v", cut.HighWater)
	}
	if sinks.Barrier == nil {
		return nil, errors.New("archive import requires a frontend event-loop barrier")
	}
	return &ArchiveTraceImporter{
		cut:       cut,
		trace:     NewTraceImporter(sinks),
		barrier:   sinks.Barrier,
		closed:    map[ArchiveSignal]bool{},
		abandoned: map[ArchiveSignal]bool{},
	}, nil
}

// ImportAndWait imports one batch and waits until the frontend event loop has
// applied it. Success is the caller's acknowledgment that its archive cursor
// may advance. Completing bootstrap requires no special call and never seals;
// the same importer remains open for remainder batches.
func (imp *ArchiveTraceImporter) ImportAndWait(ctx context.Context, cut ArchiveCut, batch ArchiveImportBatch) error {
	imp.mu.Lock()
	defer imp.mu.Unlock()

	if err := imp.checkCut(cut); err != nil {
		return err
	}
	signal, err := batch.signal()
	if err != nil {
		return err
	}
	if imp.abandoned[signal] {
		if signal == ArchiveSpans {
			return ErrArchiveSpanAbandoned
		}
		return fmt.Errorf("%w: %s was abandoned", ErrArchiveSignalClosed, signal)
	}
	if imp.closed[signal] {
		return fmt.Errorf("%w: %s", ErrArchiveSignalClosed, signal)
	}

	switch signal {
	case ArchiveSpans:
		err = imp.trace.ImportSpans(ctx, batch.Spans)
	case ArchiveLogs:
		err = imp.trace.ImportLogs(ctx, batch.Logs)
	case ArchiveMetrics:
		err = imp.trace.ImportMetrics(ctx, batch.Metrics)
	}
	if err != nil {
		return err
	}
	return imp.barrier.WaitForEventLoop(ctx)
}

// Wait is an event-loop barrier with fixed-cut validation. It is useful for a
// terminal bootstrap frame that carries no records. It deliberately does not
// seal unfinished spans.
func (imp *ArchiveTraceImporter) Wait(ctx context.Context, cut ArchiveCut) error {
	imp.mu.Lock()
	defer imp.mu.Unlock()
	if err := imp.checkCut(cut); err != nil {
		return err
	}
	return imp.barrier.WaitForEventLoop(ctx)
}

// CompleteRemainder records a bounded signal's terminal cursor. Spans seal at
// the manifest timestamp only when their remainder reaches its exact high-water
// mark. Log and metric completion cannot delay span sealing.
func (imp *ArchiveTraceImporter) CompleteRemainder(ctx context.Context, cut ArchiveCut, signal ArchiveSignal, cursor int64) error {
	imp.mu.Lock()
	defer imp.mu.Unlock()
	if err := imp.checkCut(cut); err != nil {
		return err
	}
	if err := validateArchiveSignal(signal); err != nil {
		return err
	}
	if imp.abandoned[signal] {
		if signal == ArchiveSpans {
			return ErrArchiveSpanAbandoned
		}
		return fmt.Errorf("%w: %s was abandoned", ErrArchiveSignalClosed, signal)
	}
	want := imp.highWater(signal)
	if cursor != want {
		return fmt.Errorf("archive %s terminal cursor %d does not match high-water %d", signal, cursor, want)
	}
	if imp.closed[signal] {
		return nil
	}

	if signal == ArchiveSpans {
		if err := imp.trace.SealAt(ctx, imp.cut.SealAt); err != nil {
			return err
		}
	}
	if err := imp.barrier.WaitForEventLoop(ctx); err != nil {
		return err
	}
	imp.closed[signal] = true
	return nil
}

// AbandonRemainder permanently stops one signal after its retry policy is
// exhausted. Abandoning spans seals the unfinished set at the manifest time and
// irrevocably rejects later span batches. Other signals have no bearing on the
// span seal.
func (imp *ArchiveTraceImporter) AbandonRemainder(ctx context.Context, cut ArchiveCut, signal ArchiveSignal) error {
	imp.mu.Lock()
	defer imp.mu.Unlock()
	if err := imp.checkCut(cut); err != nil {
		return err
	}
	if err := validateArchiveSignal(signal); err != nil {
		return err
	}
	if imp.closed[signal] {
		return nil
	}
	if imp.abandoned[signal] {
		if signal == ArchiveSpans {
			if err := imp.trace.SealAt(ctx, imp.cut.SealAt); err != nil {
				return err
			}
		}
		return imp.barrier.WaitForEventLoop(ctx)
	}

	// This state transition is permanent even if exporter or barrier waiting
	// fails: once a caller abandons the span stream, accepting a late retry could
	// reintroduce live historical work after the seal.
	imp.abandoned[signal] = true
	if signal == ArchiveSpans {
		if err := imp.trace.SealAt(ctx, imp.cut.SealAt); err != nil {
			return err
		}
	}
	return imp.barrier.WaitForEventLoop(ctx)
}

func (imp *ArchiveTraceImporter) checkCut(got ArchiveCut) error {
	if imp.cut.Generation != got.Generation ||
		imp.cut.HighWater != got.HighWater ||
		!imp.cut.SealAt.Equal(got.SealAt) {
		return fmt.Errorf("%w: got generation %q high-water %+v seal %s; want generation %q high-water %+v seal %s",
			ErrArchiveCutMismatch,
			got.Generation, got.HighWater, got.SealAt,
			imp.cut.Generation, imp.cut.HighWater, imp.cut.SealAt)
	}
	return nil
}

func (imp *ArchiveTraceImporter) highWater(signal ArchiveSignal) int64 {
	switch signal {
	case ArchiveSpans:
		return imp.cut.HighWater.Spans
	case ArchiveLogs:
		return imp.cut.HighWater.Logs
	case ArchiveMetrics:
		return imp.cut.HighWater.Metrics
	default:
		return 0
	}
}

func validateArchiveSignal(signal ArchiveSignal) error {
	switch signal {
	case ArchiveSpans, ArchiveLogs, ArchiveMetrics:
		return nil
	default:
		return fmt.Errorf("unknown archive signal %q", signal)
	}
}
