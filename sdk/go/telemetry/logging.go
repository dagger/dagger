package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/log"
)

// Logger returns a logger with the given name.
func Logger(name string) log.Logger {
	return loggerProvider.Logger(name) // TODO more instrumentation attrs
}

const (
	LogStreamAttr = "log.stream"
	LogDataAttr   = "log.data"
)

type OTelWriter struct {
	Ctx        context.Context
	Logger     log.Logger
	Stream     int
	Attributes []log.KeyValue
}

func (w *OTelWriter) Write(p []byte) (int, error) {
	rec := log.Record{}
	rec.SetTimestamp(time.Now())
	rec.SetBody(log.StringValue(string(p)))
	rec.AddAttributes(append(w.Attributes, log.Int(LogStreamAttr, w.Stream))...)
	w.Logger.Emit(w.Ctx, rec)
	return len(p), nil
}
