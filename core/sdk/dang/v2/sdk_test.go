package dangv2

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	telemetry "github.com/dagger/otel-go"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel/codes"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// TestCheckDangModuleOwnsItsSourceErrors covers a dependency whose Dang
// source doesn't type-check, read while its dependent's types are: the
// dependency's report is its own check's logs, and its error originates
// there, not on its dependent's check, which only passes it on.
func TestCheckDangModuleOwnsItsSourceErrors(t *testing.T) {
	t.Parallel()

	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	logs := &recordedLogs{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logs)))
	ctx, load := tp.Tracer("test").Start(telemetry.WithLoggerProvider(t.Context(), lp), "load module")

	var dep trace.SpanContext
	err := checkDangModule(ctx, "dependent", func(ctx context.Context) error {
		return checkDangModule(ctx, "dep", func(ctx context.Context) error {
			dep = trace.SpanContextFromContext(ctx)
			stdio := userFacingStdio(ctx)
			defer stdio.Close()
			return reportDangSourceError(stdio.Stderr, inferReport(errors.New("unknown type Bogus")))
		})
	})
	load.End()
	require.Error(t, err)

	origins := telemetry.ParseErrorOrigins(err.Error())
	require.Len(t, origins, 1)
	require.Equal(t, dep.SpanID(), origins[0].SpanID(), "the error originates on the dependency's check")

	var reported bool
	for _, rec := range logs.all() {
		if strings.Contains(rec.body, "unknown type Bogus") {
			reported = true
			require.Equal(t, dep.SpanID(), rec.span, "the report is the dependency's check's")
		}
	}
	require.True(t, reported, "the report is logged")

	ended := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range spans.Ended() {
		ended[span.Name()] = span
	}
	require.Equal(t, codes.Error, ended["check dep"].Status().Code)
	require.Equal(t, codes.Error, ended["check dependent"].Status().Code)
	links := ended["check dependent"].Links()
	require.Len(t, links, 1, "the dependent's check points at the dependency's")
	require.Equal(t, dep.SpanID(), links[0].SpanContext.SpanID())
}

// recordedLogs is a log exporter that keeps what it exports.
type recordedLogs struct {
	mu      sync.Mutex
	records []recordedLog
}

type recordedLog struct {
	span trace.SpanID
	body string
}

func (r *recordedLogs) Export(_ context.Context, records []sdklog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, rec := range records {
		r.records = append(r.records, recordedLog{span: rec.SpanID(), body: rec.Body().AsString()})
	}
	return nil
}

func (r *recordedLogs) Shutdown(context.Context) error   { return nil }
func (r *recordedLogs) ForceFlush(context.Context) error { return nil }

func (r *recordedLogs) all() []recordedLog {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]recordedLog(nil), r.records...)
}
