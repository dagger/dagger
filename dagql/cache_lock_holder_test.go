package dagql

import (
	"maps"
	"runtime"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

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
