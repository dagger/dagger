package telemetry

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
)

const otlpExportTimeout = time.Minute

// OTLPOptions selects an additional engine-owned destination. Empty Signals
// (nil, not an explicit empty list) defaults to traces and metrics. Logs require
// an explicit selection. HTTP endpoints are base URLs; gRPC endpoints name the
// server. Standard SDK exporters handle TLS, compression, and transport retries.
type OTLPOptions struct {
	Endpoint       string
	Protocol       string
	Headers        map[string]string
	Signals        []string
	QueueSize      int
	SampleInterval time.Duration
}

// OTLPDestination has one bounded queue per selected signal across all sessions.
// It owns transport shutdown. Client/session shutdown never waits on this
// destination. Receiver acceptance is not an engine-side durability claim.
type OTLPDestination struct {
	spans    sdktrace.SpanProcessor
	logs     *sdklog.BatchProcessor
	metrics  *asyncMetricExporter
	interval time.Duration
}

// resolve applies defaults and checks the destination settings before any
// exporter or background queue is created.
func (opts *OTLPOptions) resolve() (*url.URL, map[string]bool, error) {
	u, err := url.Parse(opts.Endpoint)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, nil, errors.New("telemetry.otlp.endpoint must be an http(s) URL without credentials, query, or fragment")
	}
	if opts.Protocol == "" {
		opts.Protocol = "http/protobuf"
	}
	if opts.Protocol != "http/protobuf" && opts.Protocol != "grpc" {
		return nil, nil, fmt.Errorf("unsupported telemetry.otlp.protocol %q", opts.Protocol)
	}
	if opts.Protocol == "grpc" && u.Path != "" && u.Path != "/" {
		return nil, nil, errors.New("telemetry.otlp.endpoint must not have a path for grpc")
	}
	if opts.QueueSize == 0 {
		opts.QueueSize = LargeSpanQueueSize
	}
	if opts.QueueSize < 1 || opts.QueueSize > 1048576 {
		return nil, nil, errors.New("telemetry.otlp.queueSize must be between 1 and 1048576")
	}
	if opts.SampleInterval == 0 {
		opts.SampleInterval = time.Second
	}
	if opts.SampleInterval < 100*time.Millisecond || opts.SampleInterval > time.Minute {
		return nil, nil, errors.New("telemetry.otlp.sampleIntervalMs must be between 100 and 60000")
	}
	if opts.Signals == nil {
		opts.Signals = []string{"traces", "metrics"}
	}
	if len(opts.Signals) == 0 {
		return nil, nil, errors.New("telemetry.otlp.signals must select at least one signal")
	}
	selected := map[string]bool{}
	for _, signal := range opts.Signals {
		if signal != "traces" && signal != "metrics" && signal != "logs" {
			return nil, nil, fmt.Errorf("unsupported telemetry.otlp signal %q", signal)
		}
		if selected[signal] {
			return nil, nil, fmt.Errorf("duplicate telemetry.otlp signal %q", signal)
		}
		selected[signal] = true
	}
	return u, selected, nil
}

func NewOTLPDestination(ctx context.Context, opts OTLPOptions) (_ *OTLPDestination, rerr error) {
	u, selected, err := opts.resolve()
	if err != nil {
		return nil, err
	}
	d := &OTLPDestination{interval: opts.SampleInterval}
	defer func() {
		if rerr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = d.Shutdown(ctx)
		}
	}()
	if selected["traces"] {
		var exporter sdktrace.SpanExporter
		if opts.Protocol == "grpc" {
			exporter, err = otlptracegrpc.New(ctx, otlptracegrpc.WithEndpointURL(u.String()), otlptracegrpc.WithHeaders(opts.Headers), otlptracegrpc.WithTimeout(otlpExportTimeout))
		} else {
			exporter, err = otlptracehttp.New(ctx, otlptracehttp.WithEndpointURL(u.JoinPath("v1", "traces").String()), otlptracehttp.WithHeaders(opts.Headers), otlptracehttp.WithTimeout(otlpExportTimeout))
		}
		if err != nil {
			return nil, fmt.Errorf("create OTLP trace exporter: %w", err)
		}
		// Deliberately no CoalescingSpanExporter or FilterLiveSpansExporter:
		// start and completion snapshots must survive even in the same batch.
		d.spans = &snapshotSpanProcessor{SpanProcessor: sdktrace.NewBatchSpanProcessor(exporter,
			sdktrace.WithMaxQueueSize(opts.QueueSize), sdktrace.WithMaxExportBatchSize(min(opts.QueueSize, 512)),
			sdktrace.WithBatchTimeout(telemetry.NearlyImmediate), sdktrace.WithExportTimeout(otlpExportTimeout))}
	}
	if selected["logs"] {
		var exporter sdklog.Exporter
		if opts.Protocol == "grpc" {
			exporter, err = otlploggrpc.New(ctx, otlploggrpc.WithEndpointURL(u.String()), otlploggrpc.WithHeaders(opts.Headers), otlploggrpc.WithTimeout(otlpExportTimeout))
		} else {
			exporter, err = otlploghttp.New(ctx, otlploghttp.WithEndpointURL(u.JoinPath("v1", "logs").String()), otlploghttp.WithHeaders(opts.Headers), otlploghttp.WithTimeout(otlpExportTimeout))
		}
		if err != nil {
			return nil, fmt.Errorf("create OTLP log exporter: %w", err)
		}
		d.logs = sdklog.NewBatchProcessor(exporter, sdklog.WithMaxQueueSize(opts.QueueSize), sdklog.WithExportMaxBatchSize(min(opts.QueueSize, 512)), sdklog.WithExportInterval(telemetry.NearlyImmediate), sdklog.WithExportTimeout(otlpExportTimeout))
	}
	if selected["metrics"] {
		var exporter sdkmetric.Exporter
		if opts.Protocol == "grpc" {
			exporter, err = otlpmetricgrpc.New(ctx, otlpmetricgrpc.WithEndpointURL(u.String()), otlpmetricgrpc.WithHeaders(opts.Headers), otlpmetricgrpc.WithTimeout(otlpExportTimeout))
		} else {
			exporter, err = otlpmetrichttp.New(ctx, otlpmetrichttp.WithEndpointURL(u.JoinPath("v1", "metrics").String()), otlpmetrichttp.WithHeaders(opts.Headers), otlpmetrichttp.WithTimeout(otlpExportTimeout))
		}
		if err != nil {
			return nil, fmt.Errorf("create OTLP metric exporter: %w", err)
		}
		d.metrics = newAsyncMetricExporter(exporter, opts.QueueSize)
	}
	return d, nil
}

func (d *OTLPDestination) SampleInterval() time.Duration { return d.interval }

func (d *OTLPDestination) SpanProcessor() sdktrace.SpanProcessor {
	if d == nil || d.spans == nil {
		return nil
	}
	return sharedSpanProcessor{d.spans}
}

func (d *OTLPDestination) LogProcessor() sdklog.Processor {
	if d == nil || d.logs == nil {
		return nil
	}
	return sharedLogProcessor{d.logs}
}

func (d *OTLPDestination) MetricExporter() sdkmetric.Exporter {
	if d == nil || d.metrics == nil {
		return nil
	}
	return SharedMetricExporter{Exporter: d.metrics}
}

func (d *OTLPDestination) SpanExporter() sdktrace.SpanExporter {
	if processor := d.SpanProcessor(); processor != nil {
		return telemetry.SpanForwarder{Processors: []sdktrace.SpanProcessor{processor}}
	}
	return nil
}

func (d *OTLPDestination) LogExporter() sdklog.Exporter {
	if processor := d.LogProcessor(); processor != nil {
		return telemetry.LogForwarder{Processors: []sdklog.Processor{processor}}
	}
	return nil
}

func (d *OTLPDestination) Shutdown(ctx context.Context) error {
	if d == nil {
		return nil
	}
	var shutdowns []func(context.Context) error
	if d.spans != nil {
		shutdowns = append(shutdowns, d.spans.Shutdown)
	}
	if d.logs != nil {
		shutdowns = append(shutdowns, d.logs.Shutdown)
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

type sharedSpanProcessor struct{ sdktrace.SpanProcessor }

func (sharedSpanProcessor) ForceFlush(context.Context) error { return nil }
func (sharedSpanProcessor) Shutdown(context.Context) error   { return nil }

type sharedLogProcessor struct{ sdklog.Processor }

func (sharedLogProcessor) ForceFlush(context.Context) error { return nil }
func (sharedLogProcessor) Shutdown(context.Context) error   { return nil }
