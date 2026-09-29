package telemetry

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/dagger/dagger/engine"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const workloadExportTimeout = time.Minute

// WorkloadExport owns bounded in-memory queues across all sessions. A session
// can flush its readings into the queue, but cannot wait on or close the
// export. Receiver acceptance is not an engine-side durability guarantee.
type WorkloadExport struct {
	spans    sdktrace.SpanProcessor
	metrics  *asyncMetricExporter
	resource *resource.Resource
	interval time.Duration
}

// NewWorkloadExport is called only when telemetry.workloadExport is enabled.
// The signals and fields are fixed: every engine span with a fixed set of
// fields, and workload readings; never logs, received SDK telemetry, or
// engine-wide resource metrics. Standard SDK exporters read endpoints, headers,
// TLS, compression, and timeouts from the OTLP environment. Signal-specific
// settings take precedence over common ones.
func NewWorkloadExport(ctx context.Context, engineInstanceID string) (_ *WorkloadExport, rerr error) {
	traceProtocol, err := workloadExportProtocol("TRACES")
	if err != nil {
		return nil, err
	}
	metricProtocol, err := workloadExportProtocol("METRICS")
	if err != nil {
		return nil, err
	}
	interval, err := workloadSampleInterval()
	if err != nil {
		return nil, err
	}
	d := &WorkloadExport{interval: interval, resource: workloadResource(ctx, engineInstanceID)}
	defer func() {
		if rerr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = d.Shutdown(ctx)
		}
	}()
	var spans sdktrace.SpanExporter
	if traceProtocol == "grpc" {
		spans, err = otlptracegrpc.New(ctx)
	} else {
		spans, err = otlptracehttp.New(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	// Session processors reduce and copy ended spans before this queue.
	d.spans = sdktrace.NewBatchSpanProcessor(spans,
		sdktrace.WithMaxQueueSize(LargeSpanQueueSize), sdktrace.WithMaxExportBatchSize(512),
		sdktrace.WithBatchTimeout(telemetry.NearlyImmediate), sdktrace.WithExportTimeout(workloadExportTimeout))
	var metrics sdkmetric.Exporter
	if metricProtocol == "grpc" {
		metrics, err = otlpmetricgrpc.New(ctx)
	} else {
		metrics, err = otlpmetrichttp.New(ctx)
	}
	if err != nil {
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}
	d.metrics = newAsyncMetricExporter(metrics, LargeSpanQueueSize)
	return d, nil
}

func workloadExportProtocol(signal string) (string, error) {
	prefix := "OTEL_EXPORTER_OTLP_" + signal
	// Do not silently send to the SDK's localhost default when only one signal
	// has been configured, as with the existing engine resource export.
	if os.Getenv(prefix+"_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" {
		return "", fmt.Errorf("telemetry.workloadExport requires %s_ENDPOINT or OTEL_EXPORTER_OTLP_ENDPOINT", prefix)
	}
	protocol := os.Getenv(prefix + "_PROTOCOL")
	if protocol == "" {
		protocol = os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL")
	}
	switch protocol {
	case "", "http/protobuf", "grpc":
		return protocol, nil
	default:
		return "", fmt.Errorf("unsupported workload %s protocol %q", signal, protocol)
	}
}

func workloadSampleInterval() (time.Duration, error) {
	value := os.Getenv("OTEL_METRIC_EXPORT_INTERVAL")
	if value == "" {
		return time.Second, nil
	}
	ms, err := strconv.ParseInt(value, 10, 64)
	if err != nil || ms < 100 || ms > 60000 {
		return 0, errors.New("OTEL_METRIC_EXPORT_INTERVAL must be between 100 and 60000 milliseconds for workload export")
	}
	return time.Duration(ms) * time.Millisecond, nil
}

func (d *WorkloadExport) SampleInterval() time.Duration { return d.interval }

func (d *WorkloadExport) SessionResource(sessionID string) *resource.Resource {
	return resource.NewSchemaless(append(d.resource.Attributes(), attribute.String(SessionIDAttr, sessionID))...)
}

func (d *WorkloadExport) SpanProcessor(sessionID string) sdktrace.SpanProcessor {
	if d == nil || d.spans == nil {
		return nil
	}
	return &workloadSpanProcessor{next: d.spans, resource: d.SessionResource(sessionID)}
}

func (d *WorkloadExport) MetricExporter() sdkmetric.Exporter {
	if d == nil || d.metrics == nil {
		return nil
	}
	return SharedMetricExporter{Exporter: d.metrics}
}

func (d *WorkloadExport) Shutdown(ctx context.Context) error {
	if d == nil {
		return nil
	}
	var shutdowns []func(context.Context) error
	if d.spans != nil {
		shutdowns = append(shutdowns, d.spans.Shutdown)
	}
	if d.metrics != nil {
		shutdowns = append(shutdowns, d.metrics.Shutdown)
	}
	results := make(chan error, len(shutdowns))
	for _, shutdown := range shutdowns {
		go func() { results <- shutdown(ctx) }()
	}
	var errs error
	for range shutdowns {
		select {
		case err := <-results:
			errs = errors.Join(errs, err)
		case <-ctx.Done():
			return errors.Join(errs, ctx.Err())
		}
	}
	return errs
}

const (
	EngineInstanceAttr = "dagger.io/engine.instance.id"
	SessionIDAttr      = "dagger.io/session.id"
)

// workloadResource belongs only to the workload export. Do not enrich
// ordinary session resources or trust identity posted by a workload. Invalid
// environment entries must not prevent startup or discard valid identity.
func workloadResource(ctx context.Context, instance string) *resource.Resource {
	res, err := resource.New(ctx, resource.WithFromEnv())
	if err != nil {
		slog.Warn("incomplete workload export identity", "error", err)
	}
	attrs := []attribute.KeyValue{
		attribute.String("service.name", "dagger-engine"),
		attribute.String("service.version", engine.Version),
		attribute.String("service.instance.id", instance),
		attribute.String(EngineInstanceAttr, instance),
		attribute.String("dagger.io/workload_export.schema", "1"),
	}
	for _, attr := range res.Attributes() {
		switch string(attr.Key) {
		case "dagger.io/engine.id", "dagger.io/provider.instance.id", "dagger.io/organization.id":
			attrs = append(attrs, attr)
		}
	}
	return resource.NewSchemaless(attrs...)
}
