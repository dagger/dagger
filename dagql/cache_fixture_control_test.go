package dagql

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
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

	// Every reached point was journalled, armed or not, in order and with
	// the pass that reached it.
	var points []FixtureBarrierPoint
	var last uint64
	for _, o := range c.partFixtureReached() {
		require.Greater(t, o.Sequence, last)
		last = o.Sequence
		if o.PassID == reached.Event.PassID {
			points = append(points, o.Point)
		}
	}
	require.Equal(t, []FixtureBarrierPoint{FixtureSharePassTaken, FixtureShareAllPrepared, FixtureShareMembersReleased, FixtureBeforeFinish}, points)
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

// A hold admitted before Close but resumed after Close swept the tokens must
// not publish one: Close would then succeed with a fixture owner outstanding,
// and closeOnce means nothing would ever release it. The late hold is refused
// and the ownership it had already taken is released through the ordinary
// path, so the row is as collectable as if the hold had never been asked for.
func TestFixtureHoldAdmittedBeforeCloseLeavesNoToken(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	held := persistedListTestResult(t, ctx, c, srv, "held-across-close", String("held"))
	id := fixtureTestHandle(t, c, held)
	row := held.cacheSharedResult()
	c.egraphMu.RLock()
	before := row.incomingOwnershipCount
	c.egraphMu.RUnlock()

	entered, resume, swept := make(chan struct{}), make(chan struct{}), make(chan struct{})
	c.testAfterSessionOperationEnter = func(string) {
		close(entered)
		select {
		case <-resume:
		case <-ctx.Done():
		}
	}
	// Close calls this after it swept the tokens, as it starts to drain the
	// admitted operations.
	c.testAfterCacheClosing = func() { close(swept) }
	holdErr := make(chan error, 1)
	go func() {
		_, err := c.HoldTransferFixtureRoots(ctx, "test-session", []*call.ID{id})
		holdErr <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("the hold was never admitted")
	}
	closed := make(chan error, 1)
	go func() { closed <- c.Close(ctx) }()
	select {
	case <-swept:
	case <-ctx.Done():
		t.Fatal("close never reached its drain")
	}
	close(resume)
	select {
	case err := <-holdErr:
		require.ErrorIs(t, err, ErrCacheClosed, "a hold that lost the race with Close is refused")
	case <-ctx.Done():
		t.Fatal("the hold never returned")
	}
	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-ctx.Done():
		t.Fatal("close never returned")
	}
	require.Zero(t, c.TransferFixtureHoldCount(), "no token was published after the sweep")
	c.egraphMu.RLock()
	after := row.incomingOwnershipCount
	c.egraphMu.RUnlock()
	require.LessOrEqual(t, after, before, "the refused hold's ownership was rolled back")
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

	require.NotEmpty(t, c.partFixtureReached(), "the pass's reached points were journalled")
	c.SetTransferFixtureEventCap(64)
	require.Empty(t, c.partFixtureReached(), "a new observation scope starts without the old scenario's reached points")
	report, err := c.TransferFixtureSnapshot(ctx, "test-session", nil)
	require.NoError(t, err)
	require.Empty(t, report.Parts, "the new bound cleared the old scenario's events")
	require.Zero(t, report.Controls.HoldTokens)
	require.Zero(t, report.Controls.ArmedBarriers)
}

// decodeJoined is the joiner's side of a shared persisted decode: a second
// demand that parks on the leader's channel reaches it, by row, while the
// leader is still inside the decode.
func TestFixtureBarrierDecodeJoined(t *testing.T) {
	ctx := cacheTestContext(t.Context())
	dbPath := filepath.Join(t.TempDir(), "cache.db")
	persistRetryDecodeSnapshotID.Store("")
	resultID := persistRetryDecodeSeed(t, ctx, dbPath, nil)
	c, err := NewCache(ctx, dbPath, nil, nil)
	require.NoError(t, err)
	c.EnableTransferFixtureParts()
	srv := newPersistRetryDecodeTestServer()

	// Everything that can hold a load is released on every exit, and the
	// loads are joined, before Close waits for them: a failed assertion must
	// report promptly instead of consuming the package timeout. Deferred
	// calls run last in, first out, so Close is deferred first.
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		require.NoError(t, c.Close(closeCtx))
	}()
	var loads []<-chan error
	defer func() {
		for _, done := range loads {
			select {
			case <-done:
			case <-time.After(10 * time.Second):
				t.Error("a load never returned during cleanup")
			}
		}
	}()
	leaderEntered, releaseLeader := make(chan struct{}), make(chan struct{})
	unblockLeader := sync.OnceFunc(func() { close(releaseLeader) })
	defer unblockLeader()
	armed, err := c.ArmTransferFixtureBarrier(FixtureBarrierRequest{Key: "joined", Point: FixtureDecodeJoined, Selector: FixtureBarrierSelector{ResultID: resultID}, Action: FixturePause})
	require.NoError(t, err)
	defer func() { _ = c.ReleaseTransferFixtureBarrier("joined", armed.Generation) }()

	var entries atomic.Int32
	persistRetryDecodeHooks.Store(resultID, func(hookCtx context.Context) error {
		if entries.Add(1) == 1 {
			close(leaderEntered)
			select {
			case <-releaseLeader:
			case <-hookCtx.Done():
				return hookCtx.Err()
			}
		}
		return nil
	})
	defer persistRetryDecodeHooks.Delete(resultID)

	load := func(sessionID string) <-chan error {
		loadCtx := engine.ContextWithClientMetadata(ctx, &engine.ClientMetadata{ClientID: sessionID + "-client", SessionID: sessionID})
		loadCtx = srvToContext(ContextWithCache(loadCtx, c), srv)
		done := make(chan error, 1)
		go func() {
			_, err := c.LoadResultByResultID(loadCtx, sessionID, srv, resultID)
			done <- err
			// Closed after its one value, so the cleanup's join returns at
			// once for a load the test already received.
			close(done)
		}()
		loads = append(loads, done)
		return done
	}
	leader := load("decode-joined-leader")
	select {
	case <-leaderEntered:
	case <-time.After(10 * time.Second):
		t.Fatal("the leader never entered the decode")
	}
	joiner := load("decode-joined-joiner")
	waitCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	reached, err := c.WaitTransferFixtureBarrier(waitCtx, "joined", armed.Generation)
	require.NoError(t, err, "the joiner never parked")
	require.Equal(t, FixtureDecodeJoined, reached.Event.Point)
	require.Equal(t, resultID, reached.Event.ResultID)
	require.EqualValues(t, 1, entries.Load(), "the joiner did not start a decode of its own")

	require.NoError(t, c.ReleaseTransferFixtureBarrier("joined", armed.Generation))
	unblockLeader()
	for _, done := range []<-chan error{leader, joiner} {
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(10 * time.Second):
			t.Fatal("a load never returned")
		}
	}
	require.EqualValues(t, 1, entries.Load(), "one decode served both")
}
