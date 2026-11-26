package dagql

import (
	"context"
	"errors"
	"testing"
	"time"
)

// An image pulled as two layers, with two execs stacked on top. Each result
// only reports its own top snapshot, as core values do.
func chainTestManager() *fakeSnapshotManager {
	return &fakeSnapshotManager{
		snapshotSizes: map[string]int64{
			"image-base": 100,
			"image-top":  50,
			"exec-1":     10,
			"exec-2":     20,
		},
		snapshotParents: map[string]string{
			"image-top": "image-base",
			"exec-1":    "image-top",
			"exec-2":    "exec-1",
		},
	}
}

func chainTestValue(snapshotID string, size int64) cacheTestSizedInt {
	return cacheTestSizedInt{
		Int:             NewInt(int(size)),
		usageIdentities: []string{snapshotID},
		sizeByIdentity:  map[string]int64{snapshotID: size},
	}
}

func chainTestCache(t *testing.T) (context.Context, *Cache) {
	t.Helper()
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", chainTestManager(), nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := c.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return ctx, c
}

func chainTestUsage(t *testing.T, ctx context.Context, c *Cache) (int64, map[int64]int) {
	t.Helper()
	var total int64
	bySize := map[int64]int{}
	for _, entry := range c.UsageEntriesAll(ctx) {
		total += entry.SizeBytes
		if entry.SizeBytes > 0 {
			bySize[entry.SizeBytes]++
		}
	}
	return total, bySize
}

// Lower layers count once, against the earliest result that retains them:
// the image result shows the whole image and each exec only what it added.
func TestCacheUsageCountsParentSnapshots(t *testing.T) {
	ctx, c := chainTestCache(t)
	publishProgressValue(t, ctx, c, "test-session", "from", chainTestValue("image-top", 50), true)
	publishProgressValue(t, ctx, c, "test-session", "exec1", chainTestValue("exec-1", 10), true)
	publishProgressValue(t, ctx, c, "test-session", "exec2", chainTestValue("exec-2", 20), true)
	cacheTestReleaseSession(t, c, ctx)

	total, bySize := chainTestUsage(t, ctx, c)
	if total != 180 {
		t.Fatalf("total=%d, want 180 (both image layers and both exec layers)", total)
	}
	if bySize[150] != 1 || bySize[10] != 1 || bySize[20] != 1 {
		t.Fatalf("entry sizes=%v, want image 150, exec1 10, exec2 20", bySize)
	}
}

// Pruning the top of a stack is credited with only its own layer while the
// results below it still retain theirs.
func TestCachePruneTopOfStackReclaimsOnlyItsLayer(t *testing.T) {
	ctx, c := chainTestCache(t)
	// Least recently used goes first, so publish the top of the stack first.
	publishProgressValue(t, ctx, c, "test-session", "exec2", chainTestValue("exec-2", 20), true)
	time.Sleep(time.Millisecond)
	publishProgressValue(t, ctx, c, "test-session", "exec1", chainTestValue("exec-1", 10), true)
	time.Sleep(time.Millisecond)
	publishProgressValue(t, ctx, c, "test-session", "from", chainTestValue("image-top", 50), true)
	cacheTestReleaseSession(t, c, ctx)

	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true, MaxUsedSpace: 179}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 || report.ReclaimedBytes != 20 {
		t.Fatalf("pruned %d entries reclaiming %d, want only the top exec reclaiming 20", len(report.Entries), report.ReclaimedBytes)
	}
	if total, _ := chainTestUsage(t, ctx, c); total != 160 {
		t.Fatalf("remaining=%d, want 160", total)
	}
}

// Once the results below are gone, the result that still retains their layers
// is charged for them, and pruning it is credited with all of them.
func TestCacheUsageChargesOrphanedLowerLayersToHolder(t *testing.T) {
	ctx, c := chainTestCache(t)
	publishProgressValue(t, ctx, c, "transient", "from", chainTestValue("image-top", 50), false)
	publishProgressValue(t, ctx, c, "transient", "exec1", chainTestValue("exec-1", 10), false)
	publishProgressValue(t, ctx, c, "test-session", "exec2", chainTestValue("exec-2", 20), true)
	if err := c.ReleaseSession(ctx, "transient"); err != nil {
		t.Fatal(err)
	}
	cacheTestReleaseSession(t, c, ctx)

	total, bySize := chainTestUsage(t, ctx, c)
	if total != 180 || bySize[180] != 1 {
		t.Fatalf("total=%d sizes=%v, want the remaining exec charged 180", total, bySize)
	}
	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true, MaxUsedSpace: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReclaimedBytes != 180 {
		t.Fatalf("reclaimed=%d, want 180", report.ReclaimedBytes)
	}
}

// Parent links carry over between passes, so steady-state passes do no
// lookups, and links of snapshots no longer in use are dropped.
func TestCacheUsageReusesParentLinksAcrossPasses(t *testing.T) {
	ctx, c := chainTestCache(t)
	manager := c.snapshotManager.(*fakeSnapshotManager)
	publishProgressValue(t, ctx, c, "test-session", "from", chainTestValue("image-top", 50), true)
	publishProgressValue(t, ctx, c, "transient", "exec2", chainTestValue("exec-2", 20), false)

	chainTestUsage(t, ctx, c)
	if len(manager.snapshotParentCalls) != 4 {
		t.Fatalf("first pass looked up %v, want each of the 4 chain snapshots once", manager.snapshotParentCalls)
	}
	manager.snapshotParentCalls = nil
	chainTestUsage(t, ctx, c)
	if len(manager.snapshotParentCalls) != 0 {
		t.Fatalf("second pass looked up %v, want none", manager.snapshotParentCalls)
	}

	if err := c.ReleaseSession(ctx, "transient"); err != nil {
		t.Fatal(err)
	}
	chainTestUsage(t, ctx, c)
	c.usageSnapshotChainsMu.Lock()
	defer c.usageSnapshotChainsMu.Unlock()
	memo := c.usageSnapshotChains
	for _, gone := range []string{"exec-2", "exec-1"} {
		if _, kept := memo.parents[gone]; kept {
			t.Fatalf("link for %s outlived every result using it", gone)
		}
		if _, kept := memo.chains[gone]; kept {
			t.Fatalf("chain for %s outlived every result using it", gone)
		}
	}
	if len(memo.parents) != 2 || len(memo.chains) != 1 {
		t.Fatalf("kept parents=%v chains=%v, want only the image chain", memo.parents, memo.chains)
	}
}

// A row that appears after sampling has no resolved parents yet. Treating its
// chain as ending early would let the simulation credit layers it retains, so
// the pass is deferred instead.
func TestCacheUsageDefersLateRowWithUnresolvedChain(t *testing.T) {
	ctx, c := chainTestCache(t)
	publishProgressValue(t, ctx, c, "test-session", "from", chainTestValue("image-top", 50), true)
	cacheTestReleaseSession(t, c, ctx)
	sample, err := c.collectUsageMeasurementInputs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer sample.close(ctx)

	// An imported, unmaterialized exec result published after sampling.
	late := publishProgressValue(t, ctx, c, "holder", "late", NewInt(77), false).cacheSharedResult()
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	late.payloadMu.Lock()
	late.self = nil
	late.hasValue = false
	late.snapshotOwnerLinks = []PersistedSnapshotRefLink{{RefKey: "exec-1", Role: "snapshot"}}
	late.payloadRevision++
	late.payloadMu.Unlock()

	if _, err := c.pruneUsageIdentitiesLocked(sample); !errors.Is(err, errCacheUsageChanged) {
		t.Fatalf("err=%v, want the pass deferred", err)
	}
}

type failingParentManager struct {
	*fakeSnapshotManager
	fail map[string]bool
}

func (m *failingParentManager) SnapshotParent(ctx context.Context, id string) (string, error) {
	if m.fail[id] {
		return "", errors.New("injected metadata read failure")
	}
	return m.fakeSnapshotManager.SnapshotParent(ctx, id)
}

// A failed parent lookup leaves the chain incomplete: the pass is deferred
// rather than crediting layers the row may retain, and the next pass retries.
func TestCachePruneDefersOnParentLookupFailure(t *testing.T) {
	ctx, c := chainTestCache(t)
	manager := &failingParentManager{fakeSnapshotManager: chainTestManager(), fail: map[string]bool{"exec-1": true}}
	c.snapshotManager = manager
	publishProgressValue(t, ctx, c, "test-session", "from", chainTestValue("image-top", 50), true)
	publishProgressValue(t, ctx, c, "holder", "exec1", chainTestValue("exec-1", 10), false)
	cacheTestReleaseSession(t, c, ctx)

	// The image is the only candidate, but the live exec retains its layers.
	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true, MaxUsedSpace: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 0 || report.ReclaimedBytes != 0 {
		t.Fatalf("pruned %d entries crediting %d bytes during a failed lookup, want the pass deferred", len(report.Entries), report.ReclaimedBytes)
	}

	manager.fail = nil
	report, err = c.Prune(ctx, []CachePrunePolicy{{All: true, MaxUsedSpace: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 || report.ReclaimedBytes != 0 {
		t.Fatalf("pruned %d entries crediting %d bytes, want the image pruned with no credit while the exec retains it", len(report.Entries), report.ReclaimedBytes)
	}
}

// Measuring holds E and is most of a pass's cost. Later policies reuse the
// measured state until a policy removes entries.
func TestCachePruneMeasuresOncePerPassUntilRemoval(t *testing.T) {
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
	var identityCalls int
	live := &usageCallbackValue{Int: NewInt(1), callback: func(kind string) {
		if kind == "identities" {
			identityCalls++
		}
	}}
	publishProgressValue(t, ctx, c, "holder", "live", live, false)
	private := cacheTestSizedInt{Int: NewInt(2), usageIdentities: []string{"private"}, sizeByIdentity: map[string]int64{"private": 100}}
	publishProgressValue(t, ctx, c, "test-session", "private", private, true)
	cacheTestReleaseSession(t, c, ctx)

	noTarget := CachePrunePolicy{All: true, MaxUsedSpace: 1 << 40}
	identityCalls = 0
	if _, err := c.Prune(ctx, []CachePrunePolicy{noTarget, noTarget, noTarget}); err != nil {
		t.Fatal(err)
	}
	if identityCalls != 2 {
		t.Fatalf("three policies that remove nothing sampled identities %d times, want one measurement (2)", identityCalls)
	}

	identityCalls = 0
	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true, MaxUsedSpace: 1}, noTarget, noTarget})
	if err != nil {
		t.Fatal(err)
	}
	if len(report.Entries) != 1 {
		t.Fatalf("pruned %d entries, want the persisted private entry", len(report.Entries))
	}
	if identityCalls != 4 {
		t.Fatalf("sampled identities %d times, want one measurement before the removal and one after (4)", identityCalls)
	}
}

// Once a pass publishes its chains, later passes read them without a lock. A
// row only finalization sees must expand without writing to them.
func TestCacheUsageFinalizeLeavesPublishedChainsUnchanged(t *testing.T) {
	ctx, c := chainTestCache(t)
	publishProgressValue(t, ctx, c, "test-session", "exec2", chainTestValue("exec-2", 20), true)
	cacheTestReleaseSession(t, c, ctx)
	sample, err := c.collectUsageMeasurementInputs(ctx, false)
	if err != nil {
		t.Fatal(err)
	}
	defer sample.close(ctx)
	reader := c.newSnapshotChains()
	if _, cached := reader.knownChains["image-top"]; cached {
		t.Fatal("image-top chain cached before any row used it")
	}

	late := publishProgressValue(t, ctx, c, "holder", "late", NewInt(77), false).cacheSharedResult()
	c.egraphMu.Lock()
	late.payloadMu.Lock()
	late.self = nil
	late.hasValue = false
	late.snapshotOwnerLinks = []PersistedSnapshotRefLink{{RefKey: "image-top", Role: "snapshot"}}
	late.payloadRevision++
	late.payloadMu.Unlock()

	// Run a later pass's read concurrently, for the race detector.
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader.chain("exec-2")
	}()
	_, err = sample.finalizeLocked()
	c.egraphMu.Unlock()
	<-done
	if err != nil {
		t.Fatal(err)
	}
	if chain, cached := reader.knownChains["image-top"]; cached {
		t.Fatalf("finalization wrote image-top=%v into the published chains", chain)
	}
}

// A later policy reuses the measurement only to decide it has nothing to do.
// Before planning removals it measures again, so a result published since
// then still protects the layers it retains.
func TestCachePruneRemeasuresBeforePlanningLaterPolicy(t *testing.T) {
	ctx, c := chainTestCache(t)
	publishProgressValue(t, ctx, c, "test-session", "from", chainTestValue("image-top", 50), true)
	cacheTestReleaseSession(t, c, ctx)
	c.testBeforePrunePolicy = func(policyIdx int) {
		if policyIdx == 1 {
			publishProgressValue(t, ctx, c, "holder", "exec1", chainTestValue("exec-1", 10), false)
		}
	}

	report, err := c.Prune(ctx, []CachePrunePolicy{{All: true, MaxUsedSpace: 1000}, {All: true, MaxUsedSpace: 1}})
	if err != nil {
		t.Fatal(err)
	}
	if report.ReclaimedBytes != 0 {
		t.Fatalf("credited %d bytes the new active exec still retains", report.ReclaimedBytes)
	}
}
