// Package cgroupmetrics reports resource use for the cgroup that contains the
// Dagger engine process.
//
// CPU usage and memory accounting include processes in descendant cgroups.
// The standard engine layout expects the engine cgroup to have no cgroup
// descendants. CPU usage values are cumulative since cgroup creation, and the
// total value reported by the kernel is the accounting source; user and system
// values are diagnostic components. Memory peak is the peak for the cgroup
// lifetime. Memory breakdown fields are diagnostic and can overlap. These
// metrics do not imply attribution to a Dagger client or organization. In the
// standard layout, /init and /buildkit are siblings: user execution cgroups are
// not included. A different layout (including an engine at the namespace root)
// can include user workloads. This package observes that boundary; it does not
// enforce it.
//
// CPU quota enforcement counters report only this cgroup's own quota. They
// exclude throttling caused by ancestor cgroups. Zero does not mean that the
// engine was not throttled.
package cgroupmetrics

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

const (
	InstrumentationScopeName = "dagger.io/engine.resources"

	CPUTimeName           = "dagger.engine.cpu.time"
	CPUThrottledTimeName  = "dagger.engine.cpu.throttled.time"
	CPUPeriodsName        = "dagger.engine.cpu.periods"
	MemoryCurrentName     = "dagger.engine.memory.current"
	MemoryPeakName        = "dagger.engine.memory.peak"
	MemorySwapCurrentName = "dagger.engine.memory.swap.current"
	MemorySwapPeakName    = "dagger.engine.memory.swap.peak"
	MemoryBreakdownName   = "dagger.engine.memory.breakdown"
	MemoryEventsName      = "dagger.engine.memory.events"
	CgroupDescendantsName = "dagger.engine.cgroup.descendants"
	ResourceAvailableName = "dagger.engine.resource_metrics.available"
)

const (
	sourceCPUStat           = "cpu.stat"
	sourceMemoryCurrent     = "memory.current"
	sourceMemoryPeak        = "memory.peak"
	sourceMemoryStat        = "memory.stat"
	sourceMemorySwapCurrent = "memory.swap.current"
	sourceMemorySwapPeak    = "memory.swap.peak"
	sourceMemoryEvents      = "memory.events"
	sourceCgroupStat        = "cgroup.stat"
)

var sources = [...]string{
	sourceCPUStat,
	sourceMemoryCurrent,
	sourceMemoryPeak,
	sourceMemoryStat,
	sourceMemorySwapCurrent,
	sourceMemorySwapPeak,
	sourceMemoryEvents,
	sourceCgroupStat,
}

var memoryTypes = [...]string{
	"anon",
	"file",
	"kernel",
	"kernel_stack",
	"pagetables",
	"sock",
	"shmem",
	"file_mapped",
	"file_dirty",
	"file_writeback",
	"slab",
	"active_anon",
	"inactive_anon",
	"active_file",
	"inactive_file",
	"unevictable",
}

var memoryEvents = [...]string{
	"low",
	"high",
	"max",
	"oom",
	"oom_kill",
	"oom_group_kill",
}

type instruments struct {
	cpuTime           metric.Int64ObservableCounter
	cpuThrottledTime  metric.Int64ObservableCounter
	cpuPeriods        metric.Int64ObservableCounter
	memoryCurrent     metric.Int64ObservableGauge
	memoryPeak        metric.Int64ObservableGauge
	memorySwapCurrent metric.Int64ObservableGauge
	memorySwapPeak    metric.Int64ObservableGauge
	memoryBreakdown   metric.Int64ObservableGauge
	memoryEvents      metric.Int64ObservableCounter
	cgroupDescendants metric.Int64ObservableGauge
	available         metric.Int64ObservableGauge
}

type sourceState struct {
	known     bool
	available bool
}

type collector struct {
	reader     *reader
	procRoot   string
	cgroupRoot string
	inst       instruments
	log        func(context.Context, slog.Level, string, ...any)

	// Readers can collect concurrently. Serialize reads and availability
	// transitions, but never retain a resource sample between collections.
	mu     sync.Mutex
	states map[string]sourceState
}

// Register discovers the engine process cgroup and registers one callback that
// reads its cgroup v2 accounting files for each OpenTelemetry collection.
// Discovery and read failures are reported as availability=0 and retried at
// collection time. Only instrument or callback registration failures return an
// error. Keep the callback registered until the meter provider shuts down.
func Register(ctx context.Context, meter metric.Meter) (metric.Registration, error) {
	return register(ctx, meter, defaultProcRoot, defaultCgroupRoot, slog.Log)
}

func register(ctx context.Context, meter metric.Meter, procRoot, cgroupRoot string, log func(context.Context, slog.Level, string, ...any)) (metric.Registration, error) {
	r, discoveryErr := newReader(procRoot, cgroupRoot)
	if errors.Is(discoveryErr, errUnsupportedPlatform) {
		return noop.Registration{}, nil
	}

	inst, err := newInstruments(meter)
	if err != nil {
		return nil, err
	}
	c := &collector{
		reader:     r,
		procRoot:   procRoot,
		cgroupRoot: cgroupRoot,
		inst:       inst,
		log:        log,
		states:     make(map[string]sourceState, len(sources)),
	}
	if discoveryErr != nil {
		// One boundary warning is enough; do not also log one per source.
		c.log(ctx, slog.LevelWarn, "engine cgroup resource metrics unavailable", "error", discoveryErr)
		for _, source := range sources {
			c.states[source] = sourceState{known: true}
		}
	} else {
		c.logBoundary(ctx)
	}
	return meter.RegisterCallback(c.observe, inst.observables()...)
}

func (c *collector) logBoundary(ctx context.Context) {
	c.log(ctx, slog.LevelInfo, "observing engine cgroup resource metrics", "path", c.reader.dir, "inode", c.reader.inode)
}

func newInstruments(meter metric.Meter) (instruments, error) {
	var inst instruments
	var err error

	inst.cpuTime, err = meter.Int64ObservableCounter(CPUTimeName,
		metric.WithUnit("us"),
		metric.WithDescription("Cumulative CPU time charged to the engine process cgroup since cgroup creation; cpu.mode=total is the accounting source."),
	)
	if err != nil {
		return inst, err
	}
	inst.cpuThrottledTime, err = meter.Int64ObservableCounter(CPUThrottledTimeName,
		metric.WithUnit("us"),
		metric.WithDescription("Cumulative CPU throttling time from the engine process cgroup's own CPU quota since cgroup creation. Excludes throttling caused by ancestor cgroups; zero does not mean the engine was not throttled."),
	)
	if err != nil {
		return inst, err
	}
	inst.cpuPeriods, err = meter.Int64ObservableCounter(CPUPeriodsName,
		metric.WithUnit("1"),
		metric.WithDescription("Cumulative CPU quota enforcement periods for the engine process cgroup's own quota since cgroup creation; cpu.period=throttled counts throttled periods. Excludes ancestor cgroup quota enforcement."),
	)
	if err != nil {
		return inst, err
	}
	inst.memoryCurrent, err = meter.Int64ObservableGauge(MemoryCurrentName,
		metric.WithUnit("By"),
		metric.WithDescription("Current memory charged to the engine process cgroup, including cgroup descendants."),
	)
	if err != nil {
		return inst, err
	}
	inst.memoryPeak, err = meter.Int64ObservableGauge(MemoryPeakName,
		metric.WithUnit("By"),
		metric.WithDescription("Cgroup-lifetime peak memory charged to the engine process cgroup, including cgroup descendants."),
	)
	if err != nil {
		return inst, err
	}
	inst.memorySwapCurrent, err = meter.Int64ObservableGauge(MemorySwapCurrentName,
		metric.WithUnit("By"),
		metric.WithDescription("Current swap charged to the engine process cgroup, including cgroup descendants."),
	)
	if err != nil {
		return inst, err
	}
	inst.memorySwapPeak, err = meter.Int64ObservableGauge(MemorySwapPeakName,
		metric.WithUnit("By"),
		metric.WithDescription("Cgroup-lifetime peak swap charged to the engine process cgroup, including cgroup descendants."),
	)
	if err != nil {
		return inst, err
	}
	inst.memoryBreakdown, err = meter.Int64ObservableGauge(MemoryBreakdownName,
		metric.WithUnit("By"),
		metric.WithDescription("Selected diagnostic memory fields for the engine process cgroup; fields can overlap and must not be summed."),
	)
	if err != nil {
		return inst, err
	}
	inst.memoryEvents, err = meter.Int64ObservableCounter(MemoryEventsName,
		metric.WithUnit("1"),
		metric.WithDescription("Cumulative memory events for the engine process cgroup since cgroup creation."),
	)
	if err != nil {
		return inst, err
	}
	inst.cgroupDescendants, err = meter.Int64ObservableGauge(CgroupDescendantsName,
		metric.WithUnit("1"),
		metric.WithDescription("Live and dying cgroup descendants below the engine process cgroup; the standard engine layout expects zero live descendants."),
	)
	if err != nil {
		return inst, err
	}
	inst.available, err = meter.Int64ObservableGauge(ResourceAvailableName,
		metric.WithUnit("1"),
		metric.WithDescription("1 if a source was read and its selected fields were valid; 0 on discovery, read, or parse failure. Valid fields from a partially malformed source are still reported."),
	)
	return inst, err
}

func (inst instruments) observables() []metric.Observable {
	return []metric.Observable{
		inst.cpuTime,
		inst.cpuThrottledTime,
		inst.cpuPeriods,
		inst.memoryCurrent,
		inst.memoryPeak,
		inst.memorySwapCurrent,
		inst.memorySwapPeak,
		inst.memoryBreakdown,
		inst.memoryEvents,
		inst.cgroupDescendants,
		inst.available,
	}
}

func (c *collector) observe(ctx context.Context, observer metric.Observer) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.reader == nil {
		r, err := newReader(c.procRoot, c.cgroupRoot)
		if err != nil {
			for _, source := range sources {
				c.observeAvailability(ctx, observer, source, err)
			}
			return nil
		}
		c.reader = r
		c.logBoundary(ctx)
	}
	c.observeCPU(ctx, observer)
	c.observeScalar(ctx, observer, sourceMemoryCurrent, c.inst.memoryCurrent)
	c.observeScalar(ctx, observer, sourceMemoryPeak, c.inst.memoryPeak)
	c.observeScalar(ctx, observer, sourceMemorySwapCurrent, c.inst.memorySwapCurrent)
	c.observeScalar(ctx, observer, sourceMemorySwapPeak, c.inst.memorySwapPeak)
	c.observeMemoryStat(ctx, observer)
	c.observeMemoryEvents(ctx, observer)
	c.observeCgroupStat(ctx, observer)
	return nil
}

func (c *collector) observeCPU(ctx context.Context, observer metric.Observer) {
	values := c.readFields(ctx, observer, sourceCPUStat,
		"usage_usec", "user_usec", "system_usec", "throttled_usec", "nr_periods", "nr_throttled",
	)

	observeField(observer, c.inst.cpuTime, values, "usage_usec", attribute.String("cpu.mode", "total"))
	observeField(observer, c.inst.cpuTime, values, "user_usec", attribute.String("cpu.mode", "user"))
	observeField(observer, c.inst.cpuTime, values, "system_usec", attribute.String("cpu.mode", "system"))
	observeField(observer, c.inst.cpuThrottledTime, values, "throttled_usec")
	observeField(observer, c.inst.cpuPeriods, values, "nr_periods", attribute.String("cpu.period", "total"))
	observeField(observer, c.inst.cpuPeriods, values, "nr_throttled", attribute.String("cpu.period", "throttled"))
}

func (c *collector) observeScalar(ctx context.Context, observer metric.Observer, source string, instrument metric.Int64ObservableGauge) {
	data, err := c.reader.read(source)
	var value int64
	if err == nil {
		value, err = parseUint(strings.TrimSpace(string(data)))
	}
	c.observeAvailability(ctx, observer, source, err)
	if err == nil {
		observer.ObserveInt64(instrument, value)
	}
}

func (c *collector) observeMemoryStat(ctx context.Context, observer metric.Observer) {
	values := c.readFields(ctx, observer, sourceMemoryStat, memoryTypes[:]...)
	for _, name := range memoryTypes {
		observeField(observer, c.inst.memoryBreakdown, values, name, attribute.String("memory.type", name))
	}
}

func (c *collector) observeMemoryEvents(ctx context.Context, observer metric.Observer) {
	values := c.readFields(ctx, observer, sourceMemoryEvents, memoryEvents[:]...)
	for _, name := range memoryEvents {
		observeField(observer, c.inst.memoryEvents, values, name, attribute.String("memory.event", name))
	}
}

func (c *collector) observeCgroupStat(ctx context.Context, observer metric.Observer) {
	values := c.readFields(ctx, observer, sourceCgroupStat, "nr_descendants", "nr_dying_descendants")
	observeField(observer, c.inst.cgroupDescendants, values, "nr_descendants", attribute.String("cgroup.state", "live"))
	observeField(observer, c.inst.cgroupDescendants, values, "nr_dying_descendants", attribute.String("cgroup.state", "dying"))
}

func (c *collector) readFields(ctx context.Context, observer metric.Observer, source string, allowlist ...string) map[string]int64 {
	data, err := c.reader.read(source)
	values := make(map[string]int64)
	if err == nil {
		for _, line := range strings.Split(string(data), "\n") {
			fields := strings.Fields(line)
			if len(fields) == 0 || !slices.Contains(allowlist, fields[0]) {
				continue
			}
			if len(fields) != 2 {
				err = errors.Join(err, fmt.Errorf("invalid cgroup field %q", fields[0]))
				continue
			}
			value, parseErr := parseUint(fields[1])
			if parseErr != nil {
				err = errors.Join(err, fmt.Errorf("%s: %w", fields[0], parseErr))
				continue
			}
			values[fields[0]] = value
		}
		if len(values) == 0 && err == nil {
			err = errors.New("no supported cgroup fields")
		}
	}
	c.observeAvailability(ctx, observer, source, err)
	return values
}

func parseUint(value string) (int64, error) {
	n, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse unsigned cgroup value %q: %w", value, err)
	}
	if n > math.MaxInt64 {
		return 0, fmt.Errorf("cgroup value %q exceeds int64", value)
	}
	return int64(n), nil
}

func observeField(observer metric.Observer, instrument metric.Int64Observable, values map[string]int64, name string, attrs ...attribute.KeyValue) {
	value, ok := values[name]
	if !ok {
		return
	}
	if len(attrs) == 0 {
		observer.ObserveInt64(instrument, value)
		return
	}
	observer.ObserveInt64(instrument, value, metric.WithAttributes(attrs...))
}

func (c *collector) observeAvailability(ctx context.Context, observer metric.Observer, source string, sourceErr error) {
	available := sourceErr == nil
	value := int64(0)
	if available {
		value = 1
	}
	observer.ObserveInt64(c.inst.available, value, metric.WithAttributes(attribute.String("source", source)))

	previous := c.states[source]
	c.states[source] = sourceState{known: true, available: available}
	if previous.known && previous.available == available {
		return
	}
	if available {
		if previous.known {
			c.log(ctx, slog.LevelInfo, "engine cgroup metric source recovered", "source", source)
		}
		return
	}
	optional := source == sourceMemoryPeak || source == sourceMemorySwapCurrent || source == sourceMemorySwapPeak
	if !previous.known && optional && errors.Is(sourceErr, os.ErrNotExist) {
		return
	}
	c.log(ctx, slog.LevelWarn, "engine cgroup metric source unavailable", "source", source, "error", sourceErr)
}
