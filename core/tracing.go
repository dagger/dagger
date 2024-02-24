package core

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const InstrumentationLibrary = "dagger.io/core"

func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationLibrary)
}
