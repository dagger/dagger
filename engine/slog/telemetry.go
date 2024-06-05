package slog

import (
	"context"
	"io"
	"log/slog"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/lmittmann/tint"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/log"
)

func SpanLogger(ctx context.Context, name string, level slog.Level, attrs ...log.KeyValue) (telemetry.SpanLogs, *Logger) {
	logs := telemetry.Logs(ctx, name, attrs...)
	profile := termenv.Ascii
	if v := ctx.Value(logProfileKey{}); v != nil {
		profile = v.(termenv.Profile)
	}
	return logs, PrettyLogger(logs.Stderr, profile, level)
}

func PrettyLogger(dest io.Writer, profile termenv.Profile, level slog.Level) *Logger {
	slogOpts := &tint.Options{
		TimeFormat: time.TimeOnly,
		NoColor:    profile == termenv.Ascii,
		Level:      level,
	}
	return New(tint.NewHandler(dest, slogOpts))
}
