package dagql

import (
	"bytes"
	"cmp"
	"context"
	"maps"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/engine/wcprof"
)

// lockTakers records, for each exclusive take of c's graph lock while a
// recorder is active, the cache function that took it and the holder it
// marked; the returned function reads them, by the function's name after
// its receiver (holdTransferClosure, HoldEntry.func1).
func lockTakers(t *testing.T, c *Cache) func() map[string][]uint64 {
	t.Helper()
	wcprof.EnsureRecorder()
	var mu sync.Mutex
	takers := map[string][]uint64{}
	stop := c.egraphMu.ObserveMarks(func(holder uint64) {
		pcs := make([]uintptr, 32)
		frames := runtime.CallersFrames(pcs[:runtime.Callers(2, pcs)])
		for {
			f, more := frames.Next()
			if !strings.Contains(f.Function, "/engine/wcprof.") {
				name := f.Function
				if i := strings.Index(name, ")."); i >= 0 {
					name = name[i+2:]
				}
				mu.Lock()
				takers[name] = append(takers[name], holder)
				mu.Unlock()
				return
			}
			if !more {
				return
			}
		}
	})
	t.Cleanup(stop)
	return func() map[string][]uint64 {
		mu.Lock()
		defer mu.Unlock()
		return maps.Clone(takers)
	}
}

// requireTakesNamed checks that every take by a function whose name starts
// with one of names marked op, and that each of names took the lock.
func requireTakesNamed(t *testing.T, takers map[string][]uint64, op uint64, names ...string) {
	t.Helper()
	for _, want := range names {
		seen := false
		for name, holders := range takers {
			if !strings.HasPrefix(name, want) {
				continue
			}
			seen = true
			for _, h := range holders {
				require.Equal(t, op, h, "%s took the graph lock without naming its op", name)
			}
		}
		require.True(t, seen, "%s never took the graph lock (takers: %v)", want, takers)
	}
}

// TestExportCaptureNamesItsHolder: an export's capture takes the graph lock
// as its op, holding its closure, releasing it, and holding an entry by
// number, so a wait behind any of them names the export.
func TestExportCaptureNamesItsHolder(t *testing.T) {
	ctx, c := storedPartTestCache(t)
	root := storedPartTestPublish(t, ctx, c, "a", "capture-holder", &captureTestValue{text: "captured"})
	takers := lockTakers(t, c)
	opCtx, op := wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindRemoteCache, "test.export.capture", wcprof.OpOpts{})
	defer op.End(wcprof.OutcomeOK)

	exportTestBundle(t, opCtx, c, root)
	_, release, err := c.HoldEntry(opCtx, uint64(root.cacheSharedResult().id))
	require.NoError(t, err)
	require.NoError(t, release(opCtx))

	requireTakesNamed(t, takers(), op.ID(), "holdTransferClosure", "Release", "HoldEntry")
}

// TestExportCaptureRecordsItsLockWaitOnce: a capture that waits for the
// graph lock records that wait once, so the capture's waits on the lock
// never overlap: one waiter can't wait twice at once.
func TestExportCaptureRecordsItsLockWaitOnce(t *testing.T) {
	ctx, c := storedPartTestCache(t)
	root := storedPartTestPublish(t, ctx, c, "a", "capture-wait", &captureTestValue{text: "captured"})
	opCtx, op := profiledRoot(ctx, "test.export.capture")
	c.egraphMu.Lock()
	done := make(chan error, 1)
	go func() {
		done <- c.WithExportedValues(opCtx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(context.Context, *ExportedValues) error { return nil })
	}()
	time.Sleep(50 * time.Millisecond)
	c.egraphMu.Unlock()
	require.NoError(t, <-done)
	op.End(wcprof.OutcomeOK)

	events, _ := profiledUnder(t, op.ID())
	var waits []profiledEvent
	for _, ev := range events {
		if ev.Type == "wait" && ev.ParentID == op.ID() && ev.ident == egraphLockIdent {
			waits = append(waits, ev)
		}
	}
	require.NotEmpty(t, waits, "the capture waited for the lock")
	slices.SortFunc(waits, func(a, b profiledEvent) int { return cmp.Compare(a.StartNS, b.StartNS) })
	for i := 1; i < len(waits); i++ {
		require.GreaterOrEqual(t, waits[i].StartNS, waits[i-1].EndNS, "the capture's waits on the lock overlap: %+v", waits)
	}
}

// TestMakeResultUnpruneableNamesItsHolder: making a result unpruneable, as
// loading a module's type definitions does, takes the graph lock as its op.
func TestMakeResultUnpruneableNamesItsHolder(t *testing.T) {
	ctx, c := storedPartTestCache(t)
	root := storedPartTestPublish(t, ctx, c, "a", "unpruneable-holder", &captureTestValue{text: "kept"})
	takers := lockTakers(t, c)
	opCtx, op := wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindCall, "test.coremod", wcprof.OpOpts{})
	defer op.End(wcprof.OutcomeOK)
	require.NoError(t, c.MakeResultUnpruneable(opCtx, root))
	requireTakesNamed(t, takers(), op.ID(), "MakeResultUnpruneable")
}

// TestOfferNamesItsHolder: an offer takes the graph lock as the op it runs
// under, the remote-cache adapter's op for the offered item, in OfferParts
// and in each part's offer.
func TestOfferNamesItsHolder(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	r := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	takers := lockTakers(t, c)
	opCtx, op := wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindRemoteCache, "test.offer.item", wcprof.OpOpts{})
	defer op.End(wcprof.OutcomeOK)
	out, err := c.testOfferParts(opCtx, r, []PersistedPartOffer{testLiveOffer()})
	require.NoError(t, err)
	require.Equal(t, OfferAccepted, out[0].Outcome)
	requireTakesNamed(t, takers(), op.ID(), "OfferParts", "offerPart")
}

// TestSnapshotSharingNamesItsHolder: on an engine that records, the sharing
// worker takes the graph lock as a snapshot.share op of its own, in taking
// its job, selecting the job's slots and releasing its members, so a wait
// behind a share pass names it rather than an unknown holder. The worker
// runs on the cache's background context, which only engine-wide recording
// reaches, so the test records engine-wide and doesn't run in parallel.
func TestSnapshotSharingNamesItsHolder(t *testing.T) {
	rec := wcprof.NewRecorder(1 << 16)
	t.Cleanup(wcprof.SetActiveForTest(rec, true))
	ctx, c, srv, _ := shareTestCache(t)
	barrier := newSharePassBarrier(c)
	takers := lockTakers(t, c)
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &transferTestValue{Text: "snapshot", links: []PersistedSnapshotRefLink{{Role: "snapshot", RefKey: "donor-snapshot"}}})
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &transferTestValue{Text: "pending"})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	partTestEquivalent(t, c, receiver, donor)
	content := digest.FromString("share-holder")
	require.NoError(t, c.TeachContentDigest(ctx, donor, content))
	require.NoError(t, c.TeachContentDigest(ctx, receiver, content))
	require.Equal(t, 1, barrier.awaitPass(t), "one planned slot")

	// The job's op is in the dump once the worker has released the job.
	classOf := func(id uint64) string {
		var buf bytes.Buffer
		require.NoError(t, rec.WriteDump(&buf, false))
		header, events, err := wcprof.ReadDump(&buf)
		require.NoError(t, err)
		for _, ev := range events {
			if ev.Type == "op" && ev.OpID == id {
				return header.Strings[ev.ClassID]
			}
		}
		return ""
	}
	names := []string{"runSnapshotShareWorker", "selectShareSlots", "releaseSnapshotShareMembers"}
	require.Eventually(t, func() bool {
		got := takers()
		for _, want := range names {
			if len(got[want]) == 0 {
				return false
			}
		}
		return true
	}, 10*time.Second, 10*time.Millisecond, "the worker takes, selects and releases")
	got := takers()
	for _, name := range names {
		for _, h := range got[name] {
			require.NotEqual(t, wcprof.HolderUnknown, h, "%s took the graph lock as an unknown holder", name)
			require.NotZero(t, h, name)
			require.Eventually(t, func() bool { return classOf(h) == "snapshot.share" }, 10*time.Second, 10*time.Millisecond, "%s's holder %d is a snapshot.share op", name, h)
		}
	}
}
