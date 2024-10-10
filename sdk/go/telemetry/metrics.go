package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

type meterProviderKey struct{}

func WithMeterProvider(ctx context.Context, provider *sdkmetric.MeterProvider) context.Context {
	return context.WithValue(ctx, meterProviderKey{}, provider)
}

func MeterProvider(ctx context.Context) *sdkmetric.MeterProvider {
	meterProvider := sdkmetric.NewMeterProvider()
	if val := ctx.Value(meterProviderKey{}); val != nil {
		meterProvider = val.(*sdkmetric.MeterProvider)
	}
	return meterProvider
}

func Meter(ctx context.Context, name string) metric.Meter {
	return MeterProvider(ctx).Meter(name)
}

/* TODO: rm once proven unneeded
type MetricForwarder interface {
	Forward(context.Context, *metricdata.ResourceMetrics) error
}

type MetricReaderWithForward interface {
	sdkmetric.Reader
	MetricForwarder
}

func NewPeriodicReaderWithForward(exporter sdkmetric.Exporter, opts ...sdkmetric.PeriodicReaderOption) MetricReaderWithForward {
	producer := newForwardProducer()
	opts = append(opts, sdkmetric.WithProducer(producer))
	innerReader := sdkmetric.NewPeriodicReader(exporter, opts...)
}

type periodicReaderWithForward struct {
	sdkmetric.Reader
	*forwardProducer
}

type forwardProducer struct {
	mu  sync.Mutex
	buf []*metricdata.ResourceMetrics
}

var _ sdkmetric.Producer = (*forwardProducer)(nil)

func (p *forwardProducer) Forward(ctx context.Context, resourceMetrics *metricdata.ResourceMetrics) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.buf = append(p.buf, resourceMetrics)
	return nil
}

func (p *forwardProducer) Produce(ctx context.Context) ([]*metricdata.ResourceMetrics, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	buf := p.buf
	p.buf = nil
	return buf, nil
}

func NewMetricMultiForwarder(readers ...MetricReaderWithForward) MetricForwarder {
}
*/
