package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	telemetry "github.com/dagger/otel-go"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const otlpExportTimeout = time.Minute

// OTLPOptions configures the internal operation destination. Its signals and
// data fields are fixed: selected engine spans and workload CPU/memory readings,
// never logs, received SDK telemetry, or engine-wide resource metrics.
// Standard SDK exporters own transport retries, TLS, and compression.
type OTLPOptions struct {
	Endpoint         string
	Protocol         string
	Headers          map[string]string
	QueueSize        int
	SampleInterval   time.Duration
	EngineInstanceID string
}

// OTLPDestination owns bounded in-memory queues across all sessions. A session
// can flush its observations into the queue, but cannot wait on or close the
// destination. Receiver acceptance is not an engine-side durability guarantee.
type OTLPDestination struct {
	spans    sdktrace.SpanProcessor
	metrics  *asyncMetricExporter
	resource *resource.Resource
	interval time.Duration
}

func (opts *OTLPOptions) resolve() (*url.URL, error) {
	u, err := url.Parse(opts.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, errors.New("telemetry.otlp.endpoint must be an http(s) URL without credentials, query, or fragment")
	}
	if opts.Protocol == "" {
		opts.Protocol = "http/protobuf"
	}
	if opts.Protocol != "http/protobuf" && opts.Protocol != "grpc" {
		return nil, fmt.Errorf("unsupported telemetry.otlp.protocol %q", opts.Protocol)
	}
	if opts.Protocol == "grpc" && u.Path != "" && u.Path != "/" {
		return nil, errors.New("telemetry.otlp.endpoint must not have a path for grpc")
	}
	if opts.QueueSize == 0 {
		opts.QueueSize = LargeSpanQueueSize
	}
	if opts.QueueSize < 1 || opts.QueueSize > 1048576 {
		return nil, errors.New("telemetry.otlp.queueSize must be between 1 and 1048576")
	}
	if opts.SampleInterval == 0 {
		opts.SampleInterval = time.Second
	}
	if opts.SampleInterval < 100*time.Millisecond || opts.SampleInterval > time.Minute {
		return nil, errors.New("telemetry.otlp.sampleIntervalMs must be between 100 and 60000")
	}
	if opts.EngineInstanceID == "" {
		opts.EngineInstanceID = uuid.NewString()
	}
	return u, nil
}

func NewOTLPDestination(ctx context.Context, opts OTLPOptions) (_ *OTLPDestination, rerr error) {
	u, err := opts.resolve()
	if err != nil {
		return nil, err
	}
	d := &OTLPDestination{interval: opts.SampleInterval, resource: operationResource(ctx, opts.EngineInstanceID)}
	defer func() {
		if rerr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = d.Shutdown(ctx)
		}
	}()
	var spans sdktrace.SpanExporter
	if opts.Protocol == "grpc" {
		spans, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(u.String()), otlptracegrpc.WithHeaders(opts.Headers), otlptracegrpc.WithTimeout(otlpExportTimeout))
	} else {
		spans, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(u.JoinPath("v1", "traces").String()), otlptracehttp.WithHeaders(opts.Headers), otlptracehttp.WithTimeout(otlpExportTimeout))
	}
	if err != nil {
		return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
	}
	// Session processors select and freeze records before this queue. Do not
	// coalesce live/completed snapshots: both boundaries must survive a batch.
	d.spans = sdktrace.NewBatchSpanProcessor(spans,
		sdktrace.WithMaxQueueSize(opts.QueueSize), sdktrace.WithMaxExportBatchSize(min(opts.QueueSize, 512)),
		sdktrace.WithBatchTimeout(telemetry.NearlyImmediate), sdktrace.WithExportTimeout(otlpExportTimeout))
	var metrics sdkmetric.Exporter
	if opts.Protocol == "grpc" {
		metrics, err = otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(u.String()), otlpmetricgrpc.WithHeaders(opts.Headers), otlpmetricgrpc.WithTimeout(otlpExportTimeout))
	} else {
		metrics, err = otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(u.JoinPath("v1", "metrics").String()), otlpmetrichttp.WithHeaders(opts.Headers), otlpmetrichttp.WithTimeout(otlpExportTimeout))
	}
	if err != nil {
		return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
	}
	d.metrics = newAsyncMetricExporter(metrics, opts.QueueSize)
	return d, nil
}

func (d *OTLPDestination) SampleInterval() time.Duration { return d.interval }

func (d *OTLPDestination) SessionResource(sessionID string) *resource.Resource {
	return resource.NewSchemaless(append(d.resource.Attributes(), attribute.String(SessionIDAttr, sessionID))...)
}

func (d *OTLPDestination) SpanProcessor(sessionID string) sdktrace.SpanProcessor {
	if d == nil || d.spans == nil {
		return nil
	}
	return &operationSpanProcessor{next: d.spans, resource: d.SessionResource(sessionID)}
}

func (d *OTLPDestination) MetricExporter() sdkmetric.Exporter {
	if d == nil || d.metrics == nil {
		return nil
	}
	return SharedMetricExporter{Exporter: d.metrics}
}

func (d *OTLPDestination) Shutdown(ctx context.Context) error {
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
