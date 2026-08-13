package dagql

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/opencontainers/go-digest"
)

func egraphBenchmarkScales(b *testing.B, approved []int) []int {
	b.Helper()
	raw := os.Getenv("DAGGER_EGRAPH_BENCH_SCALE")
	if raw == "" {
		return approved
	}
	want, err := strconv.Atoi(raw)
	if err != nil {
		b.Fatalf("DAGGER_EGRAPH_BENCH_SCALE must be an integer: %v", err)
	}
	for _, scale := range approved {
		if scale == want {
			return []int{want}
		}
	}
	// A top-level benchmark may contain several fixture families with distinct
	// approved scales. An explicitly selected scale simply disables families
	// that do not contain it; the runner verifies that the requested benchmark
	// emitted a result line.
	return nil
}

func egraphBenchmarkRequireOneIteration(b *testing.B) {
	b.Helper()
	if b.N != 1 {
		b.Fatalf("bounded egraph benchmark requires -benchtime=1x, got b.N=%d", b.N)
	}
}

func BenchmarkCacheEGraphRelease(b *testing.B) {
	for _, n := range egraphBenchmarkScales(b, []int{10_000, 50_000, 200_000}) {
		for _, mode := range []egraphBenchmarkPersistence{egraphBenchmarkTransient, egraphBenchmarkImported} {
			b.Run(fmt.Sprintf("independent/%s/%d", mode, n), func(b *testing.B) {
				egraphBenchmarkRequireOneIteration(b)
				guard := newEgraphBenchmarkGuard(b)
				f := egraphBenchmarkIndependentFixture(b, n, mode, guard)
				defer f.close(b)
				if mode == egraphBenchmarkImported {
					egraphBenchmarkPreparePersistedRelease(b, f, "independent-imported-release")
				}
				egraphBenchmarkFinishSetup(b, guard)
				egraphBenchmarkRunMeasured(b, f, len(f.allResultIDs)+64, func() error {
					return f.cache.ReleaseSession(f.ctx, f.setupSession)
				})
				if got := len(f.cache.resultsByID); got != 0 {
					b.Fatalf("independent results after release: got %d, want 0", got)
				}
			})
		}
	}

	for _, n := range egraphBenchmarkScales(b, []int{1_000, 10_000, 50_000}) {
		for _, shape := range []egraphBenchmarkOwnershipShape{egraphBenchmarkChain, egraphBenchmarkStarFanout, egraphBenchmarkStarShared} {
			b.Run(fmt.Sprintf("%s/transient/%d", shape, n), func(b *testing.B) {
				egraphBenchmarkRequireOneIteration(b)
				guard := newEgraphBenchmarkGuard(b)
				f := egraphBenchmarkOwnershipFixture(b, n, shape, guard)
				defer f.close(b)
				egraphBenchmarkFinishSetup(b, guard)
				egraphBenchmarkRunMeasured(b, f, n+64, func() error {
					return f.cache.ReleaseSession(f.ctx, f.setupSession)
				})
				if got := len(f.cache.resultsByID); got != 0 {
					b.Fatalf("%s results after release: got %d, want 0", shape, got)
				}
			})
		}
	}

	for _, width := range egraphBenchmarkScales(b, []int{64, 128, 256, 512, 1024, 2048}) {
		for _, mode := range []egraphBenchmarkPersistence{egraphBenchmarkTransient, egraphBenchmarkImported} {
			b.Run(fmt.Sprintf("wide-output/%s/%d", mode, width), func(b *testing.B) {
				egraphBenchmarkRequireOneIteration(b)
				guard := newEgraphBenchmarkGuard(b)
				f := egraphBenchmarkWideOutputFixture(b, width, mode, guard)
				defer f.close(b)
				if mode == egraphBenchmarkImported {
					egraphBenchmarkPreparePersistedRelease(b, f, "wide-output-imported-release")
				}
				egraphBenchmarkFinishSetup(b, guard)
				egraphBenchmarkRunMeasured(b, f, len(f.allResultIDs)+64, func() error {
					return f.cache.ReleaseSession(f.ctx, f.setupSession)
				})
				if got := len(f.cache.resultsByID); got != 0 {
					b.Fatalf("wide-output results after release: got %d, want 0", got)
				}
			})
		}
	}

	for _, width := range egraphBenchmarkScales(b, []int{1_000, 10_000}) {
		b.Run(fmt.Sprintf("wide-digest/transient/%d", width), func(b *testing.B) {
			egraphBenchmarkRequireOneIteration(b)
			guard := newEgraphBenchmarkGuard(b)
			f := egraphBenchmarkWideDigestFixture(b, width, guard)
			defer f.close(b)
			egraphBenchmarkFinishSetup(b, guard)
			egraphBenchmarkRunMeasured(b, f, 64, func() error {
				return f.cache.ReleaseSession(f.ctx, f.setupSession)
			})
			if got := len(f.cache.resultsByID); got != 0 {
				b.Fatalf("wide-digest results after release: got %d, want 0", got)
			}
		})
	}
}

type egraphBenchmarkLookupRoute string

const (
	egraphBenchmarkLookupExact      egraphBenchmarkLookupRoute = "exact-recipe"
	egraphBenchmarkLookupExtra      egraphBenchmarkLookupRoute = "shared-extra"
	egraphBenchmarkLookupStructural egraphBenchmarkLookupRoute = "structural"
)

func BenchmarkCacheEGraphLookup(b *testing.B) {
	for _, width := range egraphBenchmarkScales(b, []int{64, 128, 256, 512, 1024, 2048}) {
		for _, mode := range []egraphBenchmarkPersistence{egraphBenchmarkTransient, egraphBenchmarkImported} {
			for _, route := range []egraphBenchmarkLookupRoute{egraphBenchmarkLookupExact, egraphBenchmarkLookupExtra, egraphBenchmarkLookupStructural} {
				for _, repeated := range []bool{false, true} {
					ownership := "fresh-session"
					if repeated {
						ownership = "same-session-repeat"
					}
					b.Run(fmt.Sprintf("%s/%s/%s/%d", route, mode, ownership, width), func(b *testing.B) {
						egraphBenchmarkRequireOneIteration(b)
						guard := newEgraphBenchmarkGuard(b)
						f := egraphBenchmarkWideOutputFixture(b, width, mode, guard)
						defer f.close(b)
						egraphBenchmarkFinishSetup(b, guard)
						sessionID := "lookup-fresh"
						if repeated {
							sessionID = "lookup-repeat"
							warmRoute := route
							// A structural hit teaches the probed recipe onto its
							// result. Warm ownership through an existing exact recipe
							// so the measured request remains a structural lookup.
							if route == egraphBenchmarkLookupStructural {
								warmRoute = egraphBenchmarkLookupExact
							}
							if err := egraphBenchmarkRunLookup(f, warmRoute, sessionID); err != nil {
								b.Fatalf("warm repeated lookup: %v", err)
							}
						}
						egraphBenchmarkRunMeasured(b, f, 64, func() error {
							return egraphBenchmarkRunLookup(f, route, sessionID)
						})
					})
				}
			}
		}
	}
}

func egraphBenchmarkRunLookup(f *egraphBenchmarkFixture, route egraphBenchmarkLookupRoute, sessionID string) error {
	switch route {
	case egraphBenchmarkLookupExact:
		initCalls := 0
		decision := NewCacheDecision()
		res, err := f.cache.GetOrInitCall(f.ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: f.exactRequests[0], CacheEvidence: decision}, func(context.Context) (AnyResult, error) {
			initCalls++
			return nil, fmt.Errorf("exact lookup executed resolver")
		})
		if err != nil {
			return err
		}
		if initCalls != 0 || !res.HitCache() {
			return fmt.Errorf("exact lookup: initCalls=%d hit=%v", initCalls, res.HitCache())
		}
		if decision.HitRoute != CacheHitRouteRecipe {
			return fmt.Errorf("exact lookup used route %q", decision.HitRoute)
		}
		return nil
	case egraphBenchmarkLookupExtra:
		_, hit, err := f.cache.lookupCacheForDigests(f.ctx, sessionID, noopTypeResolver{}, digest.FromString("egraph-wide-extra-miss"), []call.ExtraDigest{{
			Label:  call.ExtraDigestLabelContent,
			Digest: f.sharedOutputDigest,
		}})
		if err != nil {
			return err
		}
		if !hit {
			return fmt.Errorf("shared-extra lookup missed")
		}
		return nil
	case egraphBenchmarkLookupStructural:
		initCalls := 0
		decision := NewCacheDecision()
		res, err := f.cache.GetOrInitCall(f.ctx, sessionID, noopTypeResolver{}, &CallRequest{ResultCall: f.structuralRequest, CacheEvidence: decision}, func(context.Context) (AnyResult, error) {
			initCalls++
			return nil, fmt.Errorf("structural lookup executed resolver")
		})
		if err != nil {
			return err
		}
		if initCalls != 0 || !res.HitCache() {
			return fmt.Errorf("structural lookup: initCalls=%d hit=%v", initCalls, res.HitCache())
		}
		if decision.HitRoute != CacheHitRouteStructural {
			return fmt.Errorf("structural lookup used route %q", decision.HitRoute)
		}
		return nil
	default:
		return fmt.Errorf("unknown lookup route %q", route)
	}
}

func BenchmarkCacheEGraphIDLoad(b *testing.B) {
	for _, width := range egraphBenchmarkScales(b, []int{64, 128, 256, 512, 1024, 2048}) {
		for _, mode := range []egraphBenchmarkPersistence{egraphBenchmarkTransient, egraphBenchmarkImported} {
			for _, repeated := range []bool{false, true} {
				ownership := "fresh-session"
				if repeated {
					ownership = "same-session-repeat"
				}
				b.Run(fmt.Sprintf("direct/%s/%s/%d", mode, ownership, width), func(b *testing.B) {
					egraphBenchmarkRequireOneIteration(b)
					guard := newEgraphBenchmarkGuard(b)
					f := egraphBenchmarkWideOutputFixture(b, width, mode, guard)
					defer f.close(b)
					egraphBenchmarkFinishSetup(b, guard)
					resultID := f.outputResultIDs[len(f.outputResultIDs)-1]
					sessionID := "id-load-fresh"
					if repeated {
						sessionID = "id-load-repeat"
						if _, err := f.cache.loadResultByResultID(f.ctx, sessionID, nil, uint64(resultID)); err != nil {
							b.Fatalf("warm ID load: %v", err)
						}
					}
					egraphBenchmarkRunMeasured(b, f, 64, func() error {
						_, err := f.cache.loadResultByResultID(f.ctx, sessionID, nil, uint64(resultID))
						return err
					})
				})

				b.Run(fmt.Sprintf("receiver/%s/%s/%d", mode, ownership, width), func(b *testing.B) {
					egraphBenchmarkRequireOneIteration(b)
					guard := newEgraphBenchmarkGuard(b)
					f, child := egraphBenchmarkReceiverFixture(b, width, mode, guard)
					defer f.close(b)
					egraphBenchmarkFinishSetup(b, guard)
					sessionID := "receiver-fresh"
					if repeated {
						sessionID = "receiver-repeat"
						ctx := egraphBenchmarkSessionContext(f.ctx, sessionID)
						if _, err := child.Receiver(ctx, f.server); err != nil {
							b.Fatalf("warm Receiver: %v", err)
						}
					}
					egraphBenchmarkRunMeasured(b, f, 64, func() error {
						ctx := egraphBenchmarkSessionContext(f.ctx, sessionID)
						_, err := child.Receiver(ctx, f.server)
						return err
					})
				})
			}
		}
	}
}

func BenchmarkCacheEGraphPublication(b *testing.B) {
	for _, width := range egraphBenchmarkScales(b, []int{64, 128, 256, 512, 1024, 2048}) {
		for _, joinsWideClass := range []bool{false, true} {
			name := "distinct-class"
			if joinsWideClass {
				name = "join-wide-class"
			}
			b.Run(fmt.Sprintf("publish/%s/%d", name, width), func(b *testing.B) {
				egraphBenchmarkRequireOneIteration(b)
				guard := newEgraphBenchmarkGuard(b)
				f := egraphBenchmarkWideOutputFixture(b, width, egraphBenchmarkTransient, guard)
				defer f.close(b)
				egraphBenchmarkFinishSetup(b, guard)
				request := cacheTestIntCall(fmt.Sprintf("egraph-publication-request-%s-%d", name, width))
				response := cacheTestIntCall(fmt.Sprintf("egraph-publication-response-%s-%d", name, width))
				if joinsWideClass {
					response.ExtraDigests = []call.ExtraDigest{{Label: call.ExtraDigestLabelContent, Digest: f.sharedOutputDigest}}
				}
				var resolverCalls atomic.Int32
				egraphBenchmarkRunMeasured(b, f, 64, func() error {
					_, err := f.cache.GetOrInitCall(f.ctx, "publication-measure", noopTypeResolver{}, &CallRequest{ResultCall: request}, func(context.Context) (AnyResult, error) {
						resolverCalls.Add(1)
						return cacheTestIntResult(response, width+1), nil
					})
					return err
				})
				if resolverCalls.Load() != 1 {
					b.Fatalf("publication resolver calls: got %d, want 1", resolverCalls.Load())
				}
			})
		}
	}

	for _, termCount := range egraphBenchmarkScales(b, []int{1_000, 10_000, 50_000}) {
		for _, mergeCount := range []int{1, 8, 64} {
			for _, mode := range []egraphBenchmarkPersistence{egraphBenchmarkTransient, egraphBenchmarkImported} {
				b.Run(fmt.Sprintf("popular-input/%s/terms-%d/merges-%d", mode, termCount, mergeCount), func(b *testing.B) {
					egraphBenchmarkRequireOneIteration(b)
					guard := newEgraphBenchmarkGuard(b)
					f := egraphBenchmarkPopularInputFixture(b, termCount, mergeCount, mode, guard)
					defer f.close(b)
					egraphBenchmarkFinishSetup(b, guard)
					shared := digest.FromString("egraph-popular-input-content")
					egraphBenchmarkRunMeasured(b, f, mergeCount+64, func() error {
						for i, alias := range f.aliases {
							if err := f.cache.TeachContentDigest(f.ctx, alias, shared); err != nil {
								return fmt.Errorf("teach alias %d: %w", i, err)
							}
						}
						return nil
					})
				})
			}
		}
	}
}

func BenchmarkCacheEGraphPrune(b *testing.B) {
	for _, n := range egraphBenchmarkScales(b, []int{10_000, 50_000, 200_000}) {
		for _, mode := range []egraphBenchmarkPersistence{egraphBenchmarkPersistedFresh, egraphBenchmarkImported} {
			b.Run(fmt.Sprintf("independent/%s/%d", mode, n), func(b *testing.B) {
				egraphBenchmarkRequireOneIteration(b)
				guard := newEgraphBenchmarkGuard(b)
				f := egraphBenchmarkIndependentFixture(b, n, mode, guard)
				defer f.close(b)
				egraphBenchmarkFinishSetup(b, guard)
				egraphBenchmarkRunMetadataPrune(b, f)
			})
		}
	}

	for _, n := range egraphBenchmarkScales(b, []int{1_000, 10_000, 50_000}) {
		for _, shape := range []egraphBenchmarkOwnershipShape{egraphBenchmarkChain, egraphBenchmarkStarFanout, egraphBenchmarkStarShared} {
			b.Run(fmt.Sprintf("%s/in-memory-persisted-roots/%d", shape, n), func(b *testing.B) {
				egraphBenchmarkRequireOneIteration(b)
				guard := newEgraphBenchmarkGuard(b)
				f := egraphBenchmarkOwnershipFixture(b, n, shape, guard)
				defer f.close(b)
				egraphBenchmarkPersistOwnershipRoots(b, f, shape)
				egraphBenchmarkFinishSetup(b, guard)
				egraphBenchmarkRunMetadataPrune(b, f)
			})
		}
	}

	for _, width := range egraphBenchmarkScales(b, []int{64, 128, 256, 512, 1024, 2048}) {
		for _, mode := range []egraphBenchmarkPersistence{egraphBenchmarkPersistedFresh, egraphBenchmarkImported} {
			b.Run(fmt.Sprintf("wide-output/%s/%d", mode, width), func(b *testing.B) {
				egraphBenchmarkRequireOneIteration(b)
				guard := newEgraphBenchmarkGuard(b)
				f := egraphBenchmarkWideOutputFixture(b, width, mode, guard)
				defer f.close(b)
				egraphBenchmarkFinishSetup(b, guard)
				egraphBenchmarkRunMetadataPrune(b, f)
			})
		}
	}
}

func egraphBenchmarkRunMetadataPrune(b *testing.B, f *egraphBenchmarkFixture) {
	b.Helper()
	estimate := f.cache.MetadataEstimate()
	if estimate.EstimatedBytes <= 1 {
		b.Fatalf("metadata estimate too small to prune: %+v", estimate)
	}
	var report CacheMetadataPruneReport
	egraphBenchmarkRunMeasured(b, f, len(f.allResultIDs)+128, func() error {
		var err error
		report, err = f.cache.PruneMetadataEstimate(f.ctx, estimate.EstimatedBytes-1, 1)
		return err
	})
	b.ReportMetric(float64(report.CandidateCount), "prune-candidates")
	b.ReportMetric(float64(report.PlannedRootCount), "prune-planned-roots")
	b.ReportMetric(float64(report.RemovedPersistedRootCount), "prune-removed-roots")
	b.ReportMetric(float64(report.SimulatedCollectedResultCount), "prune-simulated-results")
	if report.AfterPrune.ResultCount != 0 {
		b.Fatalf("results after metadata prune: got %d, want 0", report.AfterPrune.ResultCount)
	}
}

func BenchmarkCacheEGraphPolicyPruneRepresentative(b *testing.B) {
	for _, n := range egraphBenchmarkScales(b, []int{1_000, 10_000}) {
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			egraphBenchmarkRequireOneIteration(b)
			guard := newEgraphBenchmarkGuard(b)
			f := &egraphBenchmarkFixture{setupSession: "policy-prune-setup"}
			f.cache, f.ctx = egraphBenchmarkNewCache(b, f.setupSession, "", nil)
			for i := range n {
				guard.checkpoint(i)
				frame := cacheTestIntCall("egraph-policy-prune-" + strconv.Itoa(i))
				identity := "egraph-policy-identity-" + strconv.Itoa(i)
				res, err := f.cache.GetOrInitCall(f.ctx, f.setupSession, noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
					return cacheTestSizedIntResult(frame, i, 1, identity, nil), nil
				})
				if err != nil {
					b.Fatalf("publish policy-prune result %d: %v", i, err)
				}
				egraphBenchmarkAppendResult(f, res)
			}
			if err := f.cache.ReleaseSession(f.ctx, f.setupSession); err != nil {
				b.Fatal(err)
			}
			defer f.close(b)
			egraphBenchmarkFinishSetup(b, guard)
			var report CachePruneReport
			egraphBenchmarkRunMeasured(b, f, n+128, func() error {
				var err error
				report, err = f.cache.Prune(f.ctx, []CachePrunePolicy{{All: true}})
				return err
			})
			b.ReportMetric(float64(len(report.Entries)), "policy-prune-entries")
			b.ReportMetric(float64(report.ReclaimedBytes), "policy-prune-reclaimed-B")
			if len(f.cache.resultsByID) != 0 {
				b.Fatalf("results after policy prune: got %d, want 0", len(f.cache.resultsByID))
			}
		})
	}
}

func BenchmarkCacheEGraphInstrumentationOverhead(b *testing.B) {
	orders := [][]string{
		{"disabled", "sampled", "full"},
		{"sampled", "full", "disabled"},
		{"full", "disabled", "sampled"},
		{"disabled", "full", "sampled"},
		{"sampled", "disabled", "full"},
	}
	for pair, order := range orders {
		b.Run(fmt.Sprintf("pair-%d", pair+1), func(b *testing.B) {
			for _, instrumentation := range order {
				b.Run(instrumentation, func(b *testing.B) {
					f := egraphBenchmarkWideOutputFixture(b, 64, egraphBenchmarkTransient, nil)
					defer f.close(b)
					const sessionID = "overhead-session"
					if err := egraphBenchmarkRunLookup(f, egraphBenchmarkLookupExact, sessionID); err != nil {
						b.Fatalf("overhead warmup: %v", err)
					}
					// Keep identical recorder backing arrays live in every arm so the
					// overhead estimate is not confounded by different live-heap sizes
					// and GC trigger points. Only instrumented arms install the observer.
					recorder := newEgraphBenchmarkLockRecorder(2*b.N + 128)
					installObserver := false
					switch instrumentation {
					case "disabled":
					case "sampled":
						recorder.recordDetails = false
						recorder.sampleEvery = 32
						installObserver = true
					case "full":
						installObserver = true
					default:
						b.Fatalf("unknown instrumentation configuration %q", instrumentation)
					}
					if installObserver {
						f.cache.egraphLockObserver = recorder
					}
					b.ReportAllocs()
					b.ResetTimer()
					for range b.N {
						if err := egraphBenchmarkRunLookup(f, egraphBenchmarkLookupExact, sessionID); err != nil {
							b.Fatal(err)
						}
					}
					b.StopTimer()
					f.cache.egraphLockObserver = nil
					b.ReportMetric(float64(cap(recorder.observations)), "calibration-recorder-capacity")
					runtime.KeepAlive(recorder)
					if installObserver {
						egraphBenchmarkReportLocks(b, recorder)
					}
				})
			}
		})
	}
}

func BenchmarkCacheEGraphSteadyState(b *testing.B) {
	const operationsPerWorker = 1_000
	maxWorkers := 24
	if runtime.NumCPU() < maxWorkers {
		maxWorkers = runtime.NumCPU()
	}
	for _, mode := range []egraphBenchmarkPersistence{egraphBenchmarkTransient, egraphBenchmarkImported} {
		for _, operation := range []string{"exact-recipe", "id-load"} {
			for _, workers := range []int{1, maxWorkers} {
				b.Run(fmt.Sprintf("%s/%s/workers-%d", operation, mode, workers), func(b *testing.B) {
					egraphBenchmarkRequireOneIteration(b)
					guard := newEgraphBenchmarkGuard(b)
					f := egraphBenchmarkWideOutputFixture(b, 64, mode, guard)
					defer f.close(b)
					egraphBenchmarkFinishSetup(b, guard)
					egraphBenchmarkRunSteadyState(b, f, operation, workers, operationsPerWorker)
				})
			}
		}
	}
}

func egraphBenchmarkRunSteadyState(b *testing.B, f *egraphBenchmarkFixture, operation string, workers, operationsPerWorker int) {
	b.Helper()
	for worker := range workers {
		sessionID := fmt.Sprintf("steady-warm-%d", worker)
		switch operation {
		case "exact-recipe":
			if err := egraphBenchmarkRunLookup(f, egraphBenchmarkLookupExact, sessionID); err != nil {
				b.Fatalf("warm exact lookup: %v", err)
			}
		case "id-load":
			if _, err := f.cache.loadResultByResultID(f.ctx, sessionID, nil, uint64(f.outputResultIDs[0])); err != nil {
				b.Fatalf("warm ID load: %v", err)
			}
		default:
			b.Fatalf("unknown steady-state operation %q", operation)
		}
	}

	totalOperations := workers * operationsPerWorker
	widths := egraphBenchmarkWidthsForCache(f.cache)
	targetOutput := egraphBenchmarkTargetOutputShapeForFixture(f)
	expectedLockAttempts := totalOperations
	if operation == "exact-recipe" {
		expectedLockAttempts *= 2
	}
	recorder := newEgraphBenchmarkSampledLockRecorder(expectedLockAttempts+128, 32)
	f.cache.egraphLockObserver = recorder
	latencies := make([]time.Duration, totalOperations)
	start := make(chan struct{})
	errs := make(chan error, workers)
	var done sync.WaitGroup
	done.Add(workers)
	b.ReportAllocs()
	b.StartTimer()
	started := time.Now()
	for worker := range workers {
		go func(worker int) {
			defer done.Done()
			<-start
			sessionID := fmt.Sprintf("steady-warm-%d", worker)
			for i := range operationsPerWorker {
				operationStarted := time.Now()
				var err error
				switch operation {
				case "exact-recipe":
					err = egraphBenchmarkRunLookup(f, egraphBenchmarkLookupExact, sessionID)
				case "id-load":
					_, err = f.cache.loadResultByResultID(f.ctx, sessionID, nil, uint64(f.outputResultIDs[0]))
				}
				latencies[worker*operationsPerWorker+i] = time.Since(operationStarted)
				if err != nil {
					errs <- err
					return
				}
			}
		}(worker)
	}
	close(start)
	done.Wait()
	duration := time.Since(started)
	b.StopTimer()
	f.cache.egraphLockObserver = nil
	close(errs)
	for err := range errs {
		if err != nil {
			b.Fatal(err)
		}
	}
	egraphBenchmarkReportWidths(b, widths)
	egraphBenchmarkReportTargetOutputShape(b, targetOutput)
	dist := egraphBenchmarkDurationDistributionFor(latencies)
	b.ReportMetric(float64(totalOperations), "foreground-operations")
	b.ReportMetric(float64(totalOperations)/duration.Seconds(), "foreground-ops/s")
	b.ReportMetric(float64(dist.P50NS), "foreground-p50-ns")
	b.ReportMetric(float64(dist.P95NS), "foreground-p95-ns")
	b.ReportMetric(float64(dist.P99NS), "foreground-p99-ns")
	b.ReportMetric(float64(dist.MaxNS), "foreground-max-ns")
	if duration > egraphBenchmarkOperationLimit {
		b.ReportMetric(1, "stop-limit-exceeded")
		b.Logf("EGRAPH_BENCH_STOP steady-state operation exceeded %s: %s", egraphBenchmarkOperationLimit, duration)
	}
	egraphBenchmarkReportLocks(b, recorder)
}

type egraphBenchmarkContentionOperation string

const (
	egraphBenchmarkContentionRelease egraphBenchmarkContentionOperation = "release"
	egraphBenchmarkContentionPrune   egraphBenchmarkContentionOperation = "prune"
	egraphBenchmarkContentionTeach   egraphBenchmarkContentionOperation = "teach"
)

func BenchmarkCacheEGraphContention(b *testing.B) {
	workers := 24
	if runtime.NumCPU() < workers {
		workers = runtime.NumCPU()
	}
	for _, workerCount := range []int{1, workers} {
		for _, operation := range []egraphBenchmarkContentionOperation{egraphBenchmarkContentionRelease, egraphBenchmarkContentionPrune, egraphBenchmarkContentionTeach} {
			b.Run(fmt.Sprintf("%s/workers-%d", operation, workerCount), func(b *testing.B) {
				egraphBenchmarkRequireOneIteration(b)
				guard := newEgraphBenchmarkGuard(b)
				var (
					f        *egraphBenchmarkFixture
					longOp   func() error
					signalOp string
				)
				switch operation {
				case egraphBenchmarkContentionRelease:
					f = egraphBenchmarkOwnershipFixture(b, 50_000, egraphBenchmarkChain, guard)
					longOp = func() error { return f.cache.ReleaseSession(f.ctx, f.setupSession) }
					signalOp = "release-session"
				case egraphBenchmarkContentionPrune:
					f = egraphBenchmarkIndependentFixture(b, 50_000, egraphBenchmarkPersistedFresh, guard)
					estimate := f.cache.MetadataEstimate()
					longOp = func() error {
						_, err := f.cache.PruneMetadataEstimate(f.ctx, estimate.EstimatedBytes-1, 1)
						return err
					}
					signalOp = "prune-cut"
				case egraphBenchmarkContentionTeach:
					f = egraphBenchmarkPopularInputFixture(b, 50_000, 1, egraphBenchmarkTransient, guard)
					longOp = func() error {
						return f.cache.TeachContentDigest(f.ctx, f.aliases[0], digest.FromString("egraph-popular-input-content"))
					}
					signalOp = "teach-content-digest"
				default:
					b.Fatalf("unknown contention operation %q", operation)
				}
				defer f.close(b)
				egraphBenchmarkFinishSetup(b, guard)
				egraphBenchmarkRunContention(b, f, workerCount, signalOp, longOp)
			})
		}
	}
}

func egraphBenchmarkRunContention(b *testing.B, f *egraphBenchmarkFixture, workers int, signalOp string, longOp func() error) {
	b.Helper()
	stableFrame := cacheTestIntCall("egraph-contention-stable")
	stable, err := f.cache.GetOrInitCall(f.ctx, "contention-stable", noopTypeResolver{}, &CallRequest{ResultCall: stableFrame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return cacheTestIntResult(stableFrame, 1), nil
	})
	if err != nil {
		b.Fatal(err)
	}
	if err := f.cache.MakeResultUnpruneable(f.ctx, stable); err != nil {
		b.Fatal(err)
	}
	if err := f.cache.ReleaseSession(f.ctx, "contention-stable"); err != nil {
		b.Fatal(err)
	}

	widths := egraphBenchmarkWidthsForCache(f.cache)
	targetOutput := egraphBenchmarkTargetOutputShapeForFixture(f)
	recorder := newEgraphBenchmarkSignalingRecorder(len(f.allResultIDs)+workers*4+128, signalOp)
	f.cache.egraphLockObserver = recorder
	b.StartTimer()
	latencies := make([]time.Duration, workers)
	errs := make(chan error, workers)
	abort := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(workers)
	var done sync.WaitGroup
	done.Add(workers)
	for i := range workers {
		go func(i int) {
			defer done.Done()
			ready.Done()
			select {
			case <-recorder.signal:
			case <-abort:
				return
			}
			started := time.Now()
			_, err := f.cache.GetOrInitCall(f.ctx, fmt.Sprintf("contention-foreground-%d", i), noopTypeResolver{}, &CallRequest{ResultCall: stableFrame}, func(context.Context) (AnyResult, error) {
				return nil, fmt.Errorf("foreground exact lookup missed")
			})
			latencies[i] = time.Since(started)
			if err != nil {
				errs <- err
			}
		}(i)
	}
	ready.Wait()
	longStarted := time.Now()
	longErr := longOp()
	longDuration := time.Since(longStarted)
	signaled := false
	select {
	case <-recorder.signal:
		signaled = true
	default:
	}
	if !signaled {
		close(abort)
	}
	done.Wait()
	close(errs)
	f.cache.egraphLockObserver = nil
	if longErr != nil {
		b.Fatal(longErr)
	}
	if !signaled {
		b.Fatalf("long operation completed without acquiring measured lock %q", signalOp)
	}
	for err := range errs {
		if err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	egraphBenchmarkReportWidths(b, widths)
	egraphBenchmarkReportTargetOutputShape(b, targetOutput)
	dist := egraphBenchmarkDurationDistributionFor(latencies)
	b.ReportMetric(float64(longDuration.Nanoseconds()), "long-operation-ns")
	b.ReportMetric(float64(workers), "foreground-latency-samples")
	if workers == 1 {
		b.ReportMetric(float64(dist.MaxNS), "foreground-latency-ns")
	} else {
		b.ReportMetric(float64(dist.P50NS), "foreground-latency-sample-p50-ns")
		b.ReportMetric(float64(dist.P95NS), "foreground-latency-sample-p95-ns")
		b.ReportMetric(float64(dist.MaxNS), "foreground-latency-sample-max-ns")
	}
	if longDuration > egraphBenchmarkOperationLimit {
		b.ReportMetric(1, "stop-limit-exceeded")
		b.Logf("EGRAPH_BENCH_STOP contention long operation exceeded %s: %s", egraphBenchmarkOperationLimit, longDuration)
	}
	egraphBenchmarkReportLocks(b, recorder)
}
