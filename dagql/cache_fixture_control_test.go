package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/stretchr/testify/require"
)

func fixtureTestHandle(t *testing.T, c *Cache, res AnyResult) *call.ID {
	t.Helper()
	id, err := c.PersistedResultID(res)
	require.NoError(t, err)
	return call.NewEngineResultID(id, call.NewType(res.Type()))
}

// The closed action set: an unknown point or action and every illegal pair
// are refused before anything is armed; every legal pair is accepted.
func TestFixtureBarrierActionsAreClosed(t *testing.T) {
	_, c, _ := transferTestCache(t)
	_, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "k", Point: FixtureBeforeFinish, Action: FixturePause})
	require.ErrorContains(t, err, "not enabled", "off-gate nothing can be armed")
	c.EnableTransferFixtureParts()

	for _, point := range fixtureBarrierPoints {
		_, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "pause-" + string(point), Point: point, Action: FixturePause})
		require.NoError(t, err, "pause is legal at %s", point)
		for action, legal := range fixtureBarrierFaults {
			_, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: string(action) + "-" + string(point), Point: point, Action: action})
			if point == legal {
				require.NoError(t, err, "%s is legal at %s", action, point)
			} else {
				require.ErrorContains(t, err, "legal only at", "%s at %s", action, point)
			}
		}
	}
	armed := c.TransferFixtureBarrierCount()
	for _, bad := range []FixtureBarrierRequest{
		{Key: "", Point: FixtureBeforeFinish, Action: FixturePause},
		{Key: "k", Point: "afterEverything", Action: FixturePause},
		{Key: "k", Point: FixtureBeforeFinish, Action: "failAnything"},
		{Key: "k", Point: FixtureBeforeFinish, Action: ""},
	} {
		_, err := c.ArmTransferFixtureBarrier(bad)
		require.Error(t, err, "%+v", bad)
	}
	require.Equal(t, armed, c.TransferFixtureBarrierCount(), "a refused request arms nothing")
}

// A pause holds the real operation at its point, reports what it observed,
// and ends with its release. Waiting is idempotent; an old generation cannot
// be reopened; close releases whatever is still paused.
func TestFixtureBarrierPauseAndRelease(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	c.EnableTransferFixtureParts()
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	stale, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "finish", Point: FixtureBeforeFinish, Action: FixturePause})
	require.NoError(t, err)
	armed, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "finish", Point: FixtureBeforeFinish, Selector: FixtureBarrierSelector{ResultID: uint64(receiver.cacheSharedResult().id)}, Action: FixturePause})
	require.NoError(t, err)
	require.Greater(t, armed.Generation, stale.Generation)
	_, err = c.WaitTransferFixtureBarrier(ctx, "finish", stale.Generation)
	require.ErrorContains(t, err, "not armed", "re-arming a key retires its old generation")

	shareTestUnite(t, ctx, c, "fixture-pause", donor, receiver)
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	reached, err := c.WaitTransferFixtureBarrier(waitCtx, "finish", armed.Generation)
	require.NoError(t, err)
	require.Equal(t, FixtureBeforeFinish, reached.Event.Point)
	require.Equal(t, uint64(receiver.cacheSharedResult().id), reached.Event.ResultID)
	require.NotZero(t, reached.Event.PassID)
	require.False(t, reached.Released)
	shareTestNoPassYet(t, barrier, "while its Finish was paused at the fixture barrier")

	again, err := c.WaitTransferFixtureBarrier(waitCtx, "finish", armed.Generation)
	require.NoError(t, err)
	require.Equal(t, reached.Event, again.Event, "waiting again returns the same observation")
	require.NoError(t, c.ReleaseTransferFixtureBarrier("finish", armed.Generation))
	require.NoError(t, c.ReleaseTransferFixtureBarrier("finish", armed.Generation), "releasing twice is harmless")
	require.Equal(t, 1, barrier.awaitPass(t))
	require.True(t, shareTestHasLink(receiver, "fs-snap"), "the paused operation completed by itself")
	require.Zero(t, c.TransferFixtureBarrierCount())
}

func TestFixtureBarrierCloseReleasesPausedOperation(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	c.EnableTransferFixtureParts()
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	armed, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "prepared", Point: FixtureShareAllPrepared, Action: FixturePause})
	require.NoError(t, err)
	shareTestUnite(t, ctx, c, "fixture-close", donor, receiver)
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	_, err = c.WaitTransferFixtureBarrier(waitCtx, "prepared", armed.Generation)
	require.NoError(t, err)

	closed := make(chan error, 1)
	closeCtx, cancelClose := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancelClose()
	go func() { closed <- c.Close(closeCtx) }()
	select {
	case err := <-closed:
		require.NoError(t, err, "close released the paused pass and drained it")
	case <-time.After(15 * time.Second):
		t.Fatal("close never returned while a fixture barrier was paused")
	}
	_, err = c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "late", Point: FixtureBeforeFinish, Action: FixturePause})
	require.ErrorIs(t, err, ErrCacheClosed, "nothing is armed after close")
}

// The two owner-attach faults fail the real attachment path once: before the
// delegate, claiming no attachment, or after its real side effects. Either
// way the installed output is kept and a later demand retries only the
// bookkeeping.
func TestFixtureBarrierOwnerAttachFaults(t *testing.T) {
	for _, tc := range []struct {
		action   FixtureBarrierAction
		point    FixtureBarrierPoint
		attaches int32
	}{
		{FixtureFailOwnerAttachBefore, FixtureBeforeOwnerAttach, 0},
		{FixtureFailOwnerAttachAfter, FixtureAfterOwnerAttach, 1},
	} {
		t.Run(string(tc.action), func(t *testing.T) {
			ctx, c, srv, manager := shareTestCache(t)
			c.EnableTransferFixtureParts()
			barrier := newSharePassBarrier(c)
			donor, receiver := shareTestPair(t, ctx, c, srv,
				map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
				map[string]sharePartState{"fs": {}},
			)
			row := receiver.cacheSharedResult()
			attachesBefore := manager.attaches.Load()
			armed, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "attach", Point: tc.point, Selector: FixtureBarrierSelector{ResultID: uint64(row.id)}, Action: tc.action})
			require.NoError(t, err)
			shareTestUnite(t, ctx, c, "fixture-attach-"+string(tc.action), donor, receiver)
			require.Equal(t, 1, barrier.awaitPass(t))
			reached, err := c.WaitTransferFixtureBarrier(ctx, "attach", armed.Generation)
			require.NoError(t, err)
			require.Contains(t, reached.Event.Detail, "fs-snap")
			require.Equal(t, tc.attaches, manager.attaches.Load()-attachesBefore, "the delegate ran only if the fault follows it")

			key, _ := partAddressKey(PersistedPartAddress{Part: "fs"})
			gate := row.partGate.gate.Load()
			gate.mu.Lock()
			phase := gate.outputs[key].phase
			gate.mu.Unlock()
			require.Equal(t, PartOutputInstalled, phase, "the fault failed bookkeeping only; the output is not pending again")
			require.Zero(t, manager.released.Load(), "protection is retained for the retry")

			pins := manager.pins.Load()
			require.NoError(t, c.demandPart(ctx, receiver, PersistedPartAddress{Part: "fs"}))
			gate.mu.Lock()
			phase = gate.outputs[key].phase
			gate.mu.Unlock()
			require.Equal(t, PartComplete, phase, "the one-shot fault is spent; the retry settles the output")
			require.Equal(t, pins, manager.pins.Load(), "with no second preparation")
			require.Equal(t, int32(1), manager.released.Load())
		})
	}
}

// A hold token owns ordinary incoming holds: it keeps its rows registered
// when every other owner is gone, ends through the ordinary release path, is
// released once, and never outlives the cache.
func TestFixtureHoldTokens(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	held := persistedListTestResult(t, ctx, c, srv, "held", String("held"))
	row := held.cacheSharedResult()
	handle := fixtureTestHandle(t, c, held)

	_, err := c.HoldTransferFixtureRoots(ctx, "test-session", nil)
	require.Error(t, err)
	hold, err := c.HoldTransferFixtureRoots(ctx, "test-session", []*call.ID{handle})
	require.NoError(t, err)
	require.Equal(t, []uint64{uint64(row.id)}, hold.ResultIDs)
	require.Equal(t, 1, c.TransferFixtureHoldCount())

	dropped, err := c.DropTransferFixtureRetainedRoots(ctx, "test-session", []*call.ID{handle})
	require.NoError(t, err)
	require.Equal(t, []TransferFixtureDroppedRoot{{ResultID: uint64(row.id), Removed: true, Registered: true}}, dropped)
	again, err := c.DropTransferFixtureRetainedRoots(ctx, "test-session", []*call.ID{handle})
	require.NoError(t, err)
	require.False(t, again[0].Removed, "there is no saved edge left to remove")
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	c.egraphMu.RLock()
	require.Same(t, row, c.resultsByID[row.id], "the token alone keeps the row registered")
	c.egraphMu.RUnlock()

	released, err := c.ReleaseTransferFixtureHold(ctx, hold.Token)
	require.NoError(t, err)
	require.Equal(t, hold.ResultIDs, released.ResultIDs)
	c.egraphMu.RLock()
	_, registered := c.resultsByID[row.id]
	c.egraphMu.RUnlock()
	require.False(t, registered, "releasing the last owner collects the row")
	_, err = c.ReleaseTransferFixtureHold(ctx, hold.Token)
	require.ErrorContains(t, err, "unknown fixture hold token")
	require.Zero(t, c.TransferFixtureHoldCount())
}

func TestFixtureHoldTokensEndAtClose(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	held := persistedListTestResult(t, ctx, c, srv, "held-at-close", String("held"))
	_, err := c.HoldTransferFixtureRoots(ctx, "test-session", []*call.ID{fixtureTestHandle(t, c, held)})
	require.NoError(t, err)
	require.NoError(t, c.ReleaseSession(ctx, "test-session"))
	closeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	require.NoError(t, c.Close(closeCtx))
	require.Zero(t, c.TransferFixtureHoldCount(), "no token survives the cache")
}

// The observation bound: a report whose bound was exceeded fails instead of
// returning a silently shortened event list, and a new bound starts a new
// scenario.
func TestFixtureObserverOverflow(t *testing.T) {
	ctx, c, srv, _ := shareTestCache(t)
	c.EnableTransferFixtureParts()
	barrier := newSharePassBarrier(c)
	donor, receiver := shareTestPair(t, ctx, c, srv,
		map[string]sharePartState{"fs": {Snapshot: "fs-snap"}},
		map[string]sharePartState{"fs": {}},
	)
	c.SetTransferFixtureEventCap(1)
	shareTestUnite(t, ctx, c, "fixture-overflow", donor, receiver)
	require.Equal(t, 1, barrier.awaitPass(t), "one install records an installation, an owner sync and a settlement")
	_, err := c.TransferFixtureSnapshot(ctx, "test-session", nil)
	require.ErrorIs(t, err, ErrTransferFixtureOverflow)

	c.SetTransferFixtureEventCap(64)
	report, err := c.TransferFixtureSnapshot(ctx, "test-session", nil)
	require.NoError(t, err)
	require.Empty(t, report.Parts, "the new bound cleared the old scenario's events")
	require.Zero(t, report.Controls.HoldTokens)
	require.Zero(t, report.Controls.ArmedBarriers)
}
