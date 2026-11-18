package snapshots

import (
	"bytes"
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/wcprof"
	digest "github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// Removing a lease takes it off every snapshot it owns, and leaves the
// snapshots' other owners, and the snapshots it doesn't own, as they were.
func TestRemoveLeaseOwners(t *testing.T) {
	ctx := context.Background()
	cm := newApplySnapshotDiffTestManager(t)
	require.Empty(t, cm.addSnapshotOwner(ctx, "s1", "a", nil))
	require.Empty(t, cm.addSnapshotOwner(ctx, "s2", "a", nil))
	require.Empty(t, cm.addSnapshotOwner(ctx, "s1", "b", nil))
	require.Empty(t, cm.addSnapshotOwner(ctx, "s3", "b", nil))

	require.NoError(t, cm.RemoveLease(ctx, "a"))
	require.Equal(t, map[string]map[string]struct{}{
		"s1": {"b": {}},
		"s3": {"b": {}},
	}, cm.snapshotOwnerLeases)

	require.NoError(t, cm.RemoveLease(ctx, "b"))
	require.Empty(t, cm.snapshotOwnerLeases)

	// A lease that owned something before owns it again once added back.
	require.Empty(t, cm.addSnapshotOwner(ctx, "s2", "a", nil))
	require.NoError(t, cm.RemoveLease(ctx, "a"))
	require.Empty(t, cm.snapshotOwnerLeases)
}

// Engines remove owner leases in bulk, as when many results are released at
// once, on engines with tens of thousands of owned snapshots. A removal's
// cost should not grow with the other snapshots' owners.
func BenchmarkRemoveLeaseAmongOwnedSnapshots(b *testing.B) {
	for _, snapshots := range []int{10_000, 100_000} {
		b.Run(fmt.Sprintf("snapshots=%d", snapshots), func(b *testing.B) {
			ctx := context.Background()
			cm := newApplySnapshotDiffTestManager(b)
			for i := range snapshots {
				cm.addSnapshotOwner(ctx, fmt.Sprintf("snapshot-%d", i), fmt.Sprintf("owner-%d", i), nil)
			}
			b.ResetTimer()
			for i := range b.N {
				pin := fmt.Sprintf("pin-%d", i)
				cm.addSnapshotOwner(ctx, fmt.Sprintf("snapshot-%d", i%snapshots), pin, nil)
				if err := cm.RemoveLease(ctx, pin); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// Where AttachLease and RemoveLease take the manager's lock, an acquisition
// another holder blocks is a lock wait of the current op on it, when
// profiled.
func TestOwnerLeaseManagerLockWaitsProfiled(t *testing.T) {
	cm := newApplySnapshotDiffTestManager(t)
	wcprof.EnsureRecorder()
	for name, call := range map[string]func(context.Context){
		"snapshotBlobs":    func(ctx context.Context) { cm.snapshotBlobs(ctx, []string{"s1"}, map[string]digest.Digest{}) },
		"addSnapshotOwner": func(ctx context.Context) { cm.addSnapshotOwner(ctx, "s1", "owner", nil) },
		"RemoveLease":      func(ctx context.Context) { require.NoError(t, cm.RemoveLease(ctx, "owner")) },
	} {
		t.Run(name, func(t *testing.T) {
			ctx, root := wcprof.BeginOp(wcprof.ContextWithProfiling(context.Background()), wcprof.OpKindInternal, "test."+name, wcprof.OpOpts{})
			cm.mu.Lock()
			locked := true
			unlock := func() {
				if locked {
					locked = false
					cm.mu.Unlock()
				}
			}
			defer unlock()
			var wg sync.WaitGroup
			wg.Go(func() { call(ctx) })
			deadline := time.Now().Add(10 * time.Second)
			for !blockedOnManagerLock(name) {
				if time.Now().After(deadline) {
					unlock()
					wg.Wait()
					t.Fatalf("%s never blocked on the manager's lock", name)
				}
				runtime.Gosched()
			}
			unlock()
			wg.Wait()
			root.End(wcprof.OutcomeOK)

			var buf bytes.Buffer
			require.NoError(t, wcprof.Active().WriteDump(&buf, false))
			header, events, err := wcprof.ReadDump(&buf)
			require.NoError(t, err)
			var waits int
			for _, ev := range events {
				if ev.Type == "wait" && ev.ParentID == root.ID() {
					require.Equal(t, "lock", ev.Reason)
					require.Equal(t, "snapshots.snapshotManager.mu", header.Strings[ev.IdentID])
					require.Greater(t, ev.EndNS, ev.StartNS)
					waits++
				}
			}
			require.Equal(t, 1, waits)
		})
	}
}

// blockedOnManagerLock reports whether a goroutine is blocked acquiring the
// manager's lock in wcprof.Lock, called from the manager's method fn.
func blockedOnManagerLock(fn string) bool {
	buf := make([]byte, 1<<20)
	for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
		if strings.Contains(stack, "[sync.Mutex.Lock") && strings.Contains(stack, "wcprof.Lock(") && strings.Contains(stack, "(*snapshotManager)."+fn+"(") {
			return true
		}
	}
	return false
}
