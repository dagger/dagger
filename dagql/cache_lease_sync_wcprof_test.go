package dagql

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// A lease sync blocked behind another sync of the same result records the
// time blocked as a lock wait of its op, not as its own time.
func TestSyncResultSnapshotLeasesRecordsLockWait(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	srv.InstallObject(NewClass(srv, ClassOpts[*shareTestValue]{}))
	res := persistedListTestResult(t, ctx, c, srv, "lease-sync-wait", newShareTestValue("v", map[string]sharePartState{"fs": {Snapshot: "fs-snap"}}))
	row := res.cacheSharedResult()

	wcprof.EnsureRecorder()
	profCtx, op := wcprof.BeginOp(wcprof.ContextWithProfiling(ctx), wcprof.OpKindLazy, "test.sync", wcprof.OpOpts{})
	require.NotNil(t, op)

	// What a concurrent sync of the same result holds.
	row.leaseSyncMu.Lock()
	done := make(chan error, 1)
	go func() { done <- c.SyncResultSnapshotOwnerLeases(profCtx, res) }()
	require.Eventually(t, func() bool {
		buf := make([]byte, 256<<10)
		for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, "[sync.Mutex.Lock") && strings.Contains(stack, "syncResultSnapshotLeases(") {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond)
	row.leaseSyncMu.Unlock()
	require.NoError(t, <-done)
	op.End(wcprof.OutcomeOK)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	var waits []string
	for _, ev := range events {
		if ev.Type == "wait" && ev.ParentID == op.ID() {
			waits = append(waits, ev.Reason+" "+header.Strings[ev.IdentID])
		}
	}
	require.Equal(t, []string{"lock dagql.sharedResult.leaseSyncMu"}, waits)
}
