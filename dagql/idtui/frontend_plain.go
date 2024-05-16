package idtui

import (
	"bufio"
	"bytes"
	"context"
	"fmt"

	"os"
	"sync"
	"sync/atomic"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/dagger/dagger/telemetry/sdklog"
	"github.com/muesli/termenv"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
)

type frontendPlain struct {
	FrontendOpts

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

func NewPlain() Frontend {
	return &frontendPlain{
		output:      NewOutput(os.Stderr),
		done:        make(chan struct{}),
		frameTicker: time.NewTicker(50 * time.Millisecond),
	}
}

func (fe *frontendPlain) Run(ctx context.Context, opts FrontendOpts, run func(context.Context) error) error {
	fe.FrontendOpts = opts
	return run(ctx)
}

func (fe *frontendPlain) SetPrimary(spanID trace.SpanID) {}

func (fe *frontendPlain) Background(cmd tea.ExecCommand) error {
	return fmt.Errorf("not implemented")
}

func (fe *frontendPlain) Shutdown(ctx context.Context) error {
	fe.doneOnce.Do(func() {
		fe.frameTicker.Stop()
		close(fe.done)
	})

	// wait all printers outputs
	fe.batchPrinterWg.Wait()
	return nil
}

func (fe *frontendPlain) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	for _, span := range spans {
		fe.spanNames.Store(span.SpanContext().SpanID(), span.Name())
	}
	return nil
}

func (fe *frontendPlain) ExportLogs(ctx context.Context, logs []*sdklog.LogData) error {
	for _, log := range logs {
		fe.export(log)
	}
	return nil
}

func (fe *frontendPlain) getSpanName(spanID trace.SpanID) string {
	if name, ok := fe.spanNames.Load(spanID); ok {
		return name.(string)
	}
	return ""
}

func (fe *frontendPlain) export(logData *sdklog.LogData) {
	// FIXME may add GC when printer idle
	get, _ := fe.batchPrinterGetters.LoadOrStore(logData.SpanID, sync.OnceValue(func() logPrinter {
		w := &batchPrinter{
			spanID:      logData.SpanID,
			num:         atomic.AddInt64(&fe.idx, 1),
			getSpanName: fe.getSpanName,
		}

		fe.batchPrinterWg.Add(1)

		go func() {
			defer fe.batchPrinterWg.Done()

			<-fe.done

			fe.tryRender(func(out *termenv.Output) {
				w.render(out, true)
			})
		}()

		go func() {
			for range fe.frameTicker.C {
				fe.tryRender(func(out *termenv.Output) {
					w.render(out, false)
				})
			}
		}()

		return w
	}))

	get.(func() logPrinter)().PrintLog(logData)
}

func (fe *frontendPlain) tryRender(print func(out *termenv.Output)) {
	fe.outputMu.Lock()
	defer fe.outputMu.Unlock()

	print(fe.output)
}

type batchPrinter struct {
	num         int64
	spanID      trace.SpanID
	getSpanName func(spanID trace.SpanID) string

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

	spanName := w.getSpanName(w.spanID)
	if spanName == "" {
		return
	}

	lineCount := len(w.lines)

	if lineCount == 0 {
		return
	}

	if final || lineCount > 10 || time.Since(w.latestPrintAt) > time.Second {
		_, _ = fmt.Fprint(out, out.String(fmt.Sprintf("%d: ", w.num)).Foreground(termenv.ANSIBrightMagenta))
		_, _ = fmt.Fprint(out, out.String("in ").Foreground(termenv.ANSICyan))
		_, _ = fmt.Fprintf(out, "%s\n", spanName)

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
