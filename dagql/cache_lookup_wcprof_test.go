package dagql

import (
	"context"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// profiledCall runs frame through the cache under a recorded op, unarmed by
// any span, and returns the call's op event and the facts linked from it.
func profiledCall(t *testing.T, ctx context.Context, c *Cache, srv *Server, req *CallRequest) (profiledEvent, []profiledEvent, AnyResult) {
	t.Helper()
	ctx, root := profiledRoot(ctx, "test.lookup")
	res, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		return NewResultForCall(&transferTestValue{Text: "computed"}, req.ResultCall)
	})
	require.NoError(t, err)
	root.End(wcprof.OutcomeOK)
	events, _ := profiledUnder(t, root.ID())
	var call profiledEvent
	for _, ev := range events {
		if ev.Type == "op" && ev.OpKind == "call" && ev.ParentID == root.ID() {
			call = ev
		}
	}
	require.NotZero(t, call.OpID, "the call is recorded")
	return call, profiledLinks(events, "fact", call.OpID), res
}

func factNames(facts []profiledEvent) []string {
	var names []string
	for _, fact := range facts {
		names = append(names, fact.ident)
	}
	slices.Sort(names)
	return names
}

// A profiled call says why its lookup found what it did, in the vocabulary
// of its span's cache attributes, even with no span to arm the decision's
// carrier: a hit on a merged value is a remote hit, about the result it hit.
// The carrier it armed is gone after the call, so the call's span sees what
// it would have.
func TestLookupFactsProfiledRemoteHit(t *testing.T) {
	bundle, _ := mergeTestSource(t, "root", "sent")
	ctx, c, srv := transferTestCache(t)
	_, err := c.MergeValues(ctx, cloudCacheID, bundle)
	require.NoError(t, err)

	req := &CallRequest{ResultCall: evidenceRemoteTestRootFrame(), IsPersistable: true}
	call, facts, res := profiledCall(t, ctx, c, srv, req)
	require.True(t, res.HitCache())
	require.Equal(t, "hit", call.Outcome)
	require.Equal(t, []string{"hit.remote"}, factNames(facts))
	require.Equal(t, profResultID(res), facts[0].ResultID)
	require.Nil(t, req.CacheEvidence, "the carrier the profile armed is disarmed")
}

// A miss says which candidates it lost and why: here a merged value that
// expired.
func TestLookupFactsProfiledExpiredRemote(t *testing.T) {
	bundle, _ := mergeTestSource(t, "root", "sent")
	ctx, c, srv := transferTestCache(t)
	reply, err := c.MergeValues(ctx, cloudCacheID, bundle)
	require.NoError(t, err)
	merged, err := c.LoadResultByResultID(ctx, "", srv, reply.Imported()[0].ResultID)
	require.NoError(t, err)
	currentEntryTestExpire(c, merged)

	armed := NewCacheDecision()
	req := &CallRequest{ResultCall: evidenceRemoteTestRootFrame(), IsPersistable: true, CacheEvidence: armed}
	call, facts, res := profiledCall(t, ctx, c, srv, req)
	require.False(t, res.HitCache())
	require.Equal(t, "executed", call.Outcome)
	names := factNames(facts)
	require.Contains(t, names, "miss.saw_expired")
	require.Contains(t, names, "miss.expired_remote")
	require.Same(t, armed, req.CacheEvidence, "a carrier a span armed stays")
}

// A lookup that waits for the cache's graph lock records the wait.
func TestLookupProfiledGraphLockWait(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	frame := evidenceRemoteTestRootFrame()
	pctx, root := profiledRoot(ctx, "test.lookup")

	c.egraphMu.Lock()
	unlock, called := runHoldingGraphLock(t, c, func() error {
		_, err := c.GetOrInitCall(pctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "computed"}, frame)
		})
		return err
	})
	require.Eventually(t, func() bool {
		buf := make([]byte, 1<<20)
		for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, "wcprof.LockRW(") && strings.Contains(stack, "lookupCacheForRequest(") {
				return true
			}
		}
		return false
	}, 10*time.Second, time.Millisecond, "the lookup blocks on the graph lock")
	unlock()
	require.NoError(t, called())
	root.End(wcprof.OutcomeOK)

	events, ops := profiledUnder(t, root.ID())
	var waits int
	for _, ev := range events {
		if ev.Type == "wait" && ev.ident == "dagql.Cache.egraphMu" {
			require.Equal(t, "lock", ev.Reason)
			require.Equal(t, "call", ops[ev.ParentID].OpKind, "the call waited")
			waits++
		}
	}
	require.Equal(t, 1, waits)
}

// A publication that waits for the graph lock records the wait under its own
// op, inside the op's interval: the shared execution's op has ended by then.
func TestPublicationProfiledGraphLockWait(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	frame := evidenceRemoteTestRootFrame()
	pctx, root := profiledRoot(ctx, "test.publish")

	locked := make(chan struct{})
	c.testBeforePublicationIndex = func(*ongoingCall) {
		c.egraphMu.Lock()
		close(locked)
	}
	unlock, called := runHoldingGraphLock(t, c, func() error {
		_, err := c.GetOrInitCall(pctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
			return NewResultForCall(&transferTestValue{Text: "computed"}, frame)
		})
		return err
	})
	<-locked
	c.testBeforePublicationIndex = nil
	require.Eventually(t, func() bool {
		buf := make([]byte, 1<<20)
		for _, stack := range strings.Split(string(buf[:runtime.Stack(buf, true)]), "\n\n") {
			if strings.Contains(stack, "wcprof.LockRW(") && strings.Contains(stack, "initCompletedResult(") {
				return true
			}
		}
		return false
	}, 10*time.Second, time.Millisecond, "the publication blocks on the graph lock")
	unlock()
	require.NoError(t, called())
	root.End(wcprof.OutcomeOK)

	events, ops := profiledUnder(t, root.ID())
	var waits int
	for _, ev := range events {
		if ev.Type != "wait" || ev.ident != "dagql.Cache.egraphMu" {
			continue
		}
		pub := ops[ev.ParentID]
		require.Equal(t, "dagql.publishResult", pub.class, "the publication waited")
		require.GreaterOrEqual(t, ev.StartNS, pub.StartNS)
		require.LessOrEqual(t, ev.EndNS, pub.EndNS)
		waits++
	}
	require.Equal(t, 1, waits)
}

// runHoldingGraphLock runs call in the background while the test holds, or is
// about to hold, c's graph lock. unlock releases the lock once, and called
// waits for call and returns its error. However the test ends, even on a
// failed assertion with the lock held, its cleanup releases the lock and waits
// for call first: it runs before the fixture's, which takes the lock to close
// the cache.
func runHoldingGraphLock(t *testing.T, c *Cache, call func() error) (unlock func(), called func() error) {
	t.Helper()
	var once sync.Once
	unlock = func() { once.Do(c.egraphMu.Unlock) }
	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		err = call()
	}()
	called = func() error {
		<-done
		return err
	}
	t.Cleanup(func() {
		unlock()
		<-done
	})
	return unlock, called
}
