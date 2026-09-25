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
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

const (
	maxPendingObservations = 65536
	SamplePhaseAttr        = "dagger.io/resource.sample.phase"
	SampleIntervalAttr     = "dagger.io/resource.sample_interval_ms"
	ExecutionIDAttr        = "dagger.io/execution.id"
	ExecutionInternalAttr  = "dagger.io/execution.internal"
	ResourceAvailableName  = "dagger.resource.sample.available"
)

type observationsKey struct{}
type observationTimeKey struct{}
type observationAttrsKey struct{}
type samplePhaseKey struct{}

// Observations retains selected workload readings for one independent reader.
// Ordinary SDK gauges still receive every Record call and retain their existing
// last-value aggregation. This buffer never feeds the CLI or Cloud readers.
type Observations struct {
	interval time.Duration
	mu       sync.Mutex
	readings []observation
	dropped  uint64
}

func NewObservations(interval time.Duration) *Observations {
	return &Observations{interval: interval}
}

func WithObservations(ctx context.Context, observations *Observations) context.Context {
	if observations == nil {
		return ctx
	}
	return context.WithValue(ctx, observationsKey{}, observations)
}

func HasObservations(ctx context.Context) bool {
	o, _ := ctx.Value(observationsKey{}).(*Observations)
	return o != nil
}

func ObservationInterval(ctx context.Context, fallback time.Duration) time.Duration {
	if o, _ := ctx.Value(observationsKey{}).(*Observations); o != nil {
		return o.interval
	}
	return fallback
}

func WithObservationAttributes(ctx context.Context, attrs attribute.Set) context.Context {
	if !HasObservations(ctx) {
		return ctx
	}
	return context.WithValue(ctx, observationAttrsKey{}, attrs)
}

func WithObservationTime(ctx context.Context, at time.Time) context.Context {
	if !HasObservations(ctx) {
		return ctx
	}
	return context.WithValue(ctx, observationTimeKey{}, at)
}

func WithSamplePhase(ctx context.Context, phase string) context.Context {
	if !HasObservations(ctx) {
		return ctx
	}
	return context.WithValue(ctx, samplePhaseKey{}, phase)
}

// ObservationMeter tees only total CPU and current memory into the additional
// destination. The original meter, record options, and metric series stay intact.
func ObservationMeter(ctx context.Context, meter metric.Meter, scope string) metric.Meter {
	if o, _ := ctx.Value(observationsKey{}).(*Observations); o != nil {
		return observationMeter{Meter: meter, observations: o, scope: scope}
	}
	return meter
}

type observationMeter struct {
	metric.Meter
	observations *Observations
	scope        string
}

type observationInstrument struct {
	scope, name, unit string
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
	if err != nil || (name != telemetry.CPUStatUsage && name != telemetry.MemoryCurrentBytes) {
		return gauge, err
	}
	cfg := metric.NewInt64GaugeConfig(opts...)
	return observationGauge{Int64Gauge: gauge, observations: m.observations,
		instrument: observationInstrument{m.scope, name, cfg.Unit()}}, nil
}

func (g observationGauge) Record(ctx context.Context, value int64, opts ...metric.RecordOption) {
	g.Int64Gauge.Record(ctx, value, opts...)
	g.observations.record(ctx, g.instrument, value)
}

// ObserveResourceAvailability belongs only to the additional stream. In
// particular, it must not register a new series in the ordinary meter provider.
func ObserveResourceAvailability(ctx context.Context, source string, available bool) {
	if o, _ := ctx.Value(observationsKey{}).(*Observations); o != nil {
		value := int64(0)
		if available {
			value = 1
		}
		o.record(ctx, observationInstrument{"dagger.io/engine.buildkit", ResourceAvailableName, "1"}, value, attribute.String("source", source))
	}
}

func (o *Observations) record(ctx context.Context, instrument observationInstrument, value int64, extra ...attribute.KeyValue) {
	at, _ := ctx.Value(observationTimeKey{}).(time.Time)
	if at.IsZero() {
		at = time.Now()
	}
	attrs, _ := ctx.Value(observationAttrsKey{}).(attribute.Set)
	if phase, _ := ctx.Value(samplePhaseKey{}).(string); phase != "" {
		extra = append(extra, attribute.String(SamplePhaseAttr, phase))
	}
	attrs = attribute.NewSet(append(attrs.ToSlice(), extra...)...)
	reading := observation{instrument, metricdata.DataPoint[int64]{Time: at, Value: value, Attributes: attrs}}
	o.mu.Lock()
	if len(o.readings) < maxPendingObservations {
		o.readings = append(o.readings, reading)
		o.mu.Unlock()
		return
	}
	o.dropped++
	dropped := o.dropped
	o.mu.Unlock()
	if dropped == 1 || dropped%1024 == 0 {
		slog.Warn("operation resource observations lost: buffer full", "dropped", dropped)
	}
}

func (o *Observations) Produce(ctx context.Context) ([]metricdata.ScopeMetrics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	readings := o.readings
	o.readings = nil
	o.mu.Unlock()

	// Equal values at different times remain separate observations. Empty
	// buffers produce no points, including after the execution has stopped.
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
			Name: instrument.name, Unit: instrument.unit,
			Data: metricdata.Gauge[int64]{DataPoints: grouped[instrument]},
		})
	}
	return scopes, nil
}
