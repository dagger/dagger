package dagql

// What a span's end waits for as it reads its entry's state: a call span's
// (CacheDecision.ResultState) and a lazy evaluation's span's
// (lazySpanCacheState). Each runs on the call's own path.

import (
	"context"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// spanStateBesideAReader runs read while another goroutine holds the cache's
// graph lock for reading, as a lookup or a save's snapshot does. It fails the
// test when read returns only once the reader lets go, and returns what read
// returned.
func spanStateBesideAReader[T any](t *testing.T, c *Cache, read func() T) T {
	t.Helper()
	c.egraphMu.RLock()
	got := make(chan T, 1)
	go func() { got <- read() }()
	select {
	case v := <-got:
		c.egraphMu.RUnlock()
		return v
	case <-time.After(10 * time.Second):
		c.egraphMu.RUnlock()
		t.Error("the span's read waited for the graph lock's reader to let go")
		return <-got
	}
}

// spanStateExecuted makes, on an engine with the remote cache, an executed
// call whose result depends on a leaf, is retained, expires, and has one
// complete part of 300 bytes whose description and size nothing has read
// yet: the first span to end reads them from the entry's record.
func spanStateExecuted(t *testing.T) (context.Context, *Cache, *CallRequest, AnyResult) {
	t.Helper()
	mgr := &partSizeTestManager{fakeSnapshotManager: &fakeSnapshotManager{}, usage: map[string]int64{"top": 300}}
	ctx, c, srv, _ := partSizeTestCache(t, mgr, true)
	leafFrame := cacheTestIntCall("span-state-leaf")
	leaf, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: leafFrame}, ValueFunc(cacheTestIntResult(leafFrame, 1)))
	assert.NilError(t, err)

	value := &statePartValue{snapshotID: "top"}
	value.done.Store(true)
	frame := statePartFrame("span-state-root")
	frame.Receiver = &ResultCallRef{ResultID: uint64(leaf.cacheSharedResult().id)}
	req := cacheTestArmedRequest(frame)
	req.IsPersistable = true
	req.TTL = 3600
	res, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, frame)
	})
	assert.NilError(t, err)
	assert.Equal(t, CacheOutcomeExecuted, req.CacheEvidence.Outcome)
	assert.Assert(t, !completePartsCurrent(res.cacheSharedResult(), c.completePartSizeReader()), "nothing has read the entry's parts yet")
	return ctx, c, req, res
}

// A span's end reads its entry's parts, from the entry's record when nothing
// has read them yet, while another goroutine holds the graph lock for
// reading: it doesn't take the lock for writing to hold the entry, which
// would wait for every reader to leave, since the span's caller already
// holds the entry. A lazy evaluation's span reads them the same way.
func TestSpanEndReadsPartsBesideAReader(t *testing.T) {
	t.Parallel()
	snapshotKey, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	assert.NilError(t, err)

	ctx, c, req, res := spanStateExecuted(t)
	state := spanStateBesideAReader(t, c, func() CacheResultState {
		state, ok := req.CacheEvidence.ResultState(ctx, res)
		assert.Check(t, ok)
		return state
	})
	assert.DeepEqual(t, []string{snapshotKey}, state.Parts)
	assert.DeepEqual(t, []int64{300}, state.PartSizes)

	ctx, c, _, res = spanStateExecuted(t)
	lazy := spanStateBesideAReader(t, c, func() lazySpanCacheState {
		return c.lazySpanCacheState(ctx, res.cacheSharedResult(), nil)
	})
	assert.DeepEqual(t, []string{snapshotKey}, lazy.parts)
	assert.DeepEqual(t, []int64{300}, lazy.partSizes)
}

// A span's read holds its entry only through the ownership something else
// has of it. When the last other owner, here the call's session, lets go
// while the read captures the entry's record, the entry stays registered
// until the capture ends, and the read's release then collects it, as any
// release that leaves an entry unowned does. The span, ending once the
// entry has left the cache, reports no state for it.
func TestSpanEndReadCollectsAnEntryItsCallerLetGo(t *testing.T) {
	t.Parallel()
	mgr := &partSizeTestManager{fakeSnapshotManager: &fakeSnapshotManager{}, usage: map[string]int64{"top": 300}}
	ctx, c, srv, _ := partSizeTestCache(t, mgr, true)
	value := &statePartValue{snapshotID: "top"}
	value.done.Store(true)
	frame := statePartFrame("span-state-let-go")
	req := cacheTestArmedRequest(frame)
	res, err := c.GetOrInitCall(ctx, "span-state-let-go", srv, req, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, frame)
	})
	assert.NilError(t, err)
	row := res.cacheSharedResult()
	registered := func() bool {
		c.egraphMu.RLock()
		defer c.egraphMu.RUnlock()
		return c.resultsByID[row.id] == row
	}

	encoding, release := make(chan struct{}), make(chan struct{})
	hook := func() {
		close(encoding)
		<-release
	}
	value.encoding.Store(&hook)
	read := make(chan bool, 1)
	go func() {
		_, ok := req.CacheEvidence.ResultState(ctx, res)
		read <- ok
	}()
	<-encoding
	value.encoding.Store(nil)
	assert.NilError(t, c.ReleaseSession(ctx, "span-state-let-go"))
	assert.Assert(t, registered(), "the read's own ownership keeps the entry while it captures")
	close(release)
	assert.Assert(t, !<-read, "the entry left the cache before the span's state was read")
	assert.Assert(t, !registered(), "the read's release collected the entry nothing else owned")
}
