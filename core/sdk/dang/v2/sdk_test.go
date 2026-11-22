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

// TestTypecheckDangModuleOwnsItsSourceErrors covers a dependency whose Dang
// source doesn't type-check, loaded for its dependent: a module's
// dependencies load before its own types are read, so the dependent's load
// fails with the dependency's typecheck's error, and its own typecheck never
// runs. The dependency's report is its typecheck's logs, and its error
// originates there, for the dependent's load and for any other load the
// failed pass is shared with, whose spans only pass it on.
func TestTypecheckDangModuleOwnsItsSourceErrors(t *testing.T) {
	t.Parallel()

	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	logs := &recordedLogs{}
	lp := sdklog.NewLoggerProvider(sdklog.WithProcessor(sdklog.NewSimpleProcessor(logs)))
	ctx := telemetry.WithLoggerProvider(t.Context(), lp)
	tracer := tp.Tracer("test")
	// load is a span of the engine's around the pass, as a module's load
	// and its dependencies' loads are.
	load := func(ctx context.Context, name string, fn func(context.Context) error) (rerr error) {
		ctx, span := tracer.Start(ctx, name)
		defer telemetry.EndWithCause(span, &rerr)
		return fn(ctx)
	}

	var dep trace.SpanContext
	err := load(ctx, "load module: ./dependent", func(ctx context.Context) error {
		return load(ctx, "load dep modules", func(ctx context.Context) error {
			return typecheckDangModule(ctx, "dep", func(ctx context.Context) error {
				dep = trace.SpanContextFromContext(ctx)
				stdio := userFacingStdio(ctx)
				defer stdio.Close()
				return reportDangSourceError(stdio.Stderr, inferReport(errors.New("unknown type Bogus")))
			})
		})
	})
	require.Error(t, err)
	// Another load that joins the failed pass gets its error as it is.
	shared := err
	require.Error(t, load(ctx, "load module: ./other", func(context.Context) error { return shared }))

	origins := telemetry.ParseErrorOrigins(err.Error())
	require.Len(t, origins, 1)
	require.Equal(t, dep.SpanID(), origins[0].SpanID(), "the error originates on the dependency's typecheck")

	var reported bool
	for _, rec := range logs.all() {
		if strings.Contains(rec.body, "unknown type Bogus") {
			reported = true
			require.Equal(t, dep.SpanID(), rec.span, "the report is the dependency's typecheck's")
		}
	}
	require.True(t, reported, "the report is logged")

	ended := map[string]sdktrace.ReadOnlySpan{}
	for _, span := range spans.Ended() {
		ended[span.Name()] = span
	}
	require.Equal(t, codes.Error, ended["typecheck dep"].Status().Code)
	require.Empty(t, ended["typecheck dep"].Links(), "the dependency's typecheck is the origin")
	for _, name := range []string{"load dep modules", "load module: ./dependent", "load module: ./other"} {
		require.Equal(t, codes.Error, ended[name].Status().Code, name)
		links := ended[name].Links()
		require.Len(t, links, 1, "%s points at the dependency's typecheck", name)
		require.Equal(t, dep.SpanID(), links[0].SpanContext.SpanID(), name)
	}
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
