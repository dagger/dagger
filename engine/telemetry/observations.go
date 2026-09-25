package telemetry

import (
	"context"
	"log/slog"
	"sync"
	"time"

	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	// Bound unexported readings per client and destination. A slow reader must
	// not delay sampling or discard another destination's observations.
	maxPendingObservations = 65536
	SamplePhaseAttr        = "dagger.io/resource.sample.phase"
	SampleIntervalAttr     = "dagger.io/resource.sample_interval_ms"
	ExecutionIDAttr        = "dagger.io/execution.id"
	ExecutionInternalAttr  = "dagger.io/execution.internal"
)

type observationsKey struct{}
type observationTimeKey struct{}
type samplePhaseKey struct{}

// Observations preserves resource gauge readings before SDK last-value
// aggregation. Each reader receives its own bounded copy of the point stream.
// It uses the reader's ordinary resource, exporters, and lifecycle.
// Create readers before publishing this object to sampling contexts.
type Observations struct {
	interval time.Duration
	readers  []*observationProducer
}

func NewObservations(interval time.Duration) *Observations {
	return &Observations{interval: interval}
}

func (o *Observations) Producer(destination string) sdkmetric.Producer {
	p := &observationProducer{destination: destination}
	o.readers = append(o.readers, p)
	return p
}

func WithObservations(ctx context.Context, observations *Observations) context.Context {
	return context.WithValue(ctx, observationsKey{}, observations)
}

func ObservationInterval(ctx context.Context, fallback time.Duration) time.Duration {
	if o, _ := ctx.Value(observationsKey{}).(*Observations); o != nil && o.interval > 0 {
		return o.interval
	}
	return fallback
}

// WithObservationTime sets the time of a completed source read. It does not
// add a per-reading metric attribute or a new high-cardinality series.
func WithObservationTime(ctx context.Context, at time.Time) context.Context {
	return context.WithValue(ctx, observationTimeKey{}, at)
}

func WithSamplePhase(ctx context.Context, phase string) context.Context {
	return context.WithValue(ctx, samplePhaseKey{}, phase)
}

// ObservationMeter intercepts resource Int64Gauge.Record calls only. All other
// instruments keep SDK behavior. Without a configured buffer it is an ordinary
// meter, so standalone sampler callers remain supported.
func ObservationMeter(ctx context.Context, name string) metric.Meter {
	meter := telemetry.Meter(ctx, name)
	if o, _ := ctx.Value(observationsKey{}).(*Observations); o != nil {
		return observationMeter{Meter: meter, observations: o, scope: name}
	}
	return meter
}

type observationMeter struct {
	metric.Meter
	observations *Observations
	scope        string
}

type observationInstrument struct {
	scope, name, description, unit string
}

type observation struct {
	instrument observationInstrument
	point      metricdata.DataPoint[int64]
}

type observationGauge struct {
	metric.Int64Gauge
	observations *Observations
	instrument   observationInstrument
}

func (m observationMeter) Int64Gauge(name string, opts ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	gauge, err := m.Meter.Int64Gauge(name, opts...)
	if err != nil {
		return nil, err
	}
	cfg := metric.NewInt64GaugeConfig(opts...)
	return observationGauge{Int64Gauge: gauge, observations: m.observations,
		instrument: observationInstrument{m.scope, name, cfg.Description(), cfg.Unit()}}, nil
}

func (g observationGauge) Record(ctx context.Context, value int64, opts ...metric.RecordOption) {
	at, _ := ctx.Value(observationTimeKey{}).(time.Time)
	if at.IsZero() {
		at = time.Now()
	}
	cfg := metric.NewRecordConfig(opts)
	attrs := cfg.Attributes()
	if phase, _ := ctx.Value(samplePhaseKey{}).(string); phase != "" {
		attrs = attribute.NewSet(append(attrs.ToSlice(), attribute.String(SamplePhaseAttr, phase))...)
	}
	reading := observation{g.instrument, metricdata.DataPoint[int64]{Time: at, Value: value, Attributes: attrs}}
	for _, reader := range g.observations.readers {
		reader.record(reading)
	}
	// Do not also record into the SDK gauge. That would publish a second,
	// aggregated copy and could replay its last value after the source stops.
}

type observationProducer struct {
	mu          sync.Mutex
	destination string
	readings    []observation
	dropped     uint64
}

func (p *observationProducer) record(reading observation) {
	p.mu.Lock()
	if len(p.readings) < maxPendingObservations {
		p.readings = append(p.readings, reading)
		p.mu.Unlock()
		return
	}
	p.dropped++
	dropped := p.dropped
	p.mu.Unlock()
	if dropped == 1 || dropped%1024 == 0 {
		slog.Warn("resource observations lost: reader buffer full", "destination", p.destination, "dropped", dropped)
	}
}

func (p *observationProducer) Produce(ctx context.Context) ([]metricdata.ScopeMetrics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p.mu.Lock()
	readings := p.readings
	p.readings = nil
	p.mu.Unlock()

	// Preserve every point, including equal values at different times. Empty
	// buffers produce no points: no stale measurements get a new timestamp.
	grouped := make(map[observationInstrument][]metricdata.DataPoint[int64])
	order := make([]observationInstrument, 0)
	for _, reading := range readings {
		if _, ok := grouped[reading.instrument]; !ok {
			order = append(order, reading.instrument)
		}
		grouped[reading.instrument] = append(grouped[reading.instrument], reading.point)
	}
	var scopes []metricdata.ScopeMetrics
	indices := make(map[string]int)
	for _, instrument := range order {
		i, ok := indices[instrument.scope]
		if !ok {
			i = len(scopes)
			indices[instrument.scope] = i
			scopes = append(scopes, metricdata.ScopeMetrics{Scope: instrumentation.Scope{Name: instrument.scope}})
		}
		scopes[i].Metrics = append(scopes[i].Metrics, metricdata.Metrics{
			Name: instrument.name, Description: instrument.description, Unit: instrument.unit,
			Data: metricdata.Gauge[int64]{DataPoints: grouped[instrument]},
		})
	}
	return scopes, nil
}
