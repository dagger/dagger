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
	// One collection must fit the metric export queue, which admits whole batches.
	maxPendingWorkloadReadings = LargeSpanQueueSize
	SamplePhaseAttr            = "dagger.io/resource.sample.phase"
	SampleIntervalAttr         = "dagger.io/resource.sample_interval_ms"
	ExecutionIDAttr            = "dagger.io/execution.id"
	ExecutionInternalAttr      = "dagger.io/execution.internal"
	ResourceAvailableName      = "dagger.io/resource.sample.available"
	// Only workload export has these memory.stat readings. memory.current
	// minus inactive_file is the working set, and anon is its lower bound.
	MemoryAnonName         = "dagger.io/metrics.memory.anon"
	MemoryInactiveFileName = "dagger.io/metrics.memory.inactive_file"

	// The scope of the readings that only workload export has.
	workloadReadingScope = "dagger.io/engine.buildkit"
)

type workloadReadingsKey struct{}
type workloadReadingTimeKey struct{}
type workloadReadingAttrsKey struct{}
type samplePhaseKey struct{}

// WorkloadReadings keeps every CPU and memory reading of workloads for the
// workload export reader.
// Ordinary SDK gauges still receive every Record call and retain their existing
// last-value aggregation. This buffer never feeds the CLI or Cloud readers.
type WorkloadReadings struct {
	interval time.Duration
	mu       sync.Mutex
	readings []workloadReading
	dropped  uint64
}

func NewWorkloadReadings(interval time.Duration) *WorkloadReadings {
	return &WorkloadReadings{interval: interval}
}

func WithWorkloadReadings(ctx context.Context, readings *WorkloadReadings) context.Context {
	if readings == nil {
		return ctx
	}
	return context.WithValue(ctx, workloadReadingsKey{}, readings)
}

func HasWorkloadReadings(ctx context.Context) bool {
	o, _ := ctx.Value(workloadReadingsKey{}).(*WorkloadReadings)
	return o != nil
}

func WorkloadReadingInterval(ctx context.Context, fallback time.Duration) time.Duration {
	if o, _ := ctx.Value(workloadReadingsKey{}).(*WorkloadReadings); o != nil {
		return o.interval
	}
	return fallback
}

func WithWorkloadReadingAttributes(ctx context.Context, attrs attribute.Set) context.Context {
	if !HasWorkloadReadings(ctx) {
		return ctx
	}
	return context.WithValue(ctx, workloadReadingAttrsKey{}, attrs)
}

func WithWorkloadReadingTime(ctx context.Context, at time.Time) context.Context {
	if !HasWorkloadReadings(ctx) {
		return ctx
	}
	return context.WithValue(ctx, workloadReadingTimeKey{}, at)
}

func WithSamplePhase(ctx context.Context, phase string) context.Context {
	if !HasWorkloadReadings(ctx) {
		return ctx
	}
	return context.WithValue(ctx, samplePhaseKey{}, phase)
}

// WorkloadReadingMeter copies only total CPU and current memory readings to the
// workload export. The original meter, record options, and metric series stay intact.
func WorkloadReadingMeter(ctx context.Context, meter metric.Meter, scope string) metric.Meter {
	if o, _ := ctx.Value(workloadReadingsKey{}).(*WorkloadReadings); o != nil {
		return workloadReadingMeter{Meter: meter, readings: o, scope: scope}
	}
	return meter
}

type workloadReadingMeter struct {
	metric.Meter
	readings *WorkloadReadings
	scope    string
}

type workloadReadingInstrument struct {
	scope, name, unit string
}

type workloadReading struct {
	instrument workloadReadingInstrument
	point      metricdata.DataPoint[int64]
}

type workloadReadingGauge struct {
	metric.Int64Gauge
	readings   *WorkloadReadings
	instrument workloadReadingInstrument
}

func (m workloadReadingMeter) Int64Gauge(name string, opts ...metric.Int64GaugeOption) (metric.Int64Gauge, error) {
	gauge, err := m.Meter.Int64Gauge(name, opts...)
	if err != nil || (name != telemetry.CPUStatUsage && name != telemetry.MemoryCurrentBytes) {
		return gauge, err
	}
	cfg := metric.NewInt64GaugeConfig(opts...)
	return workloadReadingGauge{Int64Gauge: gauge, readings: m.readings,
		instrument: workloadReadingInstrument{m.scope, name, cfg.Unit()}}, nil
}

func (g workloadReadingGauge) Record(ctx context.Context, value int64, opts ...metric.RecordOption) {
	g.Int64Gauge.Record(ctx, value, opts...)
	g.readings.record(ctx, g.instrument, value)
}

// RecordResourceAvailability belongs only to the workload export. In
// particular, it must not register a new series in the ordinary meter provider.
func RecordResourceAvailability(ctx context.Context, source string, available bool) {
	if o, _ := ctx.Value(workloadReadingsKey{}).(*WorkloadReadings); o != nil {
		value := int64(0)
		if available {
			value = 1
		}
		o.record(ctx, workloadReadingInstrument{workloadReadingScope, ResourceAvailableName, "1"}, value, attribute.String("source", source))
	}
}

// RecordWorkloadMemoryStat belongs only to the workload export, like
// RecordResourceAvailability, so CLI and Cloud metrics do not change.
func RecordWorkloadMemoryStat(ctx context.Context, anon, inactiveFile int64) {
	if o, _ := ctx.Value(workloadReadingsKey{}).(*WorkloadReadings); o != nil {
		o.record(ctx, workloadReadingInstrument{workloadReadingScope, MemoryAnonName, "bytes"}, anon)
		o.record(ctx, workloadReadingInstrument{workloadReadingScope, MemoryInactiveFileName, "bytes"}, inactiveFile)
	}
}

func (o *WorkloadReadings) record(ctx context.Context, instrument workloadReadingInstrument, value int64, extra ...attribute.KeyValue) {
	at, _ := ctx.Value(workloadReadingTimeKey{}).(time.Time)
	if at.IsZero() {
		at = time.Now()
	}
	attrs, _ := ctx.Value(workloadReadingAttrsKey{}).(attribute.Set)
	if phase, _ := ctx.Value(samplePhaseKey{}).(string); phase != "" {
		extra = append(extra, attribute.String(SamplePhaseAttr, phase))
	}
	attrs = attribute.NewSet(append(attrs.ToSlice(), extra...)...)
	reading := workloadReading{instrument, metricdata.DataPoint[int64]{Time: at, Value: value, Attributes: attrs}}
	o.mu.Lock()
	if len(o.readings) < maxPendingWorkloadReadings {
		o.readings = append(o.readings, reading)
		o.mu.Unlock()
		return
	}
	o.dropped++
	dropped := o.dropped
	o.mu.Unlock()
	if dropped == 1 || dropped%1024 == 0 {
		slog.Warn("workload readings lost: buffer full", "dropped", dropped)
	}
}

func (o *WorkloadReadings) Produce(ctx context.Context) ([]metricdata.ScopeMetrics, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	o.mu.Lock()
	readings := o.readings
	o.readings = nil
	o.mu.Unlock()

	// Equal values at different times remain separate readings. Empty
	// buffers produce no points, including after the execution has stopped.
	grouped := make(map[workloadReadingInstrument][]metricdata.DataPoint[int64])
	order := make([]workloadReadingInstrument, 0)
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
