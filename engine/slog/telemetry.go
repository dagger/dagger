package slog

import (
	"context"
	"io"
	"log/slog"
	"time"

	"dagger.io/dagger/telemetry"
	"github.com/lmittmann/tint"
	"github.com/muesli/termenv"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/log"
)

const (
	debugBaggageKey   = "debug"
	noColorBaggageKey = "no-color"
	globalLogsSpan    = "global-logs-span"
)

// ContextWithDebugMode enables or disables debug mode in the given context's
// OpenTelemetry baggage.
func ContextWithDebugMode(ctx context.Context, debug bool) context.Context {
	return toggleBaggage(ctx, debugBaggageKey, debug)
}

// ContextWithColorMode enables or disables color mode in the given context's
// OpenTelemetry baggage.
func ContextWithColorMode(ctx context.Context, noColor bool) context.Context {
	return toggleBaggage(ctx, noColorBaggageKey, noColor)
}

// OutputWithContextColorMode returns a termenv.Output configured according to
// the given context's OpenTelemetry baggage.
func ColorProfileFromContext(ctx context.Context) termenv.Profile {
	bag := baggage.FromContext(ctx)
	if bag.Member(noColorBaggageKey).Value() == "true" {
		return termenv.Ascii
	}
	return termenv.ANSI
}

// SpanLogger returns a Logger that writes to the give context's span logs.
//
// The logger will use the context's baggage to determine the log level and
// color profile:
//
// - If no-color=true is set in baggage, the profile will be Ascii. Otherwise,
// it will be ANSI.
//
// - If debug=true is set in baggage, the log level will be Debug. Otherwise,
// it will be Info.
func SpanLogger(ctx context.Context, name string, attrs ...log.KeyValue) *Logger {
	bag := baggage.FromContext(ctx)
	profile := termenv.ANSI
	if bag.Member(noColorBaggageKey).Value() == "true" {
		profile = termenv.Ascii
	}
	level := LevelInfo
	if bag.Member(debugBaggageKey).Value() == "true" {
		level = LevelDebug
	}
	return PrettyLogger(
		telemetry.NewWriter(ctx, name, attrs...),
		profile,
		level,
	)
}

// GlobalLogger returns a Logger that sends logs to the global span, or the
// current span if none is configured.
func GlobalLogger(ctx context.Context, name string, attrs ...log.KeyValue) *Logger {
	return SpanLogger(telemetry.GlobalLogsSpanContext(ctx), name, attrs...)
}

func PrettyLogger(dest io.Writer, profile termenv.Profile, level slog.Level) *Logger {
	slogOpts := &tint.Options{
		TimeFormat: time.TimeOnly,
		NoColor:    profile == termenv.Ascii,
		Level:      level,
	}
	return New(tint.NewHandler(dest, slogOpts))
}

func toggleBaggage(ctx context.Context, key string, value bool) context.Context {
	bag := baggage.FromContext(ctx)
	if value {
		m, err := baggage.NewMember(key, "true")
		if err != nil {
			// value would have to be invalid; 'true' is fine
			panic(err)
		}
		bag, err = bag.SetMember(m)
		if err != nil {
			// member would have to be invalid, but it ain't
			panic(err)
		}
	} else {
		bag = bag.DeleteMember(key)
	}
	return baggage.ContextWithBaggage(ctx, bag)
}
