package testutil

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/testctx"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// tWriter is a writer that writes to testing.T
type tWriter struct {
	t   testing.TB
	buf bytes.Buffer
	mu  sync.Mutex
}

// NewTWriter creates a new TWriter
func NewTWriter(t testing.TB) io.Writer {
	tw := &tWriter{t: t}
	t.Cleanup(tw.flush)
	return tw
}

// Write writes data to the testing.T
func (tw *tWriter) Write(p []byte) (n int, err error) {
	tw.mu.Lock()
	defer tw.mu.Unlock()

	tw.t.Helper()

	if n, err = tw.buf.Write(p); err != nil {
		return n, err
	}

	for {
		line, err := tw.buf.ReadBytes('\n')
		if err == io.EOF {
			// If we've reached the end of the buffer, write it back, because it doesn't have a newline
			tw.buf.Write(line)
			break
		}
		if err != nil {
			return n, err
		}

		tw.t.Log(strings.TrimSuffix(string(line), "\n"))
	}
	return n, nil
}

func (tw *tWriter) flush() {
	tw.mu.Lock()
	defer tw.mu.Unlock()
	tw.t.Log(tw.buf.String())
}

// SpanOpts returns span options for testctx
func SpanOpts[T testctx.Runner[T]](w *testctx.W[T]) []trace.SpanStartOption {
	var t T
	attrs := []attribute.KeyValue{
		attribute.String("dagger.io/testctx.name", w.Name()),
		attribute.String("dagger.io/testctx.type", fmt.Sprintf("%T", t)),
		// Prevent revealed/rolled-up stuff bubbling up through test spans.
		attribute.Bool(telemetry.UIBoundaryAttr, true),
	}
	if strings.Count(w.Name(), "/") == 0 {
		// Only reveal top-level test suites; we don't need to automatically see
		// every single one.
		attrs = append(attrs, attribute.Bool(telemetry.UIRevealAttr, true))
	}
	if _, ok := os.LookupEnv("TESTCTX_PREWARM"); ok {
		attrs = append(attrs, attribute.Bool("dagger.io/testctx.prewarm", true))
	}
	return []trace.SpanStartOption{
		trace.WithAttributes(attrs...),
	}
}
