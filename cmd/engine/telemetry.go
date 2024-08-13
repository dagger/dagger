package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/moby/buildkit/identity"
	"github.com/sirupsen/logrus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/resource"
	semconv "go.opentelemetry.io/otel/semconv/v1.24.0"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/engine"
)

const (
	InstrumentationScopeName = "dagger.io/engine"
)

var (
	engineName string
)

func init() {
	var ok bool
	engineName, ok = os.LookupEnv(engine.DaggerNameEnv)
	if !ok {
		// use the hostname
		hostname, err := os.Hostname()
		if err != nil {
			engineName = "rand-" + identity.NewID() // random ID as a fallback
		} else {
			engineName = hostname
		}
	}
}

func InitTelemetry(ctx context.Context) context.Context {
	otelResource, err := resource.New(ctx,
		resource.WithHost(),
		resource.WithAttributes(
			semconv.ServiceNameKey.String("dagger-engine"),
			semconv.ServiceVersionKey.String(engine.Version),
			attribute.String("dagger.io/engine.name", engineName),
		),
	)
	if err != nil {
		slog.Error("failed to create OTel resource", "error", err)
		return ctx
	}

	ctx = telemetry.Init(ctx, telemetry.Config{
		Resource: otelResource,
	})

	// send engine logs to OTel. logrus is the globally used logger; bklog
	// also sends to it.
	logrus.AddHook(&otelLogrusHook{
		logger: telemetry.LoggerProvider(ctx).Logger(InstrumentationScopeName),
	})

	return ctx
}
