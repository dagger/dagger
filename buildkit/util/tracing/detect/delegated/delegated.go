package delegated

import (
	"context"
	"sync"

	"github.com/moby/buildkit/util/tracing/detect"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const maxBuffer = 256

var exp = &Exporter{}

func init() {
	detect.Register("delegated", detect.TraceExporterDetector(func() (sdktrace.SpanExporter, error) {
		return exp, nil
	}), 100)
}

type Exporter struct {
	mu        sync.Mutex
	exporters []sdktrace.SpanExporter
	buffer    []sdktrace.ReadOnlySpan
}

var _ sdktrace.SpanExporter = &Exporter{}

func (e *Exporter) ExportSpans(ctx context.Context, ss []sdktrace.ReadOnlySpan) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var err error
	for _, e := range e.exporters {
		if err1 := e.ExportSpans(ctx, ss); err1 != nil {
			err = err1
		}
	}
	if err != nil {
		return err
	}

	if len(e.buffer) > maxBuffer {
		return nil
	}

	e.buffer = append(e.buffer, ss...)
	return nil
}

func (e *Exporter) Shutdown(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	var err error
	for _, e := range e.exporters {
		if err1 := e.Shutdown(ctx); err1 != nil {
			err = err1
		}
	}

	return err
}

func (e *Exporter) SetSpanExporter(ctx context.Context, exp sdktrace.SpanExporter) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.exporters = append(e.exporters, exp)

	if len(e.buffer) > 0 {
		return exp.ExportSpans(ctx, e.buffer)
	}
	return nil
}
