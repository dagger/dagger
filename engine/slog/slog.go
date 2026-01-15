package slog

import (
	"bytes"
	"context"
	"io"
	"log/slog"
)

const (
	// standard levels
	LevelDebug = slog.LevelDebug
	LevelInfo  = slog.LevelInfo
	LevelWarn  = slog.LevelWarn
	LevelError = slog.LevelError

	// custom levels

	// LevelExtraDebug is between Debug and Trace. It had extra information that's useful sometimes
	// but not as overwhelming as Trace. e.g. OTel logging is at this level.
	LevelExtraDebug = slog.LevelDebug - 1

	// Trace is the most verbose level. It includes session logging and an enormous amount of detail from
	// buildkit on cache refs and cache queries.
	LevelTrace = slog.LevelDebug - 2
)

type Level = slog.Level

// Logger wraps the slog.Logger type with support for a few additional levels
type Logger struct {
	*slog.Logger
}

func (l *Logger) With(args ...any) *Logger {
	return &Logger{l.Logger.With(args...)}
}

func (l *Logger) ExtraDebug(msg string, args ...any) {
	l.Log(context.Background(), LevelExtraDebug, msg, args...)
}

func (l *Logger) ExtraDebugContext(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, LevelExtraDebug, msg, args...)
}

func (l *Logger) Trace(msg string, args ...any) {
	l.Log(context.Background(), LevelTrace, msg, args...)
}

func (l *Logger) TraceContext(ctx context.Context, msg string, args ...any) {
	l.Log(ctx, LevelTrace, msg, args...)
}

// LineWriter wraps a Logger and writes each line as a log message at the specified level.
// It buffers partial lines until a newline is received.
type LineWriter struct {
	logger *Logger
	level  Level
	buf    []byte
}

// NewLineWriter creates an io.Writer that logs each line at the specified level.
func NewLineWriter(logger *Logger, level Level) *LineWriter {
	return &LineWriter{
		logger: logger,
		level:  level,
	}
}

func (w *LineWriter) Write(p []byte) (n int, err error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		w.logger.Log(context.Background(), w.level, line)
	}
	return len(p), nil
}

func Default() *Logger { return &Logger{slog.Default()} }

// ctxKey is the context key for storing a Logger.
type ctxKey struct{}

// WithLogger returns a new context with the given logger stored in it.
func WithLogger(ctx context.Context, logger *Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// FromContext returns the Logger stored in the context, or the default logger if none is set.
func FromContext(ctx context.Context) *Logger {
	if logger, ok := ctx.Value(ctxKey{}).(*Logger); ok {
		return logger
	}
	if logger, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return &Logger{logger}
	}
	return Default()
}

func SetDefault(l *Logger) {
	slog.SetDefault(l.Logger)
}

func New(h slog.Handler) *Logger {
	return &Logger{slog.New(h)}
}

func With(args ...any) *Logger {
	return Default().With(args...)
}

func Debug(msg string, args ...any) {
	Default().Debug(msg, args...)
}

func DebugContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).DebugContext(ctx, msg, args...)
}

func Info(msg string, args ...any) {
	Default().Info(msg, args...)
}

func InfoContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).InfoContext(ctx, msg, args...)
}

func Warn(msg string, args ...any) {
	Default().Warn(msg, args...)
}

func WarnContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).WarnContext(ctx, msg, args...)
}

func Error(msg string, args ...any) {
	Default().Error(msg, args...)
}

func ErrorContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).ErrorContext(ctx, msg, args...)
}

func ExtraDebug(msg string, args ...any) {
	Default().ExtraDebug(msg, args...)
}

func ExtraDebugContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).ExtraDebugContext(ctx, msg, args...)
}

func Trace(msg string, args ...any) {
	Default().Trace(msg, args...)
}

func TraceContext(ctx context.Context, msg string, args ...any) {
	FromContext(ctx).TraceContext(ctx, msg, args...)
}

func NewTextHandler(w io.Writer, opts *slog.HandlerOptions) *slog.TextHandler {
	return slog.NewTextHandler(w, opts)
}

type HandlerOptions = slog.HandlerOptions

func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) *slog.JSONHandler {
	return slog.NewJSONHandler(w, opts)
}
