package telemetry

import (
	"context"
	"errors"
	"io"
	"time"

	"go.opentelemetry.io/otel/log"
)

// Logger returns a logger with the given name.
func Logger(name string) log.Logger {
	return loggerProvider.Logger(name) // TODO more instrumentation attrs
}

type SpanLogs struct {
	Stdout io.WriteCloser
	Stderr io.WriteCloser
}

func (sl SpanLogs) Close() error {
	return errors.Join(
		sl.Stdout.Close(),
		sl.Stderr.Close(),
	)
}

// Logs returns a pair of io.WriteClosers which will send log records with
// stdio.stream=1 for stdout and stdio.stream=2 for stderr. Closing either of
// them will send a log record with an empty body and stdio.eof=true.
//
// A typical pattern is to start a new span, call Logs, and defer logs.Close().
// If a function does not create its own span, it should not call Close().
func Logs(ctx context.Context, name string, attrs ...log.KeyValue) SpanLogs {
	logger := Logger(name)
	return SpanLogs{
		Stdout: &logStream{
			ctx:    ctx,
			logger: logger,
			stream: 1,
			attrs:  attrs,
		},
		Stderr: &logStream{
			ctx:    ctx,
			logger: logger,
			stream: 2,
			attrs:  attrs,
		},
	}
}

type logStream struct {
	ctx    context.Context
	logger log.Logger
	stream int
	attrs  []log.KeyValue
}

func (w *logStream) Write(p []byte) (int, error) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	rec.SetBody(log.StringValue(string(p)))
	rec.AddAttributes(log.Int(StdioStreamAttr, w.stream))
	rec.AddAttributes(w.attrs...)
	w.logger.Emit(w.ctx, rec)
	return len(p), nil
}

func (w *logStream) Close() error {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	rec.SetBody(log.StringValue(""))
	rec.AddAttributes(log.Int(StdioStreamAttr, w.stream), log.Bool(StdioEOFAttr, true))
	rec.AddAttributes(w.attrs...)
	w.logger.Emit(w.ctx, rec)
	return nil
}
