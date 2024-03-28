package telemetry

import (
	"context"
	"io"
	"log/slog"
	"time"

	"github.com/lmittmann/tint"
	"go.opentelemetry.io/otel/log"

	"github.com/dagger/dagger/dagql/ioctx"
)

type OtelWriter struct {
	Ctx    context.Context
	Logger log.Logger
	Stream int
}

func ContextLogger(ctx context.Context, level slog.Level) *slog.Logger {
	return PrettyLogger(ioctx.Stderr(ctx), level)
}

func PrettyLogger(dest io.Writer, level slog.Level) *slog.Logger {
	slogOpts := &tint.Options{
		TimeFormat: time.TimeOnly,
		NoColor:    false,
		Level:      level,
	}
	return slog.New(tint.NewHandler(dest, slogOpts))
}

const (
	// We use this to identify which logs should be bubbled up to the user
	// "globally" regardless of which span they came from.
	GlobalLogs = "dagger.io/global"
)

func GlobalLogger(ctx context.Context) *slog.Logger {
	logW := &OtelWriter{
		Ctx:    ctx,
		Logger: Logger(GlobalLogs),
		Stream: 2,
	}
	return PrettyLogger(logW, slog.LevelDebug)
}

func WithStdioToOtel(ctx context.Context, name string) (context.Context, io.Writer, io.Writer) {
	logger := Logger(name)
	stdout, stderr := &OtelWriter{
		Ctx:    ctx,
		Logger: logger,
		Stream: 1,
	}, &OtelWriter{
		Ctx:    ctx,
		Logger: logger,
		Stream: 2,
	}
	ctx = ioctx.WithStdout(ctx, stdout)
	ctx = ioctx.WithStderr(ctx, stderr)
	return ctx, stdout, stderr
}

const (
	LogStreamAttr = "log.stream"
	LogDataAttr   = "log.data"
)

func (w *OtelWriter) Write(p []byte) (int, error) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	rec.SetBody(log.StringValue(string(p)))
	rec.AddAttributes(log.Int(LogStreamAttr, w.Stream))
	w.Logger.Emit(w.Ctx, rec)
	return len(p), nil
}
