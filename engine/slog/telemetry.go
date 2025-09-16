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
	"go.opentelemetry.io/otel/trace"
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

// ContextWithGlobalSpan makes the current span the target for global logs, by
// storing it in OpenTelemetry baggage.
func ContextWithGlobalSpan(ctx context.Context) context.Context {
	bag := baggage.FromContext(ctx)
	m, err := baggage.NewMember(globalLogsSpan,
		trace.SpanContextFromContext(ctx).SpanID().String())
	if err != nil {
		// value would have to be invalid, but it ain't
		panic(err)
	}
	bag, err = bag.SetMember(m)
	if err != nil {
		// member would have to be invalid, but it ain't
		panic(err)
	}
	return baggage.ContextWithBaggage(ctx, bag)
}

// GlobalLogger returns a Logger that sends logs to the global span, or the
// current span if none is configured.
func GlobalLogger(ctx context.Context, name string, attrs ...log.KeyValue) *Logger {
	bag := baggage.FromContext(ctx)
	spanCtx := trace.SpanContextFromContext(ctx)
	if spanIDHex := bag.Member(globalLogsSpan).Value(); spanIDHex != "" {
		spanID, err := trace.SpanIDFromHex(spanIDHex)
		if err != nil {
			slog.Warn("invalid span ID hex for global logs", "spanIDHex", spanIDHex, "error", err)
		} else {
			spanCtx = spanCtx.WithSpanID(spanID)
			ctx = trace.ContextWithSpanContext(ctx, spanCtx)
		}
	}
	return SpanLogger(ctx, name, attrs...)
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
