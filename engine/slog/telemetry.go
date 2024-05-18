package slog

import (
	"context"
	"io"
	"log/slog"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/dagql/ioctx"
	"github.com/lmittmann/tint"
	"github.com/muesli/termenv"
)

func PrettyLogger(dest io.Writer, profile termenv.Profile, level slog.Level) *Logger {
	slogOpts := &tint.Options{
		TimeFormat: time.TimeOnly,
		NoColor:    profile == termenv.Ascii,
		Level:      level,
	}
	return New(tint.NewHandler(dest, slogOpts))
}

const (
	// We use this to identify which logs should be bubbled up to the user
	// "globally" regardless of which span they came from.
	GlobalLogs = "dagger.io/global"
)

func GlobalLogger(ctx context.Context) *Logger {
	profile := termenv.Ascii
	if v := ctx.Value(logProfileKey{}); v != nil {
		profile = v.(termenv.Profile)
	}
	logW := &telemetry.OTelWriter{
		Ctx:    ctx,
		Logger: telemetry.Logger(GlobalLogs),
		Stream: 2,
	}
	return PrettyLogger(logW, profile, slog.LevelDebug)
}

func SpanLogger(ctx context.Context, name string, level slog.Level) *Logger {
	_, _, stderr := WithStdioToOTel(ctx, name)
	profile := termenv.Ascii
	if v := ctx.Value(logProfileKey{}); v != nil {
		profile = v.(termenv.Profile)
	}
	return PrettyLogger(stderr, profile, level)
}

func ContextLogger(ctx context.Context, level slog.Level) *Logger {
	profile := termenv.Ascii
	if v := ctx.Value(logProfileKey{}); v != nil {
		profile = v.(termenv.Profile)
	}
	return PrettyLogger(ioctx.Stderr(ctx), profile, level)
}

func WithStdioToOTel(ctx context.Context, name string) (context.Context, io.Writer, io.Writer) {
	logger := telemetry.Logger(name)
	stdout, stderr := &telemetry.OTelWriter{
		Ctx:    ctx,
		Logger: logger,
		Stream: 1,
	}, &telemetry.OTelWriter{
		Ctx:    ctx,
		Logger: logger,
		Stream: 2,
	}
	ctx = ioctx.WithStdout(ctx, stdout)
	ctx = ioctx.WithStderr(ctx, stderr)
	return ctx, stdout, stderr
}
