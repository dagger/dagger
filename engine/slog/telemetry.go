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

type logProfileKey struct{}

func WithLogProfile(ctx context.Context, profile termenv.Profile) context.Context {
	return context.WithValue(ctx, logProfileKey{}, profile)
}

// SpanLogger returns a Logger that writes to the give context's span logs.
func SpanLogger(ctx context.Context, name string, level slog.Level, attrs ...log.KeyValue) *Logger {
	profile := termenv.ANSI
	if v := ctx.Value(logProfileKey{}); v != nil {
		profile = v.(termenv.Profile)
	}
	return PrettyLogger(
		telemetry.NewWriter(ctx, name, attrs...),
		profile,
		level,
	)
}

func PrettyLogger(dest io.Writer, profile termenv.Profile, level slog.Level) *Logger {
	slogOpts := &tint.Options{
		TimeFormat: time.TimeOnly,
		NoColor:    profile == termenv.Ascii,
		Level:      level,
	}
	return New(tint.NewHandler(dest, slogOpts))
}
