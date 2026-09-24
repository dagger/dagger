//go:build linux

package cgroupmetrics

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestCgroupDiscovery(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		entry string
		path  string
	}{
		{name: "init", entry: "0::/init\n", path: "/init"},
		{name: "namespace root", entry: "0::/\n", path: "/"},
		{name: "unified entry in hybrid hierarchy", entry: "5:cpu:/legacy\n0::/engine\n", path: "/engine"},
		{name: "missing unified entry", entry: "5:cpu:/legacy\n"},
		{name: "malformed entry", entry: "not-a-cgroup-entry\n"},
		{name: "relative path", entry: "0::init\n"},
		{name: "traversal", entry: "0::/../outside\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			procRoot, cgroupRoot, dir := fakeCgroup(t, tc.path)
			writeFile(t, filepath.Join(procRoot, "self/cgroup"), tc.entry)
			r, err := newReader(procRoot, cgroupRoot)
			if tc.path == "" {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, dir, r.dir)

			require.NoError(t, os.Remove(filepath.Join(dir, "cgroup.controllers")))
			_, err = newReader(procRoot, cgroupRoot)
			require.Error(t, err, "a cgroup v2 boundary is required")
		})
	}
}

func TestCgroupDiscoveryRejectsSymlinkEscape(t *testing.T) {
	t.Parallel()
	procRoot, cgroupRoot, _ := fakeCgroup(t, "/")
	require.NoError(t, os.Symlink(t.TempDir(), filepath.Join(cgroupRoot, "escape")))
	writeFile(t, filepath.Join(procRoot, "self/cgroup"), "0::/escape\n")
	_, err := newReader(procRoot, cgroupRoot)
	require.ErrorContains(t, err, "escapes mount")
}

func TestResourceMetrics(t *testing.T) {
	t.Parallel()
	procRoot, cgroupRoot, dir := fakeCgroup(t, "/engine")
	first, second := newCollectors(t, procRoot, cgroupRoot, io.Discard)
	metrics := collect(t, first)

	// Check the exported contract, not the instrument handles or implementation
	// allowlists. Attribute checks also exclude client/span attribution.
	assertMetric(t, metrics, "dagger.engine.cpu.time", "us", "cpu.mode", true,
		map[string]int64{"total": 100, "user": 90, "system": 30})
	assertMetric(t, metrics, "dagger.engine.cpu.throttled.time", "us", "", true,
		map[string]int64{"": 4})
	assertMetric(t, metrics, "dagger.engine.cpu.periods", "1", "cpu.period", true,
		map[string]int64{"total": 8, "throttled": 2})
	assertMetric(t, metrics, "dagger.engine.memory.current", "By", "", false,
		map[string]int64{"": 200})
	assertMetric(t, metrics, "dagger.engine.memory.peak", "By", "", false,
		map[string]int64{"": 300})
	assertMetric(t, metrics, "dagger.engine.memory.swap.current", "By", "", false,
		map[string]int64{"": 10})
	assertMetric(t, metrics, "dagger.engine.memory.swap.peak", "By", "", false,
		map[string]int64{"": 20})
	assertMetric(t, metrics, "dagger.engine.memory.breakdown", "By", "memory.type", false,
		map[string]int64{"anon": 120, "file": 50, "kernel": 50, "shmem": 10})
	assertMetric(t, metrics, "dagger.engine.memory.events", "1", "memory.event", true,
		map[string]int64{"low": 1, "high": 2, "max": 3, "oom": 4, "oom_kill": 5, "oom_group_kill": 6})
	assertMetric(t, metrics, "dagger.engine.cgroup.descendants", "1", "cgroup.state", false,
		map[string]int64{"live": 0, "dying": 1})
	assertMetric(t, metrics, "dagger.engine.resource_metrics.available", "1", "source", false,
		map[string]int64{
			"cpu.stat": 1, "memory.current": 1, "memory.peak": 1, "memory.stat": 1,
			"memory.swap.current": 1, "memory.swap.peak": 1, "memory.events": 1, "cgroup.stat": 1,
		})

	writeFile(t, filepath.Join(dir, "cpu.stat"), "usage_usec 150\nuser_usec 100\nsystem_usec 40\n")
	writeFile(t, filepath.Join(dir, "memory.current"), "250\n")
	// Both readers see raw kernel values immediately. There is no engine-start
	// baseline, cached sample, or memory-peak reset on either collection.
	for _, reader := range []*sdkmetric.ManualReader{first, second} {
		metrics = collect(t, reader)
		assertMetric(t, metrics, "dagger.engine.cpu.time", "us", "cpu.mode", true,
			map[string]int64{"total": 150, "user": 100, "system": 40})
		assertMetric(t, metrics, "dagger.engine.memory.current", "By", "", false, map[string]int64{"": 250})
		assertMetric(t, metrics, "dagger.engine.memory.peak", "By", "", false, map[string]int64{"": 300})
	}
}

func TestPartialAndUnavailableSources(t *testing.T) {
	t.Parallel()
	procRoot, cgroupRoot, dir := fakeCgroup(t, "/init")
	reader, _ := newCollectors(t, procRoot, cgroupRoot, io.Discard)
	collect(t, reader)

	// Bad fields cannot suppress good fields, wrap into negative int64 points,
	// or leave a previous value in the export. New kernel fields are ignored.
	writeFile(t, filepath.Join(dir, "cpu.stat"), "usage_usec 150\nuser_usec broken\nsystem_usec 9223372036854775808\nthrottled_usec -1\nnr_periods 10\nfuture_field not-a-number\n")
	writeFile(t, filepath.Join(dir, "memory.stat"), "anon 120\nfile invalid\nkernel\nshmem 10\n")
	writeFile(t, filepath.Join(dir, "memory.events"), "oom 1\noom_kill nope\n")
	for _, source := range []string{"memory.peak", "memory.swap.current", "memory.swap.peak"} {
		require.NoError(t, os.Remove(filepath.Join(dir, source)))
	}

	metrics := collect(t, reader)
	assertMetric(t, metrics, "dagger.engine.cpu.time", "us", "cpu.mode", true, map[string]int64{"total": 150})
	assertMetric(t, metrics, "dagger.engine.cpu.periods", "1", "cpu.period", true, map[string]int64{"total": 10})
	assertMetric(t, metrics, "dagger.engine.memory.current", "By", "", false, map[string]int64{"": 200})
	assertMetric(t, metrics, "dagger.engine.memory.breakdown", "By", "memory.type", false, map[string]int64{"anon": 120, "shmem": 10})
	assertMetric(t, metrics, "dagger.engine.memory.events", "1", "memory.event", true, map[string]int64{"oom": 1})
	assertMetric(t, metrics, "dagger.engine.resource_metrics.available", "1", "source", false,
		map[string]int64{
			"cpu.stat": 0, "memory.current": 1, "memory.peak": 0, "memory.stat": 0,
			"memory.swap.current": 0, "memory.swap.peak": 0, "memory.events": 0, "cgroup.stat": 1,
		})
	require.NotContains(t, metrics, "dagger.engine.memory.peak")
	require.NotContains(t, metrics, "dagger.engine.memory.swap.current")
	require.NotContains(t, metrics, "dagger.engine.memory.swap.peak")

	require.NoError(t, os.Remove(filepath.Join(dir, "cpu.stat")))
	writeFile(t, filepath.Join(dir, "memory.current"), "18446744073709551615\n")
	metrics = collect(t, reader)
	require.NotContains(t, metrics, "dagger.engine.cpu.time")
	require.NotContains(t, metrics, "dagger.engine.memory.current")
	require.Contains(t, metrics, "dagger.engine.memory.events")
}

func TestAvailabilityTransitions(t *testing.T) {
	t.Parallel()
	procRoot, cgroupRoot, dir := fakeCgroup(t, "/engine")
	for _, source := range []string{"memory.peak", "memory.swap.current", "memory.swap.peak"} {
		require.NoError(t, os.Remove(filepath.Join(dir, source)))
	}
	var logs bytes.Buffer
	reader, _ := newCollectors(t, procRoot, cgroupRoot, &logs)
	logs.Reset() // The boundary is logged once at registration.
	collect(t, reader)
	require.Empty(t, logs.String(), "missing optional files must not produce initial warnings")

	require.NoError(t, os.Remove(filepath.Join(dir, "memory.current")))
	collect(t, reader)
	collect(t, reader)
	var record map[string]any
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record), "one transition, not repeated warnings")
	require.Equal(t, "WARN", record["level"])
	require.Equal(t, "memory.current", record["source"])

	logs.Reset()
	writeFile(t, filepath.Join(dir, "memory.current"), "25\n")
	metrics := collect(t, reader)
	collect(t, reader)
	require.NoError(t, json.Unmarshal(bytes.TrimSpace(logs.Bytes()), &record))
	require.Equal(t, "INFO", record["level"])
	require.Equal(t, "memory.current", record["source"])
	assertMetric(t, metrics, "dagger.engine.memory.current", "By", "", false, map[string]int64{"": 25})
}

func TestDiscoveryFailureRecovers(t *testing.T) {
	t.Parallel()
	procRoot, cgroupRoot, _ := fakeCgroup(t, "/engine")
	writeFile(t, filepath.Join(procRoot, "self/cgroup"), "5:cpu:/legacy\n")
	var logs bytes.Buffer
	reader, _ := newCollectors(t, procRoot, cgroupRoot, &logs)
	logs.Reset()
	metrics := collect(t, reader)
	collect(t, reader)
	require.Len(t, metrics, 1, "no resource samples should exist without a boundary")
	assertMetric(t, metrics, "dagger.engine.resource_metrics.available", "1", "source", false,
		map[string]int64{
			"cpu.stat": 0, "memory.current": 0, "memory.peak": 0, "memory.stat": 0,
			"memory.swap.current": 0, "memory.swap.peak": 0, "memory.events": 0, "cgroup.stat": 0,
		})
	require.Empty(t, logs.String(), "discovery retries must not repeat warnings")

	writeFile(t, filepath.Join(procRoot, "self/cgroup"), "0::/engine\n")
	metrics = collect(t, reader)
	assertMetric(t, metrics, "dagger.engine.cpu.time", "us", "cpu.mode", true,
		map[string]int64{"total": 100, "user": 90, "system": 30})
}

func fakeCgroup(t *testing.T, path string) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	procRoot := filepath.Join(root, "proc")
	cgroupRoot := filepath.Join(root, "cgroup")
	dir := filepath.Join(cgroupRoot, strings.TrimPrefix(path, "/"))
	require.NoError(t, os.MkdirAll(filepath.Join(procRoot, "self"), 0o755))
	require.NoError(t, os.MkdirAll(dir, 0o755))
	writeFile(t, filepath.Join(procRoot, "self/cgroup"), "0::"+path+"\n")
	for name, content := range map[string]string{
		"cgroup.controllers":  "cpu memory\n",
		"cpu.stat":            "usage_usec 100\nuser_usec 90\nsystem_usec 30\nthrottled_usec 4\nnr_periods 8\nnr_throttled 2\n",
		"memory.current":      "200\n",
		"memory.peak":         "300\n",
		"memory.swap.current": "10\n",
		"memory.swap.peak":    "20\n",
		"memory.stat":         "anon 120\nfile 50\nkernel 50\nshmem 10\npgfault 999\nfuture_field ignored\n",
		"memory.events":       "low 1\nhigh 2\nmax 3\noom 4\noom_kill 5\noom_group_kill 6\nunknown 99\n",
		"cgroup.stat":         "nr_descendants 0\nnr_dying_descendants 1\n",
	} {
		writeFile(t, filepath.Join(dir, name), content)
	}
	return procRoot, cgroupRoot, dir
}

func newCollectors(t *testing.T, procRoot, cgroupRoot string, logs io.Writer) (*sdkmetric.ManualReader, *sdkmetric.ManualReader) {
	t.Helper()
	first, second := sdkmetric.NewManualReader(), sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(first), sdkmetric.WithReader(second))
	_, err := register(t.Context(), provider.Meter(InstrumentationScopeName), procRoot, cgroupRoot, slog.New(slog.NewJSONHandler(logs, nil)).Log)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, provider.Shutdown(t.Context())) })
	return first, second
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.WriteFile(path, []byte(content), 0o644))
}

func collect(t *testing.T, reader *sdkmetric.ManualReader) map[string]metricdata.Metrics {
	t.Helper()
	var result metricdata.ResourceMetrics
	require.NoError(t, reader.Collect(t.Context(), &result))
	require.Len(t, result.ScopeMetrics, 1)
	require.Equal(t, "dagger.io/engine.resources", result.ScopeMetrics[0].Scope.Name)
	metrics := make(map[string]metricdata.Metrics)
	for _, m := range result.ScopeMetrics[0].Metrics {
		metrics[m.Name] = m
	}
	return metrics
}

func assertMetric(t *testing.T, metrics map[string]metricdata.Metrics, name, unit, attrKey string, counter bool, want map[string]int64) {
	t.Helper()
	require.Contains(t, metrics, name)
	m := metrics[name]
	require.Equal(t, unit, m.Unit, name)
	var points []metricdata.DataPoint[int64]
	if counter {
		sum, ok := m.Data.(metricdata.Sum[int64])
		require.True(t, ok, "%s has type %T, want Sum[int64]", name, m.Data)
		require.True(t, sum.IsMonotonic, name)
		require.Equal(t, metricdata.CumulativeTemporality, sum.Temporality, name)
		points = sum.DataPoints
	} else {
		gauge, ok := m.Data.(metricdata.Gauge[int64])
		require.True(t, ok, "%s has type %T, want Gauge[int64]", name, m.Data)
		points = gauge.DataPoints
	}
	got := map[string]int64{}
	for _, point := range points {
		attrs := point.Attributes.ToSlice()
		var key string
		if attrKey == "" {
			require.Empty(t, attrs, name)
		} else {
			require.Len(t, attrs, 1, name)
			require.Equal(t, attrKey, string(attrs[0].Key), name)
			key = attrs[0].Value.AsString()
		}
		got[key] = point.Value
	}
	require.Equal(t, want, got, name)
}
