package core

import (
	"bytes"
	"context"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// profiledOp starts a wcprof op on a context marked for per-session
// profiling, so only this test's work is recorded.
func profiledOp(t *testing.T, class string) (context.Context, *wcprof.Op) {
	t.Helper()
	wcprof.EnsureRecorder()
	ctx, op := wcprof.BeginOp(wcprof.ContextWithProfiling(t.Context()), wcprof.OpKindLazy, class, wcprof.OpOpts{})
	require.NotNil(t, op)
	return ctx, op
}

// lockWaits returns the recorded lock waits of op, by resource name.
func lockWaits(t *testing.T, op *wcprof.Op) map[string]int {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	waits := map[string]int{}
	for _, ev := range events {
		if ev.Type == "wait" && ev.ParentID == op.ID() && ev.Reason == wcprof.WaitReasonLock.String() {
			waits[header.Strings[ev.IdentID]]++
		}
	}
	return waits
}

func awaitBlockedIn(t *testing.T, frame string) {
	t.Helper()
	require.Eventually(t, func() bool {
		buf := make([]byte, 256<<10)
		for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, "[sync.Mutex.Lock") && strings.Contains(stack, frame) {
				return true
			}
		}
		return false
	}, 5*time.Second, time.Millisecond)
}

// Work blocked behind another runner's group body is recorded as a lock wait
// of the blocked op, not left as its own time.
func TestLazyGroupBodyWaitsRecordedAsLockWaits(t *testing.T) {
	op := &ContainerFromImageRefLazy{LazyState: NewLazyState()}
	ctr := NewContainer(Platform{})
	ctr.Lazy = op

	entered, allow := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(allow) })
	defer release()
	bodyDone := make(chan error, 1)
	go func() {
		bodyDone <- op.EvaluateGroup(t.Context(), "fs", ContainerLazyGroupWrite, func(context.Context) error {
			close(entered)
			<-allow
			ctr.FS.setValue(containerPersistenceTestDirectory("owned", "/"))
			return nil
		})
	}()
	<-entered

	// A second runner of the same group.
	runnerCtx, runnerOp := profiledOp(t, "test.runner")
	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- op.EvaluateGroup(runnerCtx, "fs", ContainerLazyGroupWrite, func(context.Context) error { return nil })
	}()
	awaitBlockedIn(t, "EvaluateGroup(")

	// An ownership reader waiting for the running body.
	readerCtx, readerOp := profiledOp(t, "test.reader")
	readerDone := make(chan error, 1)
	go func() {
		_, _, err := ctr.ReadSnapshotOwner(readerCtx)
		readerDone <- err
	}()
	awaitBlockedIn(t, "lockSnapshotOwnerRead(")

	release()
	require.NoError(t, <-bodyDone)
	require.NoError(t, <-runnerDone)
	require.NoError(t, <-readerDone)
	runnerOp.End(wcprof.OutcomeOK)
	readerOp.End(wcprof.OutcomeOK)

	// The reader can wait again behind the runner's no-op turn of the body.
	for _, op := range []*wcprof.Op{runnerOp, readerOp} {
		waits := lockWaits(t, op)
		require.Len(t, waits, 1, "waits: %v", waits)
		require.GreaterOrEqual(t, waits[lazyGroupMuProfIdent], 1, "waits: %v", waits)
	}
}
