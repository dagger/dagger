package dagql

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/slog"
)

const benchmarkMetadataHeapSampleInterval = 5 * time.Millisecond

type benchmarkMetadataFixture string

const (
	benchmarkMetadataMinimal benchmarkMetadataFixture = "minimal-scalar"
	benchmarkMetadataGlob    benchmarkMetadataFixture = "glob-no-match"
	benchmarkMetadataRich    benchmarkMetadataFixture = "rich-call"
	benchmarkMetadataShared  benchmarkMetadataFixture = "shared-dependency"
)

var benchmarkMetadataEstimateSink CacheMetadataEstimate

func BenchmarkCacheMetadataEstimate(b *testing.B) {
	for _, resultCount := range []int{200_000, 1_000_000} {
		b.Run(strconv.Itoa(resultCount), func(b *testing.B) {
			result := &sharedResult{}
			term := &egraphTerm{}
			c := &Cache{
				resultsByID:   make(map[sharedResultID]*sharedResult, resultCount),
				egraphTerms:   make(map[egraphTermID]*egraphTerm, resultCount),
				egraphParents: make([]eqClassID, resultCount+1),
			}
			for i := range resultCount {
				c.resultsByID[sharedResultID(i+1)] = result
				c.egraphTerms[egraphTermID(i+1)] = term
			}

			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				benchmarkMetadataEstimateSink = c.MetadataEstimate()
			}
			b.StopTimer()
			b.ReportMetric(float64(benchmarkMetadataEstimateSink.EstimatedBytes), "estimated-B")
		})
	}
}

func BenchmarkCachePruneSnapshot(b *testing.B) {
	for _, mode := range []struct {
		name string
		mode pruneSnapshotMode
	}{
		{name: "metadata", mode: pruneSnapshotMetadata},
		{name: "disk", mode: pruneSnapshotDisk},
	} {
		b.Run(mode.name, func(b *testing.B) {
			for _, resultCount := range []int{200_000, 1_000_000} {
				b.Run(strconv.Itoa(resultCount), func(b *testing.B) {
					b.ReportAllocs()
					for range b.N {
						b.StopTimer()
						c, ctx, _ := benchmarkMetadataPruneCache(b, resultCount, benchmarkMetadataMinimal, "", io.Discard)
						estimate := c.MetadataEstimate()
						directResultBytes := int64(0)
						if mode.mode == pruneSnapshotMetadata {
							directResultBytes = metadataDirectResultBytes(estimate)
						}

						b.StartTimer()
						snapshot := c.snapshotPruneState(nil, mode.mode, directResultBytes)
						b.StopTimer()
						if len(snapshot.results) != resultCount {
							b.Fatalf("snapshot results: got %d, want %d", len(snapshot.results), resultCount)
						}
						b.ReportMetric(float64(estimate.EstimatedBytes), "estimated-B")
						b.ReportMetric(float64(len(snapshot.results)), "snapshot-results")
						runtime.KeepAlive(ctx)
						runtime.KeepAlive(snapshot)
					}
				})
			}
		})
	}
}

// Run each scale sub-benchmark with -benchtime=1x in a fresh process. The
// retained-heap deltas use the process state before that one fixture as their
// control, while B/op and allocs/op cover only PruneMetadataEstimate itself.
func BenchmarkCacheMetadataPrune(b *testing.B) {
	for _, resultCount := range []int{200_000, 1_000_000} {
		b.Run(strconv.Itoa(resultCount), func(b *testing.B) {
			benchmarkCacheMetadataFixtureLifecycle(b, resultCount, benchmarkMetadataMinimal)
		})
	}
}

// BenchmarkCacheMetadataFixtureCalibration exercises the metadata-dominated
// call shapes used to calibrate the coarse coefficient bundle. Run each
// sub-benchmark with -benchtime=1x in a fresh process.
func BenchmarkCacheMetadataFixtureCalibration(b *testing.B) {
	for _, fixture := range []benchmarkMetadataFixture{
		benchmarkMetadataMinimal,
		benchmarkMetadataGlob,
		benchmarkMetadataRich,
		benchmarkMetadataShared,
	} {
		b.Run(string(fixture), func(b *testing.B) {
			for _, resultCount := range []int{200_000, 1_000_000} {
				b.Run(strconv.Itoa(resultCount), func(b *testing.B) {
					benchmarkCacheMetadataFixtureLifecycle(b, resultCount, fixture)
				})
			}
		})
	}
}

func benchmarkCacheMetadataFixtureLifecycle(b *testing.B, resultCount int, fixture benchmarkMetadataFixture) {
	b.Helper()
	b.ReportAllocs()
	for range b.N {
		b.StopTimer()
		baseline := benchmarkMetadataMemStats()
		var automaticLog bytes.Buffer
		c, ctx, persistedRootCount := benchmarkMetadataPruneCache(b, resultCount, fixture, "", &automaticLog)
		before := c.MetadataEstimate()
		populated := benchmarkMetadataMemStats()
		runtime.KeepAlive(c)
		stopHeapSampler := benchmarkMetadataStartHeapSampler(populated.HeapInuse)

		b.StartTimer()
		report, err := c.PruneMetadataEstimate(ctx, before.EstimatedBytes-1, 1)
		b.StopTimer()
		peakHeapInuse := stopHeapSampler()
		if err != nil {
			b.Fatal(err)
		}
		if report.RemovedPersistedRootCount != persistedRootCount {
			b.Fatalf("removed roots: got %d, want %d", report.RemovedPersistedRootCount, persistedRootCount)
		}
		if report.AfterPrune.ResultCount != 0 {
			b.Fatalf("results after prune: got %d, want 0", report.AfterPrune.ResultCount)
		}

		retained := benchmarkMetadataMemStats()
		runtime.KeepAlive(c)

		setupHeapDelta := positiveDelta(populated.HeapAlloc, baseline.HeapAlloc)
		reclaimedHeap := positiveDelta(populated.HeapAlloc, retained.HeapAlloc)
		estimateDelta := positiveDelta(
			uint64(report.AfterInitialCompaction.EstimatedBytes),
			uint64(report.AfterPrune.EstimatedBytes),
		)
		peakScratchHeapInuse := positiveDelta(peakHeapInuse, populated.HeapInuse)

		b.ReportMetric(float64(before.ResultCount), "before-results")
		b.ReportMetric(float64(before.TermCount), "before-terms")
		b.ReportMetric(float64(before.ClassSlotCount), "before-class-slots")
		b.ReportMetric(float64(before.EstimatedBytes), "before-estimated-B")
		b.ReportMetric(float64(report.AfterPrune.ResultCount), "after-results")
		b.ReportMetric(float64(report.AfterPrune.TermCount), "after-terms")
		b.ReportMetric(float64(report.AfterPrune.ClassSlotCount), "after-class-slots")
		b.ReportMetric(float64(report.AfterPrune.EstimatedBytes), "after-estimated-B")
		b.ReportMetric(float64(setupHeapDelta), "setup-HeapAlloc-B")
		b.ReportMetric(float64(populated.HeapInuse), "setup-HeapInuse-B")
		b.ReportMetric(float64(peakScratchHeapInuse), "peak-scratch-HeapInuse-B")
		b.ReportMetric(float64(reclaimedHeap), "reclaimed-HeapAlloc-B")
		b.ReportMetric(float64(report.InitialCompactionOldClassSlots), "initial-old-class-slots")
		b.ReportMetric(float64(report.InitialCompactionNewClassSlots), "initial-new-class-slots")
		b.ReportMetric(float64(report.FinalCompactionOldClassSlots), "final-old-class-slots")
		b.ReportMetric(float64(report.FinalCompactionNewClassSlots), "final-new-class-slots")
		b.ReportMetric(float64(report.RemovedPersistedRootCount), "removed-roots")
		b.ReportMetric(float64(report.Duration.Nanoseconds()), "reported-pass-ns")
		b.ReportMetric(float64(automaticLog.Len()), "automatic-log-B")
		b.ReportMetric(float64(benchmarkMetadataHeapSampleInterval.Nanoseconds()), "heap-sample-interval-ns")
		if setupHeapDelta > 0 {
			b.ReportMetric(float64(before.EstimatedBytes)/float64(setupHeapDelta), "setup-estimate/heap")
		}
		if reclaimedHeap > 0 {
			b.ReportMetric(float64(estimateDelta)/float64(reclaimedHeap), "prune-estimate/heap")
		}
	}
}

// BenchmarkCacheMetadataChurn records the baseline-to-peak,
// peak-to-session-release, and forced-compaction transitions required to
// calibrate the class-slot coefficient. Run with -benchtime=1x.
func BenchmarkCacheMetadataChurn(b *testing.B) {
	const (
		unpruneableFloor = 2_000
		churnCount       = 200_000
	)
	b.ReportAllocs()
	for range b.N {
		b.StopTimer()
		ctx := benchmarkMetadataContext(b, io.Discard)
		c, err := NewCache(ctx, "", nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkMetadataPopulateUnpruneableFloor(b, c, ctx, unpruneableFloor)

		floorEstimate := c.MetadataEstimate()
		floorStats := benchmarkMetadataMemStats()
		floorControl := benchmarkMetadataMemStats()
		noopHeapNoise := absoluteDelta(floorControl.HeapAlloc, floorStats.HeapAlloc)

		b.StartTimer()
		benchmarkMetadataPopulateTransient(b, c, ctx, churnCount)
		b.StopTimer()
		peakEstimate := c.MetadataEstimate()
		peakStats := benchmarkMetadataMemStats()

		releaseStarted := time.Now()
		if err := c.ReleaseSession(ctx, "metadata-churn"); err != nil {
			b.Fatal(err)
		}
		releaseDuration := time.Since(releaseStarted)
		afterReleaseEstimate := c.MetadataEstimate()
		afterReleaseStats := benchmarkMetadataMemStats()

		compactionStarted := time.Now()
		c.egraphMu.Lock()
		compacted, oldClassSlots, newClassSlots := c.compactEqClassesLocked(true)
		c.egraphMu.Unlock()
		compactionDuration := time.Since(compactionStarted)
		afterCompactionEstimate := c.MetadataEstimate()
		afterCompactionStats := benchmarkMetadataMemStats()
		if !compacted {
			b.Fatal("forced compaction did not compact churned class slots")
		}

		growthEstimate := absoluteDelta(uint64(peakEstimate.EstimatedBytes), uint64(floorEstimate.EstimatedBytes))
		growthHeap := positiveDelta(peakStats.HeapAlloc, floorControl.HeapAlloc)
		releaseEstimate := positiveDelta(uint64(peakEstimate.EstimatedBytes), uint64(afterReleaseEstimate.EstimatedBytes))
		releaseHeap := positiveDelta(peakStats.HeapAlloc, afterReleaseStats.HeapAlloc)
		compactionEstimate := positiveDelta(uint64(afterReleaseEstimate.EstimatedBytes), uint64(afterCompactionEstimate.EstimatedBytes))
		compactionHeap := positiveDelta(afterReleaseStats.HeapAlloc, afterCompactionStats.HeapAlloc)

		b.ReportMetric(float64(noopHeapNoise), "noop-HeapAlloc-noise-B")
		b.ReportMetric(float64(floorEstimate.EstimatedBytes), "floor-estimated-B")
		b.ReportMetric(float64(floorControl.HeapAlloc), "floor-HeapAlloc-B")
		b.ReportMetric(float64(peakEstimate.EstimatedBytes), "peak-estimated-B")
		b.ReportMetric(float64(peakStats.HeapAlloc), "peak-HeapAlloc-B")
		b.ReportMetric(float64(afterReleaseEstimate.EstimatedBytes), "released-estimated-B")
		b.ReportMetric(float64(afterReleaseStats.HeapAlloc), "released-HeapAlloc-B")
		b.ReportMetric(float64(afterCompactionEstimate.EstimatedBytes), "compacted-estimated-B")
		b.ReportMetric(float64(afterCompactionStats.HeapAlloc), "compacted-HeapAlloc-B")
		b.ReportMetric(float64(oldClassSlots), "compaction-old-class-slots")
		b.ReportMetric(float64(newClassSlots), "compaction-new-class-slots")
		b.ReportMetric(float64(releaseDuration.Nanoseconds()), "release-ns")
		b.ReportMetric(float64(compactionDuration.Nanoseconds()), "compaction-ns")
		if growthHeap > noopHeapNoise {
			b.ReportMetric(float64(growthEstimate)/float64(growthHeap), "growth-estimate/heap")
		}
		if releaseHeap > noopHeapNoise {
			b.ReportMetric(float64(releaseEstimate)/float64(releaseHeap), "release-estimate/heap")
		}
		if compactionHeap > noopHeapNoise {
			b.ReportMetric(float64(compactionEstimate)/float64(compactionHeap), "compaction-estimate/heap")
		}
		runtime.KeepAlive(c)
	}
}

// BenchmarkCacheMetadataSparseForcedCompaction measures the write-lock hold
// for forced compaction of a genuinely sparse one-million-slot graph. Run with
// -benchtime=1x in a fresh process.
func BenchmarkCacheMetadataSparseForcedCompaction(b *testing.B) {
	const (
		allocatedClassSlots = 1_000_000
		unpruneableFloor    = 2_000
	)
	b.ReportAllocs()
	var totalWriteLockHold time.Duration
	for range b.N {
		b.StopTimer()
		ctx := benchmarkMetadataContext(b, io.Discard)
		c, err := NewCache(ctx, "", nil, nil)
		if err != nil {
			b.Fatal(err)
		}
		benchmarkMetadataPopulateUnpruneableFloor(b, c, ctx, unpruneableFloor)
		benchmarkMetadataPopulateTransient(b, c, ctx, allocatedClassSlots-unpruneableFloor)
		if err := c.ReleaseSession(ctx, "metadata-churn"); err != nil {
			b.Fatal(err)
		}

		before := c.MetadataEstimate()
		if before.ClassSlotCount != allocatedClassSlots {
			b.Fatalf("allocated class slots: got %d, want %d", before.ClassSlotCount, allocatedClassSlots)
		}
		if before.ResultCount != unpruneableFloor {
			b.Fatalf("live results: got %d, want %d", before.ResultCount, unpruneableFloor)
		}

		b.StartTimer()
		c.egraphMu.Lock()
		writeLockStarted := time.Now()
		compacted, oldClassSlots, newClassSlots := c.compactEqClassesLocked(true)
		c.egraphMu.Unlock()
		totalWriteLockHold += time.Since(writeLockStarted)
		b.StopTimer()
		if !compacted {
			b.Fatal("forced compaction did not compact sparse class slots")
		}
		if oldClassSlots != allocatedClassSlots {
			b.Fatalf("old class slots: got %d, want %d", oldClassSlots, allocatedClassSlots)
		}
		if newClassSlots != unpruneableFloor {
			b.Fatalf("new class slots: got %d, want %d", newClassSlots, unpruneableFloor)
		}
		after := c.MetadataEstimate()
		b.ReportMetric(float64(oldClassSlots), "old-class-slots")
		b.ReportMetric(float64(newClassSlots), "new-class-slots")
		b.ReportMetric(float64(after.EstimatedBytes), "after-estimated-B")
		runtime.KeepAlive(c)
	}
	b.ReportMetric(float64(totalWriteLockHold.Nanoseconds())/float64(b.N), "write-lock-hold-ns")
}

func benchmarkMetadataPopulateUnpruneableFloor(b *testing.B, c *Cache, ctx context.Context, resultCount int) {
	b.Helper()
	for i := range resultCount {
		call := cacheTestIntCall("metadata-churn-floor-" + strconv.Itoa(i))
		res, err := c.GetOrInitCall(ctx, "metadata-churn-floor", noopTypeResolver{}, &CallRequest{
			ResultCall:    call,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(call, i), nil
		})
		if err != nil {
			b.Fatal(err)
		}
		if err := c.MakeResultUnpruneable(ctx, res); err != nil {
			b.Fatal(err)
		}
	}
	if err := c.ReleaseSession(ctx, "metadata-churn-floor"); err != nil {
		b.Fatal(err)
	}
}

func benchmarkMetadataPopulateTransient(b *testing.B, c *Cache, ctx context.Context, resultCount int) {
	b.Helper()
	for i := range resultCount {
		call := cacheTestIntCall("metadata-churn-" + strconv.Itoa(i))
		_, err := c.GetOrInitCall(ctx, "metadata-churn", noopTypeResolver{}, &CallRequest{
			ResultCall: call,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(call, i), nil
		})
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkCacheMetadataSchema17Import measures importing the unchanged
// current persistence schema at the same two scale points. Run each
// sub-benchmark with -benchtime=1x in a fresh process.
func BenchmarkCacheMetadataSchema17Import(b *testing.B) {
	if cachePersistenceSchemaVersion != "17" {
		b.Fatalf("benchmark expects persistence schema 17, got %s", cachePersistenceSchemaVersion)
	}
	for _, resultCount := range []int{200_000, 1_000_000} {
		b.Run(strconv.Itoa(resultCount), func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				b.StopTimer()
				dbPath := filepath.Join(b.TempDir(), "cache.db")
				c, ctx, _ := benchmarkMetadataPruneCache(b, resultCount, benchmarkMetadataMinimal, dbPath, io.Discard)
				beforeClose := c.MetadataEstimate()
				if err := c.Close(context.Background()); err != nil {
					b.Fatal(err)
				}
				c = nil
				baseline := benchmarkMetadataMemStats()
				stopHeapSampler := benchmarkMetadataStartHeapSampler(baseline.HeapInuse)

				b.StartTimer()
				imported, err := NewCache(ctx, dbPath, nil, nil)
				b.StopTimer()
				peakHeapInuse := stopHeapSampler()
				if err != nil {
					b.Fatal(err)
				}
				afterImport := imported.MetadataEstimate()
				if afterImport != beforeClose {
					b.Fatalf("imported estimate: got %+v, want %+v", afterImport, beforeClose)
				}
				retained := benchmarkMetadataMemStats()
				dbStat, err := filepath.Glob(dbPath + "*")
				if err != nil {
					b.Fatal(err)
				}
				var dbBytes int64
				for _, path := range dbStat {
					info, err := os.Stat(path)
					if err != nil {
						b.Fatal(err)
					}
					dbBytes += info.Size()
				}

				b.ReportMetric(17, "schema-version")
				b.ReportMetric(float64(afterImport.ResultCount), "imported-results")
				b.ReportMetric(float64(afterImport.TermCount), "imported-terms")
				b.ReportMetric(float64(afterImport.ClassSlotCount), "imported-class-slots")
				b.ReportMetric(float64(afterImport.EstimatedBytes), "imported-estimated-B")
				b.ReportMetric(float64(positiveDelta(retained.HeapAlloc, baseline.HeapAlloc)), "imported-HeapAlloc-B")
				b.ReportMetric(float64(positiveDelta(peakHeapInuse, baseline.HeapInuse)), "peak-import-HeapInuse-B")
				b.ReportMetric(float64(dbBytes), "database-B")
				if err := imported.Close(context.Background()); err != nil {
					b.Fatal(err)
				}
				runtime.KeepAlive(imported)
			}
		})
	}
}

func benchmarkMetadataPruneCache(b *testing.B, resultCount int, fixture benchmarkMetadataFixture, dbPath string, logWriter io.Writer) (*Cache, context.Context, int) {
	b.Helper()
	ctx := benchmarkMetadataContext(b, logWriter)
	c, err := NewCache(ctx, dbPath, nil, nil)
	if err != nil {
		b.Fatal(err)
	}
	const sessionID = "metadata-prune-benchmark"
	persistedRootCount := resultCount
	var sharedDependency AnyResult
	if fixture == benchmarkMetadataGlob || fixture == benchmarkMetadataShared {
		call := cacheTestIntCall(string(fixture) + "-receiver")
		sharedDependency, err = c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{
			ResultCall: call,
		}, func(context.Context) (AnyResult, error) {
			return cacheTestIntResult(call, 0), nil
		})
		if err != nil {
			b.Fatal(err)
		}
		persistedRootCount--
	}

	for i := range persistedRootCount {
		call, result := benchmarkMetadataFixtureCall(fixture, i, sharedDependency)
		_, err := c.GetOrInitCall(ctx, sessionID, noopTypeResolver{}, &CallRequest{
			ResultCall:    call,
			IsPersistable: true,
		}, func(context.Context) (AnyResult, error) {
			return result, nil
		})
		if err != nil {
			b.Fatalf("populate result %d: %v", i, err)
		}
	}
	if err := c.ReleaseSession(ctx, sessionID); err != nil {
		b.Fatal(err)
	}
	return c, ctx, persistedRootCount
}

func benchmarkMetadataFixtureCall(fixture benchmarkMetadataFixture, i int, sharedDependency AnyResult) (*ResultCall, AnyResult) {
	switch fixture {
	case benchmarkMetadataMinimal:
		call := cacheTestIntCall("metadata-prune-benchmark-" + strconv.Itoa(i))
		return call, cacheTestIntResult(call, i)

	case benchmarkMetadataGlob:
		// This is the cache-level shape of a no-match Directory.glob: a
		// receiver dependency, unique pattern argument, and empty string array.
		call := &ResultCall{
			Kind:     ResultCallKindField,
			Type:     NewResultCallType(NewStringArray().Type()),
			Field:    "glob",
			Receiver: benchmarkMetadataResultRef(sharedDependency),
			Args: []*ResultCallArg{{
				Name: "patterns",
				Value: &ResultCallLiteral{
					Kind:        ResultCallLiteralKindString,
					StringValue: "missing-" + strconv.Itoa(i),
				},
			}},
		}
		result, err := NewResultForCall(NewStringArray(), call)
		if err != nil {
			panic(err)
		}
		return call, result

	case benchmarkMetadataRich:
		call := cacheTestIntCall("rich")
		call.Args = make([]*ResultCallArg, 8)
		for j := range call.Args {
			call.Args[j] = &ResultCallArg{
				Name: fmt.Sprintf("arg-%02d", j),
				Value: &ResultCallLiteral{
					Kind:        ResultCallLiteralKindString,
					StringValue: fmt.Sprintf("%08d-%02d-%s", i, j, strings.Repeat("x", 64)),
				},
			}
		}
		return call, cacheTestIntResult(call, i)

	case benchmarkMetadataShared:
		call := cacheTestIntCall("shared")
		call.Receiver = benchmarkMetadataResultRef(sharedDependency)
		call.Args = []*ResultCallArg{{
			Name: "key",
			Value: &ResultCallLiteral{
				Kind:        ResultCallLiteralKindString,
				StringValue: strconv.Itoa(i),
			},
		}}
		return call, cacheTestIntResult(call, i)

	default:
		panic("unknown metadata benchmark fixture: " + string(fixture))
	}
}

func benchmarkMetadataResultRef(result AnyResult) *ResultCallRef {
	if result == nil {
		return nil
	}
	return &ResultCallRef{ResultID: uint64(result.cacheSharedResult().id)}
}

func benchmarkMetadataContext(b *testing.B, logWriter io.Writer) context.Context {
	b.Helper()
	return cacheTestContext(slog.WithLogger(
		b.Context(),
		slog.New(slog.NewTextHandler(logWriter, nil)),
	))
}

func benchmarkMetadataMemStats() runtime.MemStats {
	runtime.GC()
	debug.FreeOSMemory()
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return stats
}

func benchmarkMetadataStartHeapSampler(initialHeapInuse uint64) func() uint64 {
	stop := make(chan struct{})
	done := make(chan uint64, 1)
	go func() {
		peakHeapInuse := initialHeapInuse
		ticker := time.NewTicker(benchmarkMetadataHeapSampleInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				if stats.HeapInuse > peakHeapInuse {
					peakHeapInuse = stats.HeapInuse
				}
			case <-stop:
				var stats runtime.MemStats
				runtime.ReadMemStats(&stats)
				if stats.HeapInuse > peakHeapInuse {
					peakHeapInuse = stats.HeapInuse
				}
				done <- peakHeapInuse
				return
			}
		}
	}()
	return func() uint64 {
		close(stop)
		return <-done
	}
}

func positiveDelta(after, before uint64) uint64 {
	if after <= before {
		return 0
	}
	return after - before
}

func absoluteDelta(a, b uint64) uint64 {
	if a >= b {
		return a - b
	}
	return b - a
}
