package client

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const InstrumentationLibrary = "dagger.io/engine.client"

func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationLibrary)
}
