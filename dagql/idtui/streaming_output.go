package idtui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"github.com/vito/progrock/ui"

	"os"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/muesli/termenv"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type streamingExporter struct {
	spanNames sync.Map

	// incr idx for spanID
	idx                 int64
	batchPrinterGetters sync.Map
	batchPrinterWg      sync.WaitGroup

	output   *termenv.Output
	outputMu sync.RWMutex

	frameTicker *time.Ticker

	done     chan struct{}
	doneOnce sync.Once
}

func newStreamingExporter() *streamingExporter {
	return &streamingExporter{
		output:      termenv.NewOutput(os.Stdout, termenv.WithProfile(ui.ColorProfile()), termenv.WithUnsafe()),
		done:        make(chan struct{}),
		frameTicker: time.NewTicker(50 * time.Millisecond),
	}
}

func (t *streamingExporter) Shutdown(ctx context.Context) error {
	t.doneOnce.Do(func() {
		t.frameTicker.Stop()
		close(t.done)
	})

	// wait all printers outputs
	t.batchPrinterWg.Wait()
	return nil
}

func (t *streamingExporter) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		t.spanNames.Store(span.SpanContext().SpanID(), span.Name())
	}
	return nil
}

func (t *streamingExporter) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	for _, log := range logs {
		t.export(log)
	}
	return nil
}

func (t *streamingExporter) getSpanName(spanID trace.SpanID) string {
	if name, ok := t.spanNames.Load(spanID); ok {
		return name.(string)
	}
	return ""
}

func (t *streamingExporter) export(logData *sdklog.LogData) {
	// FIXME may add GC when printer idle
	get, _ := t.batchPrinterGetters.LoadOrStore(logData.SpanID, sync.OnceValue(func() logPrinter {
		w := &batchPrinter{
			spanID:      logData.SpanID,
			num:         atomic.AddInt64(&t.idx, 1),
			getSpanName: t.getSpanName,
		}

		t.batchPrinterWg.Add(1)

		go func() {
			defer t.batchPrinterWg.Done()

			<-t.done

			t.tryRender(func(out *termenv.Output) {
				w.render(out, true)
			})
		}()

		go func() {
			for range t.frameTicker.C {
				t.tryRender(func(out *termenv.Output) {
					w.render(out, false)
				})
			}
		}()

		return w
	}))

	get.(func() logPrinter)().PrintLog(logData)
}

func (t *streamingExporter) tryRender(print func(out *termenv.Output)) {
	t.outputMu.Lock()
	defer t.outputMu.Unlock()

	print(t.output)
}

type batchPrinter struct {
	num         int64
	spanID      trace.SpanID
	getSpanName func(spanID trace.SpanID) string

	lineCount     int
	latestPrintAt time.Time

	lines []string
	mu    sync.RWMutex
}

func (w *batchPrinter) PrintLog(data *sdklog.LogData) {
	msg := data.Body().AsString()

	if msg == "" {
		return
	}

	s := bufio.NewScanner(bytes.NewBufferString(msg))
	for s.Scan() {
		if line := s.Text(); len(line) > 0 {
			w.collect(line)
		}
	}
}

func (w *batchPrinter) collect(line string) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.lines = append(w.lines, line)
}

func (w *batchPrinter) render(out *termenv.Output, final bool) {
	w.mu.Lock()
	defer w.mu.Unlock()

	lineCount := len(w.lines)

	if lineCount == 0 {
		return
	}

	if final || lineCount > 10 || time.Since(w.latestPrintAt) > time.Second {
		_, _ = fmt.Fprint(out, out.String(fmt.Sprintf("%d: ", w.num)).Foreground(termenv.ANSIBrightMagenta))
		_, _ = fmt.Fprint(out, out.String("in ").Foreground(termenv.ANSICyan))
		_, _ = fmt.Fprintf(out, "%s\n", w.getSpanName(w.spanID))

		for _, line := range w.lines {
			_, _ = fmt.Fprint(out, out.String(fmt.Sprintf("%d: ", w.num)).Foreground(termenv.ANSIBrightMagenta))
			_, _ = fmt.Fprintln(out, line)
		}

		// one more line to break
		_, _ = fmt.Fprintln(out)

		w.lines = nil
		w.latestPrintAt = time.Now()
	}
}

type logPrinter interface {
	PrintLog(data *sdklog.LogData)
}
