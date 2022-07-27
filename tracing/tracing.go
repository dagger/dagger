package tracing

import (
	"context"
	"io"
	"os"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/jaeger"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
)

var Tracer = otel.Tracer("cloak")

func Init() io.Closer {
	traceEndpoint := os.Getenv("OTEL_EXPORTER_JAEGER_ENDPOINT")
	if traceEndpoint == "" {
		return &nopCloser{}
	}

	tp, err := tracerProvider(traceEndpoint)
	if err != nil {
		panic(err)
	}

	// Register our TracerProvider as the global so any imported
	// instrumentation in the future will default to using it.
	otel.SetTracerProvider(tp)

	closer := providerCloser{
		TracerProvider: tp,
	}

	return closer
}

// tracerProvider returns an OpenTelemetry TracerProvider configured to use
// the Jaeger exporter that will send spans to the provided url. The returned
// TracerProvider will also use a Resource configured with all the information
// about the application.
func tracerProvider(url string) (*tracesdk.TracerProvider, error) {
	// Create the Jaeger exporter
	exp, err := jaeger.New(jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(url)))
	if err != nil {
		return nil, err
	}
	tp := tracesdk.NewTracerProvider(
		// Always be sure to batch in production.
		tracesdk.WithBatcher(exp, tracesdk.WithMaxExportBatchSize(1)),
		// Record information about this application in an Resource.
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNameKey.String("cloak"),
		)),
	)
	return tp, nil
}

type providerCloser struct {
	*tracesdk.TracerProvider
}

func (t providerCloser) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second*5)
	defer cancel()
	return t.Shutdown(ctx)
}

type nopCloser struct {
}

func (*nopCloser) Close() error {
	return nil
}
