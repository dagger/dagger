package jaeger

import (
	"context"
	"net"
	"os"
	"strings"
	"sync"

	"github.com/moby/buildkit/util/tracing/detect"
	//nolint:staticcheck // Jaeger still supported for compatibility
	"go.opentelemetry.io/otel/exporters/jaeger"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

func init() {
	detect.Register("jaeger", detect.TraceExporterDetector(jaegerExporter), 11)
}

func jaegerExporter() (sdktrace.SpanExporter, error) {
	set := os.Getenv("OTEL_TRACES_EXPORTER") == "jaeger" || os.Getenv("JAEGER_TRACE") != "" || os.Getenv("OTEL_EXPORTER_JAEGER_AGENT_HOST") != "" || os.Getenv("OTEL_EXPORTER_JAEGER_ENDPOINT") != ""
	if !set {
		return nil, nil
	}

	endpoint := envOr("OTEL_EXPORTER_JAEGER_ENDPOINT", "http://localhost:14250")
	host := envOr("OTEL_EXPORTER_JAEGER_HOST", "localhost")
	port := envOr("OTEL_EXPORTER_JAEGER_PORT", "6831")
	var isEndpoint bool

	// JAEGER_TRACE is not env defined by opentelemetry spec but buildkit backward compatibility
	if v := os.Getenv("JAEGER_TRACE"); v != "" {
		if strings.HasPrefix(v, "http://") || strings.HasPrefix(v, "https://") {
			isEndpoint = true
			endpoint = v
		} else {
			h, p, err := net.SplitHostPort(v)
			if err != nil {
				return nil, err
			}
			host = h
			port = p
		}
	} else {
		isEndpoint = os.Getenv("OTEL_EXPORTER_JAEGER_ENDPOINT") != ""
	}

	epo := jaeger.WithCollectorEndpoint(jaeger.WithEndpoint(endpoint))

	if !isEndpoint {
		epo = jaeger.WithAgentEndpoint(jaeger.WithAgentHost(host), jaeger.WithAgentPort(port))
	}

	exp, err := jaeger.New(epo)
	if err != nil {
		return nil, err
	}

	return &threadSafeExporterWrapper{
		exporter: exp,
	}, nil
}

func envOr(key, defaultValue string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return defaultValue
}

// We've received reports that the Jaeger exporter is not thread-safe,
// so wrap it in a mutex.
type threadSafeExporterWrapper struct {
	mu       sync.Mutex
	exporter sdktrace.SpanExporter
}

func (tse *threadSafeExporterWrapper) ExportSpans(ctx context.Context, spans []sdktrace.ReadOnlySpan) error {
	tse.mu.Lock()
	defer tse.mu.Unlock()
	return tse.exporter.ExportSpans(ctx, spans)
}

func (tse *threadSafeExporterWrapper) Shutdown(ctx context.Context) error {
	tse.mu.Lock()
	defer tse.mu.Unlock()
	return tse.exporter.Shutdown(ctx)
}
