package dagql

import (
	"context"
	"fmt"
	"slices"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// freeTestResult publishes, under session, a result whose request, response
// and content digests start as three classes and end as one.
func freeTestResult(t *testing.T, c *Cache, ctx context.Context, session, name string, value int) AnyResult {
	t.Helper()
	request := cacheTestIntCall(name + "-request")
	response := cacheTestIntCall(name + "-response")
	res, err := c.GetOrInitCall(ctx, session, noopTypeResolver{}, &CallRequest{ResultCall: request}, func(ctx context.Context) (AnyResult, error) {
		return cacheTestIntResult(response, value).(Result[Int]).WithContentDigest(ctx, digest.FromString(name))
	})
	require.NoError(t, err)
	require.False(t, res.HitCache())
	return res
}

// freeTestChild is the call "child" on parentCall: a term whose input is
// parentCall's class.
func freeTestChild(parentCall *ResultCall) *ResultCall {
	child := cacheTestIntCall("child")
	child.Receiver = &ResultCallRef{Call: parentCall}
	return child
}

// freeTestSlots returns the class slots in use and the classes a term or an
// entry uses.
func freeTestSlots(c *Cache) (slots, live int) {
	c.egraphMu.Lock()
	defer c.egraphMu.Unlock()
	return c.eqClassSlotsLocked(), testLiveEqClassesLocked(c)
}

// A collection frees the classes it ends: once a session's release has
// collected its results and their children, every class slot left in use is
// a class that a term or an entry uses, and the result that stays still
// hits.
func TestACollectionFreesTheClassesItEnds(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	require.NoError(t, err)
	ctxA := cacheTestSessionContext(baseCtx, "ends-a")
	for i := range 4 {
		parent := freeTestResult(t, c, ctxA, "ends-a", fmt.Sprintf("ends-a-%d", i), i)
		parentCall, err := parent.ResultCall()
		require.NoError(t, err)
		child := freeTestChild(parentCall)
		_, err = c.GetOrInitCall(ctxA, "ends-a", noopTypeResolver{}, &CallRequest{ResultCall: child}, ValueFunc(cacheTestIntResult(child, 10+i)))
		require.NoError(t, err)
	}
	ctxB := cacheTestSessionContext(baseCtx, "ends-b")
	stays := cacheTestIntCall("ends-b")
	_, err = c.GetOrInitCall(ctxB, "ends-b", noopTypeResolver{}, &CallRequest{ResultCall: stays}, ValueFunc(cacheTestIntResult(stays, 7)))
	require.NoError(t, err)
	before, _ := freeTestSlots(c)

	require.NoError(t, c.ReleaseSession(ctxA, "ends-a"))
	slots, live := freeTestSlots(c)
	require.Less(t, slots, before, "the release ended classes")
	require.Equal(t, live, slots, "every class slot left in use is a live class")

	hit, err := c.GetOrInitCall(ctxB, "ends-b", noopTypeResolver{}, &CallRequest{ResultCall: stays}, func(context.Context) (AnyResult, error) {
		return nil, fmt.Errorf("the result that stays missed")
	})
	require.NoError(t, err)
	require.True(t, hit.HitCache())
	require.Equal(t, 7, cacheTestUnwrapInt(t, hit))
	require.NoError(t, c.ReleaseSession(ctxB, "ends-b"))
}

// The Cloud's collection frees the classes it ends too, however few they are
// beside the live ones.
func TestACloudCollectionFreesTheClassesItEnds(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	cloud, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	for i := range 10 {
		key := HolderKey{Cache: "cache-a", Number: uint64(i + 1)}
		_, err := cloud.AttachRemoteHolding(ctx, key, holdingOf(fmt.Sprintf("cloud-ends-%d", i)))
		require.NoError(t, err)
		session := "stays"
		if i < 3 {
			session = "ends"
		}
		require.NoError(t, errOf(cloud.AddRemoteHold(ctx, key, session)))
	}
	change := cloud.ReleaseRemoteSession(ctx, "cache-a", "ends")
	collected, _, err := cloud.CollectRemoteHoldings(ctx, change.Candidates, nil)
	require.NoError(t, err)
	require.Len(t, collected, 3)
	slots, live := freeTestSlots(cloud)
	require.Equal(t, 7, live)
	require.Equal(t, live, slots, "every class slot left in use is a live class")
}

// A class a union joins into another is freed by the next collection: no
// slot is left joined, and every digest of the joined classes still finds
// the entry.
func TestAJoinedClassIsFreedByTheNextCollection(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	require.NoError(t, err)
	ctxA := cacheTestSessionContext(baseCtx, "joined-a")
	res := freeTestResult(t, c, ctxA, "joined-a", "joined-a", 1)
	ctxB := cacheTestSessionContext(baseCtx, "joined-b")
	freeTestResult(t, c, ctxB, "joined-b", "joined-b", 2)
	require.NoError(t, c.ReleaseSession(ctxB, "joined-b"))

	slots, live := freeTestSlots(c)
	require.Equal(t, live, slots, "every class slot left in use is a live class")
	c.egraphMu.RLock()
	for id := 1; id < len(c.egraphParents); id++ {
		parent := c.egraphParents[id]
		require.True(t, parent == 0 || parent == eqClassID(id), "class %d is still joined into %d", id, parent)
	}
	c.egraphMu.RUnlock()

	resCall, err := res.ResultCall()
	require.NoError(t, err)
	for _, dig := range []digest.Digest{
		cacheTestCallDigest(cacheTestIntCall("joined-a-request")),
		cacheTestCallDigest(resCall),
		digest.FromString("joined-a"),
	} {
		hit, ok, err := c.lookupCacheForDigests(ctxA, "joined-a", noopTypeResolver{}, dig, nil)
		require.NoError(t, err)
		require.True(t, ok, "digest %s finds the entry", dig)
		require.Equal(t, 1, cacheTestUnwrapInt(t, hit))
	}
	require.NoError(t, c.ReleaseSession(ctxA, "joined-a"))
}

// A dead class's number goes to a new class, and nothing of the dead class
// reaches that class's entries: a lookup through each digest the dead class
// held misses, and so does a call that took the dead class as its input,
// though the new class's entry has the same call over it.
func TestADeadClassesDigestsNeverReachTheClassThatTakesItsNumber(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	require.NoError(t, err)
	// A result that stays, so that the e-graph isn't reset when A goes.
	ctxStays := cacheTestSessionContext(baseCtx, "reused-stays")
	freeTestResult(t, c, ctxStays, "reused-stays", "reused-stays", 0)
	ctxA := cacheTestSessionContext(baseCtx, "reused-a")
	a := freeTestResult(t, c, ctxA, "reused-a", "reused-a", 1)
	aCall, err := a.ResultCall()
	require.NoError(t, err)
	aChild := freeTestChild(aCall)
	_, err = c.GetOrInitCall(ctxA, "reused-a", noopTypeResolver{}, &CallRequest{ResultCall: aChild}, ValueFunc(cacheTestIntResult(aChild, 2)))
	require.NoError(t, err)
	c.egraphMu.Lock()
	dead := c.findEqClassLocked(c.egraphDigestToClass[cacheTestCallDigest(aCall).String()])
	deadDigests := c.eqClassToDigests[dead].Clone()
	c.egraphMu.Unlock()
	require.NotZero(t, dead)
	require.GreaterOrEqual(t, deadDigests.Size(), 3)
	require.NoError(t, c.ReleaseSession(ctxA, "reused-a"))

	// New results, the dead class's number next on the free list each time,
	// until one's class takes it.
	ctxB := cacheTestSessionContext(baseCtx, "reused-b")
	var bCall *ResultCall
	for i := 0; i < 8 && bCall == nil; i++ {
		c.egraphMu.Lock()
		at := slices.Index(c.freeEqClassIDs, dead)
		if at >= 0 {
			c.freeEqClassIDs = append(slices.Delete(c.freeEqClassIDs, at, at+1), dead)
		}
		c.egraphMu.Unlock()
		require.GreaterOrEqual(t, at, 0, "the dead class's number is free")
		b := freeTestResult(t, c, ctxB, "reused-b", fmt.Sprintf("reused-b-%d", i), 100+i)
		call, err := b.ResultCall()
		require.NoError(t, err)
		c.egraphMu.Lock()
		if c.findEqClassLocked(c.egraphDigestToClass[cacheTestCallDigest(call).String()]) == dead {
			bCall = call
		}
		c.egraphMu.Unlock()
	}
	require.NotNil(t, bCall, "a new class takes the dead class's number")
	bChild := freeTestChild(bCall)
	_, err = c.GetOrInitCall(ctxB, "reused-b", noopTypeResolver{}, &CallRequest{ResultCall: bChild}, ValueFunc(cacheTestIntResult(bChild, 99)))
	require.NoError(t, err)

	ctxC := cacheTestSessionContext(baseCtx, "reused-c")
	for dig := range deadDigests.All() {
		_, ok, err := c.lookupCacheForDigests(ctxC, "reused-c", noopTypeResolver{}, digest.Digest(dig), nil)
		require.NoError(t, err)
		require.False(t, ok, "the dead class's digest %s finds nothing", dig)
	}
	ran := false
	got, err := c.GetOrInitCall(ctxC, "reused-c", noopTypeResolver{}, &CallRequest{ResultCall: aChild}, func(context.Context) (AnyResult, error) {
		ran = true
		return cacheTestIntResult(aChild, 3), nil
	})
	require.NoError(t, err)
	require.True(t, ran, "the call over the dead class misses")
	require.Equal(t, 3, cacheTestUnwrapInt(t, got))
	require.NoError(t, c.ReleaseSession(ctxC, "reused-c"))
	require.NoError(t, c.ReleaseSession(ctxB, "reused-b"))
	require.NoError(t, c.ReleaseSession(ctxStays, "reused-stays"))
}

// Freed class numbers feed new classes: under a churn of sessions beside one
// result that stays, the class slots never grow past what one session holds
// at once.
func TestFreedClassNumbersFeedNewClasses(t *testing.T) {
	t.Parallel()
	baseCtx := t.Context()
	c, err := NewCache(baseCtx, "", nil, nil)
	require.NoError(t, err)
	ctxStays := cacheTestSessionContext(baseCtx, "churn-stays")
	freeTestResult(t, c, ctxStays, "churn-stays", "churn-stays", 1)
	most := 0
	for i := range 100 {
		session := fmt.Sprintf("churn-%d", i)
		ctx := cacheTestSessionContext(baseCtx, session)
		freeTestResult(t, c, ctx, session, session, i)
		require.NoError(t, c.ReleaseSession(ctx, session))
		c.egraphMu.RLock()
		if n := len(c.egraphParents); n > most {
			most = n
		}
		c.egraphMu.RUnlock()
	}
	require.Less(t, most, 16, "the slots stay at what one session and the result that stays use")
	require.NoError(t, c.ReleaseSession(ctxStays, "churn-stays"))
}
