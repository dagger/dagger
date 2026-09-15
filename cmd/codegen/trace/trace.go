package trace

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const InstrumentationLibrary = "dagger.io/codegen"

func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationLibrary)
}
