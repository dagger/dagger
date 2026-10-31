package core

import (
	"bytes"
	"context"
	"errors"
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

// When the running body is profiled, a runner or reader blocked behind it is
// recorded as joining that body's op, so a profile charges the wait to the
// body's work rather than to the waiter's own time.
func TestLazyGroupBodyWaitsJoinTheRunningBody(t *testing.T) {
	op := &ContainerFromImageRefLazy{LazyState: NewLazyState()}
	ctr := NewContainer(Platform{})
	ctr.Lazy = op

	bodyCtx, bodyOp := profiledOp(t, "test.body")
	entered, allow := make(chan struct{}), make(chan struct{})
	release := sync.OnceFunc(func() { close(allow) })
	defer release()
	bodyDone := make(chan error, 1)
	go func() {
		bodyDone <- op.EvaluateGroup(bodyCtx, "fs", ContainerLazyGroupWrite, func(context.Context) error {
			close(entered)
			<-allow
			ctr.FS.setValue(containerPersistenceTestDirectory("owned", "/"))
			return nil
		})
	}()
	<-entered

	runnerCtx, runnerOp := profiledOp(t, "test.runner")
	runnerDone := make(chan error, 1)
	go func() {
		runnerDone <- op.EvaluateGroup(runnerCtx, "fs", ContainerLazyGroupWrite, func(context.Context) error { return nil })
	}()
	awaitBlockedIn(t, "EvaluateGroup(")

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
	bodyOp.End(wcprof.OutcomeOK)
	runnerOp.End(wcprof.OutcomeOK)
	readerOp.End(wcprof.OutcomeOK)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	_, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	for _, waiter := range []*wcprof.Op{runnerOp, readerOp} {
		var joins int
		for _, ev := range events {
			if ev.Type == "wait" && ev.ParentID == waiter.ID() && ev.TargetID == bodyOp.ID() {
				require.Equal(t, wcprof.WaitReasonLazy.String(), ev.Reason)
				joins++
			}
		}
		require.Equal(t, 1, joins, "op %d's waits on the running body", waiter.ID())
	}
}

// A wait that outlasts the body it began behind is a lock wait, not a join:
// when that body fails and a retry's body takes the group before the waiter
// does, the waiter waited for both, and a join on the first would leave the
// retry's work out of the wait.
func TestLazyGroupWaitAcrossTwoBodiesIsALockWait(t *testing.T) {
	op := &ContainerFromImageRefLazy{LazyState: NewLazyState()}
	ctr := NewContainer(Platform{})
	ctr.Lazy = op

	failCtx, failOp := profiledOp(t, "test.failing")
	entered, allowFail := make(chan struct{}), make(chan struct{})
	releaseFail := sync.OnceFunc(func() { close(allowFail) })
	defer releaseFail()
	failDone := make(chan error, 1)
	go func() {
		failDone <- op.EvaluateGroup(failCtx, "fs", ContainerLazyGroupWrite, func(context.Context) error {
			close(entered)
			<-allowFail
			return errors.New("body failed")
		})
	}()
	<-entered

	// The retry queues first, then the reader; past a millisecond the mutex
	// hands itself to its waiters in order, so the retry's body runs next.
	retryCtx, retryOp := profiledOp(t, "test.retry")
	retrying, allowRetry := make(chan struct{}), make(chan struct{})
	releaseRetry := sync.OnceFunc(func() { close(allowRetry) })
	defer releaseRetry()
	retryDone := make(chan error, 1)
	go func() {
		retryDone <- op.EvaluateGroup(retryCtx, "fs", ContainerLazyGroupWrite, func(context.Context) error {
			close(retrying)
			<-allowRetry
			ctr.FS.setValue(containerPersistenceTestDirectory("owned", "/"))
			return nil
		})
	}()
	awaitBlockedIn(t, "EvaluateGroup(")
	readerCtx, readerOp := profiledOp(t, "test.reader")
	readerDone := make(chan error, 1)
	go func() {
		_, _, err := ctr.ReadSnapshotOwner(readerCtx)
		readerDone <- err
	}()
	awaitBlockedIn(t, "lockSnapshotOwnerRead(")
	time.Sleep(10 * time.Millisecond)

	releaseFail()
	require.Error(t, <-failDone)
	<-retrying
	releaseRetry()
	require.NoError(t, <-retryDone)
	require.NoError(t, <-readerDone)
	failOp.End(wcprof.OutcomeOK)
	retryOp.End(wcprof.OutcomeOK)
	readerOp.End(wcprof.OutcomeOK)

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	var retryJoins, readerLockWaits int
	for _, ev := range events {
		if ev.Type != "wait" {
			continue
		}
		switch ev.ParentID {
		case retryOp.ID():
			if ev.TargetID == failOp.ID() {
				retryJoins++
			}
		case readerOp.ID():
			require.NotEqual(t, failOp.ID(), ev.TargetID, "the reader's wait joins the failed body")
			if ev.Reason == wcprof.WaitReasonLock.String() && header.Strings[ev.IdentID] == lazyGroupMuProfIdent {
				readerLockWaits++
			}
		}
	}
	require.Equal(t, 1, retryJoins, "the retry waited for the failed body alone")
	require.Equal(t, 1, readerLockWaits, "the reader's wait across both bodies")
}
