package slog

import (
	"context"
	"io"
	"log/slog"

	"github.com/muesli/termenv"
)

type logProfileKey struct{}

func WithLogProfile(ctx context.Context, profile termenv.Profile) context.Context {
	return context.WithValue(ctx, logProfileKey{}, profile)
}

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

func Default() *Logger { return &Logger{slog.Default()} }

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
	Default().DebugContext(ctx, msg, args...)
}

func Info(msg string, args ...any) {
	Default().Info(msg, args...)
}

func InfoContext(ctx context.Context, msg string, args ...any) {
	Default().InfoContext(ctx, msg, args...)
}

func Warn(msg string, args ...any) {
	Default().Warn(msg, args...)
}

func WarnContext(ctx context.Context, msg string, args ...any) {
	Default().WarnContext(ctx, msg, args...)
}

func Error(msg string, args ...any) {
	Default().Error(msg, args...)
}

func ErrorContext(ctx context.Context, msg string, args ...any) {
	Default().ErrorContext(ctx, msg, args...)
}

func ExtraDebug(msg string, args ...any) {
	Default().ExtraDebug(msg, args...)
}

func ExtraDebugContext(ctx context.Context, msg string, args ...any) {
	Default().ExtraDebugContext(ctx, msg, args...)
}

func Trace(msg string, args ...any) {
	Default().Trace(msg, args...)
}

func TraceContext(ctx context.Context, msg string, args ...any) {
	Default().TraceContext(ctx, msg, args...)
}

func NewTextHandler(w io.Writer, opts *slog.HandlerOptions) *slog.TextHandler {
	return slog.NewTextHandler(w, opts)
}

func NewJSONHandler(w io.Writer, opts *slog.HandlerOptions) *slog.JSONHandler {
	return slog.NewJSONHandler(w, opts)
}
