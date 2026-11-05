package dagql

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

// partReuseTestReceiver returns an imported receiver whose snapshot part was
// installed from a local donor and settled, and an evaluation of it by ID in
// the test session, as a step of a later build reads it.
func partReuseTestReceiver(t *testing.T, setup ...func(*Cache)) (context.Context, *Cache, *Server, AnyResult, func() error) {
	t.Helper()
	ctx, c, srv, _ := shareTestCache(t)
	for _, f := range setup {
		f(c)
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	t.Cleanup(cancel)
	c.EnableTransferFixtureParts()
	srv.InstallObject(NewClass(srv, ClassOpts[*fixtureDemandTestValue]{}))
	barrier := newSharePassBarrier(c)
	held := make(chan struct{})
	release := sync.OnceFunc(func() { close(held) })
	barrier.hold.Store(&held)
	t.Cleanup(release)
	donor := persistedListTestResult(t, ctx, c, srv, "donor", &fixtureDemandTestValue{shareTestValue: newShareTestValue("donor", map[string]sharePartState{"snapshot": {Snapshot: "local-snapshot"}})})
	receiver := persistedListTestResult(t, ctx, c, srv, "receiver", &fixtureDemandTestValue{shareTestValue: newShareTestValue("receiver", map[string]sharePartState{"snapshot": {}})})
	shareTestEncodedReceiver(t, ctx, c, receiver)
	receiverID, err := receiver.ID()
	require.NoError(t, err)
	shareTestUnite(t, ctx, c, "part-reuse", donor, receiver)
	require.Equal(t, 1, barrier.awaitEntered(t))
	evaluate := func() error {
		return c.EvaluateTransferFixtureRoots(ctx, "test-session", srv, []*call.ID{receiverID})
	}
	require.NoError(t, evaluate())
	release()
	require.Equal(t, 1, barrier.awaitPass(t))
	require.True(t, shareTestHasLink(receiver, "local-snapshot"))
	return ctx, c, srv, receiver, evaluate
}

// partReuseTestTasks counts the acquisition tasks that have finished for
// res: each records its row's owner lease sync.
func partReuseTestTasks(t *testing.T, ctx context.Context, c *Cache, res AnyResult) int {
	t.Helper()
	report, err := c.TransferFixtureSnapshot(ctx, "test-session", nil)
	require.NoError(t, err)
	n := 0
	for _, event := range report.Parts {
		if event.ResultID == uint64(res.cacheSharedResult().id) && event.Kind == "owner-sync" {
			n++
		}
	}
	return n
}

// testMovePayload moves res's payload revision, as a change to its payload
// made by another goroutine would.
func testMovePayload(res *sharedResult) {
	res.payloadMu.Lock()
	res.payloadRevision++
	res.payloadMu.Unlock()
}

// An acquired part already complete and opened is read again without
// another acquisition, as a local result is read without evaluating it
// again.
func TestAcquiredPartReuseRunsNoTask(t *testing.T) {
	t.Parallel()
	ctx, c, _, receiver, evaluate := partReuseTestReceiver(t)
	// The first read after the installation settled finds the part complete
	// and opens it.
	require.NoError(t, evaluate())
	before := partReuseTestTasks(t, ctx, c, receiver)
	require.NoError(t, evaluate())
	require.NoError(t, evaluate())
	require.Equal(t, before, partReuseTestTasks(t, ctx, c, receiver), "a reused part runs no acquisition task")
}

// Anything that changes the row after the part was opened makes the next
// read acquire it again, and the read after that reuse it.
func TestAcquiredPartReuseEndsWhenTheRowChanges(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*sharedResult)
	}{
		{"stored payload", testMovePayload},
		{"offers", func(row *sharedResult) { row.transferRevision++ }},
		{"dependencies", func(row *sharedResult) { row.dependencyOwnershipRevision++ }},
		{"value replacement", func(row *sharedResult) { row.replacements++ }},
		{"gate", func(row *sharedResult) {
			gate := row.partGate.gate.Load()
			gate.mu.Lock()
			gate.revision++
			gate.mu.Unlock()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ctx, c, _, receiver, evaluate := partReuseTestReceiver(t)
			require.NoError(t, evaluate())
			before := partReuseTestTasks(t, ctx, c, receiver)
			c.egraphMu.Lock()
			tc.change(receiver.cacheSharedResult())
			c.egraphMu.Unlock()
			require.NoError(t, evaluate())
			after := partReuseTestTasks(t, ctx, c, receiver)
			require.Greater(t, after, before, "a changed row is acquired again")
			require.NoError(t, evaluate())
			require.Equal(t, after, partReuseTestTasks(t, ctx, c, receiver), "and then reused")
		})
	}
}

// The acquisition that installs a part moves its row, by settling the
// installation and syncing the row's owner leases over the new snapshot.
// The part is still reused by the next read, not acquired a second time.
func TestAcquiredPartReuseAfterItsOwnTaskMovesTheRow(t *testing.T) {
	t.Parallel()
	var target atomic.Pointer[sharedResult]
	var moved atomic.Bool
	ctx, c, _, receiver, evaluate := partReuseTestReceiver(t, func(c *Cache) {
		c.testAfterLazyEvalFinish = func(attempt *lazyEvalAttempt) {
			row := target.Load()
			if row == nil || attempt.token == nil || attempt.token.row != row ||
				!strings.HasPrefix(string(attempt.token.key), "acquire:") {
				return
			}
			if moved.CompareAndSwap(false, true) {
				testMovePayload(row)
			}
		}
	})
	row := receiver.cacheSharedResult()
	// The row changes, so the next read acquires the part again; that
	// acquisition's own task moves the row before it finishes.
	testMovePayload(row)
	target.Store(row)
	require.NoError(t, evaluate())
	require.True(t, moved.Load(), "the acquisition ran its task")
	before := partReuseTestTasks(t, ctx, c, receiver)
	require.NoError(t, evaluate())
	require.Equal(t, before, partReuseTestTasks(t, ctx, c, receiver), "the next read reuses the part")
}

// A reused part is still refused to a session that doesn't satisfy its
// row's resource requirements, which can grow without the row changing
// otherwise.
func TestAcquiredPartReuseChecksSessionRequirements(t *testing.T) {
	t.Parallel()
	ctx, c, srv, receiver, evaluate := partReuseTestReceiver(t)
	require.NoError(t, evaluate())
	leaf := persistedListTestResult(t, ctx, c, srv, "leaf", String("socket"))
	c.egraphMu.Lock()
	leaf.cacheSharedResult().sessionResourceHandle = "socket"
	_, err := c.recomputeRequiredSessionResourcesLocked(leaf.cacheSharedResult())
	receiver.cacheSharedResult().requiredSessionResources = leaf.cacheSharedResult().requiredSessionResources
	c.egraphMu.Unlock()
	require.NoError(t, err)
	require.Error(t, evaluate())
}

// A record is never dated later than the state it saw, and a part being
// written or a running evaluation is never reused.
func TestAcquiredPartReuseConditions(t *testing.T) {
	t.Parallel()
	_, c, _, receiver := newLazyRetryTestResult(t, nil)
	row := receiver.cacheSharedResult()
	key, err := partAddressKey(PersistedPartAddress{Part: "snapshot"})
	require.NoError(t, err)
	reusable := func() bool {
		c.egraphMu.RLock()
		defer c.egraphMu.RUnlock()
		return partReusableLocked(row, []string{key})
	}

	stale := c.capturePartReuse(row)
	testMovePayload(row)
	c.recordPartReuse(row, key, stale)
	require.False(t, reusable(), "a record of a state that has since moved isn't written")

	c.recordPartReuse(row, key, c.capturePartReuse(row))
	require.True(t, reusable())

	gate := row.partGate.gate.Load()
	gate.mu.Lock()
	gate.outputs[key] = partOutputState{phase: PartOutputInstalled}
	gate.mu.Unlock()
	require.False(t, reusable(), "an installed part that hasn't settled is joined, not reused")
	gate.mu.Lock()
	delete(gate.outputs, key)
	gate.groups["whole"] = &partLazyEvaluationState{phase: LazyEvaluationRunning}
	gate.mu.Unlock()
	require.False(t, reusable(), "nothing is reused while an evaluation of the row runs")
	gate.mu.Lock()
	delete(gate.groups, "whole")
	gate.writers[&writerTicket{done: make(chan struct{})}] = &PartPermit{address: PersistedPartAddress{Part: "snapshot"}}
	gate.mu.Unlock()
	require.False(t, reusable(), "a part with a writer isn't reused")
}
