package dagql

import (
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/trace"
)

const InstrumentationLibrary = "dagger.io/dagql"

func Tracer() trace.Tracer {
	return otel.Tracer(InstrumentationLibrary)
}
