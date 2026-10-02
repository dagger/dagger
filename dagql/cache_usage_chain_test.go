package dagql

import (
	"context"
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
	c.usageSnapshotParentsMu.Lock()
	defer c.usageSnapshotParentsMu.Unlock()
	for _, gone := range []string{"exec-2", "exec-1"} {
		if _, kept := c.usageSnapshotParents[gone]; kept {
			t.Fatalf("link for %s outlived every result using it", gone)
		}
	}
	if len(c.usageSnapshotParents) != 2 {
		t.Fatalf("kept links=%v, want only the image chain", c.usageSnapshotParents)
	}
}
