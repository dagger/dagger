package dagql

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"golang.org/x/sync/errgroup"
)

func TestCacheUsagePruneProgressDuringPublication(t *testing.T) {
	for _, phase := range []string{"measurement", "snapshot"} {
		t.Run(phase, func(t *testing.T) {
			ctx := cacheTestContext(t.Context())
			c, err := NewCache(ctx, "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			v := &usageCallbackValue{Int: NewInt(1), callback: func(string) {}}
			t.Cleanup(func() {
				v.callback = func(string) {}
				if err := c.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			frame := cacheTestIntCall("retained-usage")
			res, err := c.GetOrInitCall(ctx, "test-session", noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) { return cacheTestDetachedResult(frame, v), nil })
			if err != nil {
				t.Fatal(err)
			}
			cacheTestReleaseSession(t, c, ctx)
			c.egraphMu.RLock()
			_, retained := c.persistedEdgesByResult[res.cacheSharedResult().id]
			c.egraphMu.RUnlock()
			if !retained {
				t.Fatal("fixture lacks a persisted candidate")
			}
			if phase == "snapshot" {
				if err := c.measureAllResultSizes(ctx); err != nil {
					t.Fatal(err)
				}
			}
			publications, identityCalls := 0, 0
			v.callback = func(kind string) {
				if kind == "identities" {
					identityCalls++
				}
				inject := phase == "measurement" && kind == "size" || phase == "snapshot" && kind == "identities" && identityCalls%2 == 0
				if !inject {
					return
				}
				pubCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				group, gctx := errgroup.WithContext(pubCtx)
				for range 4 {
					publications++
					frame := cacheTestIntCall(fmt.Sprintf("concurrent-scalar-%d", publications))
					group.Go(func() error {
						_, err := c.GetOrInitCall(gctx, "publisher", noopTypeResolver{}, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) { return cacheTestDetachedResult(frame, NewInt(2)), nil })
						return err
					})
				}
				if err := group.Wait(); err != nil {
					t.Fatal(err)
				}
			}
			policies := []CachePrunePolicy{{All: true, MaxUsedSpace: 32}}
			const attempts = 8
			var reclaimed int64
			for range attempts {
				report, err := c.Prune(ctx, policies)
				if err != nil {
					t.Fatal(err)
				}
				reclaimed += report.ReclaimedBytes
				if reclaimed > 0 {
					break
				}
			}
			c.egraphMu.RLock()
			var measured int64
			for _, size := range res.cacheSharedResult().cacheUsageSizeByIdentity {
				measured += size
			}
			c.egraphMu.RUnlock()
			t.Logf("phase=%s publications=%d measured=%d reclaimed=%d", phase, publications, measured, reclaimed)
			// A quiescent control must reclaim the same retained fixture; lack of
			// progress above must not be an ineligible-candidate setup error.
			v.callback = func(string) {}
			if reclaimed == 0 {
				control, err := c.Prune(ctx, policies)
				if err != nil {
					t.Fatal(err)
				}
				if control.ReclaimedBytes != 64 {
					t.Fatalf("invalid control: reclaimed=%d", control.ReclaimedBytes)
				}
				t.Fatalf("prune starved for %d passes during %s publication", attempts, phase)
			}
			if reclaimed != 64 {
				t.Fatalf("reclaimed=%d, want64", reclaimed)
			}
		})
	}
}

func publishProgressValue(t *testing.T, ctx context.Context, c *Cache, session, field string, value Typed, persist bool) AnyResult {
	t.Helper()
	frame := cacheTestIntCall(field)
	res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: frame, IsPersistable: persist}, func(context.Context) (AnyResult, error) { return cacheTestDetachedResult(frame, value), nil })
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestCacheUsagePruneNewSharerPreventsFalseReclaim(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	v := &usageCallbackValue{Int: NewInt(1), callback: func(string) {}}
	publishProgressValue(t, ctx, c, "test-session", "shared", v, true)
	private := cacheTestSizedInt{Int: NewInt(2), usageIdentities: []string{"private"}, sizeByIdentity: map[string]int64{"private": 100}}
	publishProgressValue(t, ctx, c, "test-session", "private", private, true)
	cacheTestReleaseSession(t, c, ctx)
	var once sync.Once
	v.callback = func(kind string) {
		if kind == "size" {
			once.Do(func() {
				publishProgressValue(t, ctx, c, "holder", "new-sharer", &usageCallbackValue{Int: NewInt(3), callback: func(string) {}}, false)
			})
		}
	}
	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true, MaxUsedSpace: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReclaimedBytes != 100 {
		t.Fatalf("reclaimed=%d, want only private100; shared64 still live", report.ReclaimedBytes)
	}
	var remaining int64
	for _, entry := range c.UsageEntriesAll(ctx) {
		remaining += entry.SizeBytes
	}
	if remaining != 64 {
		t.Fatalf("remaining=%d, want shared64", remaining)
	}
}

func TestCacheUsagePruneUnknownProviderExhaustionDefers(t *testing.T) {
	for _, phase := range []string{"measurement", "snapshot"} {
		t.Run(phase, func(t *testing.T) {
			ctx := cacheTestContext(t.Context())
			c, err := NewCache(ctx, "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			v := &usageCallbackValue{Int: NewInt(1), callback: func(string) {}}
			res := publishProgressValue(t, ctx, c, "test-session", "retained", v, true)
			cacheTestReleaseSession(t, c, ctx)
			if err := c.measureAllResultSizes(ctx); err != nil {
				t.Fatal(err)
			}
			calls, published := 0, 0
			var callback func(string)
			callback = func(kind string) {
				if kind != "identities" {
					return
				}
				calls++
				if phase == "snapshot" && calls == 1 {
					return
				}
				published++
				publishProgressValue(t, ctx, c, "churn", fmt.Sprintf("provider-%d", published), &usageCallbackValue{Int: NewInt(published + 1), callback: callback}, false)
			}
			v.callback = callback
			report, err := c.Prune(ctx, []CachePrunePolicy{{All: true, MaxUsedSpace: 32}})
			if err != nil {
				t.Fatal(err)
			}
			if len(report.Entries) != 0 || report.ReclaimedBytes != 0 {
				t.Fatalf("incomplete membership must not plan eviction: %+v", report)
			}
			if published != cacheUsageMaxSamplingRounds {
				t.Fatalf("published=%d, rounds not bounded by%d", published, cacheUsageMaxSamplingRounds)
			}
			c.egraphMu.RLock()
			_, retained := c.persistedEdgesByResult[res.cacheSharedResult().id]
			size := res.cacheSharedResult().cacheUsageSizeByIdentity["shared-snapshot"]
			c.egraphMu.RUnlock()
			if !retained || size != 64 {
				t.Fatalf("deferred pass changed root/prior size: retained=%v size=%d", retained, size)
			}
			if c.activeGlobalOperations.Load() != 0 {
				t.Fatal("incomplete sample leaked operation")
			}
		})
	}
}

func TestCacheUsageSnapshotReassignsCollectedSizeOwner(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	v := &usageCallbackValue{Int: NewInt(1), callback: func(string) {}}
	old := publishProgressValue(t, ctx, c, "old-owner", "first", v, false)
	other := publishProgressValue(t, ctx, c, "holder", "second", &usageCallbackValue{Int: NewInt(2), callback: func(string) {}}, false)
	if err := c.measureAllResultSizes(ctx); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	v.callback = func(kind string) {
		if kind == "identities" {
			once.Do(func() {
				if err := c.ReleaseSession(ctx, "old-owner"); err != nil {
					t.Error(err)
				}
			})
		}
	}
	snapshot, err := c.snapshotPruneStateCancelable(nil, pruneSnapshotDisk, 0, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshot.results[old.cacheSharedResult().id]; ok {
		t.Fatal("collected owner remains in graph snapshot")
	}
	if got := snapshot.results[other.cacheSharedResult().id].entry.SizeBytes; got != 64 {
		t.Fatalf("surviving owner's size=%d, want64", got)
	}
	if snapshot.usedBytes != 64 {
		t.Fatalf("usedBytes=%d, want64", snapshot.usedBytes)
	}
}

func TestCacheUsagePruneCancellationPropagates(t *testing.T) {
	for _, phase := range []string{"before", "provider"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			c, err := NewCache(ctx, "", nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := c.Close(context.Background()); err != nil {
					t.Error(err)
				}
			})
			released := false
			v := &usageCallbackValue{Int: NewInt(1), callback: func(string) {}, onRelease: func() { released = true }}
			publishProgressValue(t, ctx, c, "owner", "cancel", v, false)
			if phase == "before" {
				cancel()
			} else {
				v.callback = func(kind string) {
					if kind == "identities" {
						if err := c.ReleaseSession(context.Background(), "owner"); err != nil {
							t.Error(err)
						}
						cancel()
					}
				}
			}
			_, err = c.Prune(ctx, []CachePrunePolicy{{All: true}})
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("Prune cancellation=%v", err)
			}
			if c.activeGlobalOperations.Load() != 0 {
				t.Fatal("cancellation leaked operation")
			}
			if phase == "provider" && (!released || c.Size() != 0) {
				t.Fatal("cancellation leaked held provider")
			}
		})
	}
}
