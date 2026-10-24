package dagql

import (
	"context"
	"runtime"
	"sync/atomic"
	"testing"
	"time"
	"weak"

	"github.com/stretchr/testify/require"
)

// A lazy operation's cleanup releases what the operation built, such as the
// private object a no-source decision decoded, its recipe and its record.
// Once the cleanup has run, the row must not keep those alive: the row lives
// as long as the cache keeps it, so every operation it re-runs would
// otherwise stay in memory with it.
func TestPartLazyOperationCleanupReleasesItsInvocation(t *testing.T) {
	for _, tc := range []struct {
		name    string
		encoded bool
	}{{"encoded receiver", true}, {"typed receiver", false}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, c, srv, _ := shareTestCache(t)
			srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
			receiver := persistedListTestResult(t, ctx, c, srv, "cleanup-receiver", newShareTestValue("receiver", map[string]sharePartState{"fs": {}}))
			row := receiver.cacheSharedResult()
			if tc.encoded {
				shareTestEncodedReceiver(t, ctx, c, receiver)
			} else {
				c.egraphMu.Lock()
				row.imported = true
				c.egraphMu.Unlock()
			}
			evaluated := persistedListTestResult(t, ctx, c, srv, "cleanup-evaluated", newShareTestValue("evaluated", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}}))
			address := PersistedPartAddress{Part: "fs"}

			var releases atomic.Int32
			cleanup, invocation := heldInvocationCleanup(&releases)
			ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			err := c.RunLazyTask(ctx, receiver, "lazy:cleanup", LazyTaskSpec{Body: func(ctx context.Context) error {
				drain, _, err := c.PrepareOriginal(ctx, receiver, LazyGroupAddress{Group: LazyGroupWhole}, []PersistedPartAddress{address}, PartTaskFromContext(ctx))
				if err != nil {
					return err
				}
				if err = drain.Wait(ctx); err != nil {
					return err
				}
				scan, err := c.CheckPartSources(ctx, receiver, address, drain, &PartDemandState{})
				if err != nil {
					return err
				}
				original, _, err := c.BeginOriginal(ctx, scan.NoSource)
				if err != nil {
					return err
				}
				var version capturedRowRevision
				produced, err := c.capturePartRecord(ctx, evaluated.cacheSharedResult(), false, nil, &version)
				if err != nil {
					return err
				}
				return c.publishEvaluatedParts(ctx, receiver, address, produced, original, cleanup, nil)
			}})
			require.NoError(t, err)
			cleanup = nil
			_, _, probe, err := c.probePart(context.WithoutCancel(ctx), row, address)
			require.NoError(t, err)
			require.True(t, probe.LocalComplete, "the operation's part is installed")
			require.Eventually(t, func() bool { return releases.Load() == 1 }, 5*time.Second, 10*time.Millisecond, "the cleanup runs after the install")
			require.EqualValues(t, 1, releases.Load(), "the cleanup ran once")

			require.Eventually(t, func() bool {
				runtime.GC()
				return invocation.Value() == nil
			}, 5*time.Second, 10*time.Millisecond, "the installed row still holds the operation it re-ran")
			runtime.KeepAlive(row)
		})
	}
}

// heldInvocation stands for what a no-source decision's private operation
// keeps: a decoded object graph, here a 1 MiB buffer.
type heldInvocation struct{ buf []byte }

// heldInvocationCleanup returns a cleanup that releases a new invocation, and
// a weak pointer to the invocation: only the cleanup keeps it alive.
func heldInvocationCleanup(releases *atomic.Int32) (*partCleanup, weak.Pointer[heldInvocation]) {
	invocation := &heldInvocation{buf: make([]byte, 1<<20)}
	return &partCleanup{fn: func(context.Context) error {
		_ = invocation.buf
		releases.Add(1)
		return nil
	}}, weak.Make(invocation)
}
