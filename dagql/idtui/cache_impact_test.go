package idtui

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/dagui"
	"github.com/dagger/dagger/engine/telemetryattrs"
	telemetry "github.com/dagger/otel-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestCacheImpactUsesWorkflowMakespan(t *testing.T) {
	oldPath := cacheImpactFile
	cacheImpactFile = filepath.Join(t.TempDir(), "cache-impact.json")
	t.Cleanup(func() { cacheImpactFile = oldPath })

	cold := cacheImpactTestDB(t, 10*time.Second, 8*time.Second,
		telemetryattrs.CacheOutcomeExecuted, telemetryattrs.CacheOutcomeExecuted)
	cold.MetricsByCall = cacheImpactTestMetrics(90*time.Second, 1_200_000_000)
	addMemoryTestSeries(cold.MetricsByCall, "branch-a", 2, startForCacheImpactTest(), 10*time.Second, 4_000_000_000)
	addMemoryTestSeries(cold.MetricsByCall, "branch-b", 3, startForCacheImpactTest(), 8*time.Second, 2_000_000_000)
	coldFE := NewWithDB(io.Discard, cold)
	coldFE.prepareCacheImpact()
	if coldFE.cacheImpact != nil {
		t.Fatal("cold run unexpectedly has cache impact")
	}

	// The 10s branch becomes a hit while unrelated 8s work remains. The saved
	// wall time is therefore 2s, not the cold branch's full 10s duration.
	warm := cacheImpactTestDB(t, 100*time.Millisecond, 8*time.Second,
		telemetryattrs.CacheOutcomeHit, telemetryattrs.CacheOutcomeExecuted)
	warm.MetricsByCall = cacheImpactTestMetrics(30*time.Second, 200_000_000)
	addMemoryTestSeries(warm.MetricsByCall, "branch-b", 3, startForCacheImpactTest(), 8*time.Second, 2_000_000_000)
	warmFE := NewWithDB(io.Discard, warm)
	warmFE.prepareCacheImpact()

	impact := warmFE.cacheImpact
	if impact == nil {
		t.Fatal("warm run has no cache impact")
	}
	if impact.Elapsed != 2*time.Second {
		t.Fatalf("elapsed saved = %s, want 2s", impact.Elapsed)
	}
	if impact.CPU != 60*time.Second {
		t.Fatalf("CPU saved = %s, want 1m", impact.CPU)
	}
	if impact.NetworkBytes != 1_000_000_000 {
		t.Fatalf("network saved = %d, want 1000000000", impact.NetworkBytes)
	}
	if impact.MemoryBytes != 4_000_000_000 || impact.MemoryPeriod != 10*time.Second {
		t.Fatalf("memory saved = %g bytes for %s, want 4000000000 bytes for 10s", impact.MemoryBytes, impact.MemoryPeriod)
	}

	got := strings.Join(warmFE.cacheReport(false), "\n")
	for _, want := range []string{
		"ESTIMATED CACHE SAVINGS",
		"Compared with a colder run (0% cache hit rate)",
		"Finished ~2s faster (20%)",
		"Compute avoided: ~1m of CPU work",
		"Network transfer avoided: ~1.0 GB",
		"Memory occupancy avoided: equivalent to ~4.0 GB held for 10s",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("cache report missing %q:\n%s", want, got)
		}
	}
}

func TestSaveCacheImpactProfileReplacesCorruptStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cache-impact.json")
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	want := cacheRunProfile{Key: "workflow", Elapsed: time.Second, RecordedAt: time.Now()}
	if err := saveCacheImpactProfile(path, want); err != nil {
		t.Fatal(err)
	}
	got, found, err := loadCacheImpactProfile(path, want.Key)
	if err != nil {
		t.Fatal(err)
	}
	if !found || got.Elapsed != want.Elapsed {
		t.Fatalf("profile = %+v, found %v", got, found)
	}
}

func TestIntegrateMemorySeriesUsesSampleIntervals(t *testing.T) {
	start := time.Unix(100, 0)
	points := []metricdata.DataPoint[int64]{
		{Time: start.Add(5 * time.Second), Value: 4_000_000_000},
		{Time: start.Add(10 * time.Second), Value: 2_000_000_000},
	}
	got, ok := integrateMemorySeries(points, start, start.Add(10*time.Second))
	if !ok {
		t.Fatal("memory series was not integrated")
	}
	// Samples describe the interval since the preceding sample: 4 GB for five
	// seconds, then 2 GB for five seconds.
	if want := float64(30_000_000_000); got != want {
		t.Fatalf("memory byte-seconds = %g, want %g", got, want)
	}
}

func TestMemoryByteSecondsTreatsNoContainersAsZero(t *testing.T) {
	db := dagui.NewDB()
	got, available := memoryByteSeconds(db)
	if !available || got != 0 {
		t.Fatalf("memory byte-seconds = %g, available %v; want zero, true", got, available)
	}
}

func cacheImpactTestDB(t *testing.T, first, second time.Duration, firstOutcome, secondOutcome string) *dagui.DB {
	t.Helper()
	db := dagui.NewDB()
	start := startForCacheImpactTest()
	root := prettyTestSpanID(1)
	db.ImportSnapshots([]dagui.SpanSnapshot{
		{ID: root, TraceID: prettyTestTraceID(), Name: "workflow", StartTime: start, EndTime: start.Add(max(first, second)), Final: true},
		{
			ID: prettyTestSpanID(2), TraceID: prettyTestTraceID(), ParentID: root,
			StartTime: start, EndTime: start.Add(first), Final: true,
			CacheContract: telemetryattrs.CacheContractV1, CacheOutcome: firstOutcome,
		},
		{
			ID: prettyTestSpanID(3), TraceID: prettyTestTraceID(), ParentID: root,
			StartTime: start, EndTime: start.Add(second), Final: true,
			CacheContract: telemetryattrs.CacheContractV1, CacheOutcome: secondOutcome,
		},
	})
	db.SetPrimarySpan(root)
	return db
}

func startForCacheImpactTest() time.Time {
	return time.Unix(100, 0)
}

func cacheImpactTestMetrics(cpu time.Duration, network int64) map[string]map[string][]metricdata.DataPoint[int64] {
	return map[string]map[string][]metricdata.DataPoint[int64]{
		"call": {
			telemetry.CPUStatUsage:   {{Value: cpu.Microseconds()}},
			telemetry.NetstatRxBytes: {{Value: network / 2}},
			telemetry.NetstatTxBytes: {{Value: network - network/2}},
		},
	}
}

func addMemoryTestSeries(
	metrics map[string]map[string][]metricdata.DataPoint[int64],
	call string,
	spanID byte,
	start time.Time,
	duration time.Duration,
	bytes int64,
) {
	attrs := attribute.NewSet(attribute.String(telemetry.MetricsSpanIDAttr, prettyTestSpanID(spanID).String()))
	metrics[call] = map[string][]metricdata.DataPoint[int64]{
		telemetry.MemoryCurrentBytes: {
			{Attributes: attrs, Time: start.Add(min(5*time.Second, duration)), Value: bytes},
			{Attributes: attrs, Time: start.Add(duration), Value: bytes},
		},
	}
}
