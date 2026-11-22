package dagql

import (
	"context"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

// engineWideReads are the reads of the engine-wide report among reads: those
// that carry no session.
func engineWideReads(reads []PartRead) []PartRead {
	var out []PartRead
	for _, read := range reads {
		if read.Session == "" {
			out = append(out, read)
		}
	}
	return out
}

// newPartsReadTestResult is newPartsTestResult under a field of its own, so
// that results of different fields are different entries.
func newPartsReadTestResult(t *testing.T, c *Cache, ctx context.Context, obj *cacheTestPartsObject, field string) ObjectResult[*cacheTestPartsObject] {
	t.Helper()
	srv := cacheTestPartsServer(t)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestPartsObject{}).Type()),
		Field: field,
	}
	resAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: frame}, func(context.Context) (AnyResult, error) {
		return NewObjectResultForCall(obj, srv, frame)
	})
	assert.NilError(t, err)
	return resAny.(ObjectResult[*cacheTestPartsObject])
}

// newPartsReadTestValue is a parts value whose output group completes its
// filesystem part, after running before, when set.
func newPartsReadTestValue(t *testing.T, c *Cache, ctx context.Context, field string, before func(context.Context) error) ObjectResult[*cacheTestPartsObject] {
	t.Helper()
	obj := &cacheTestPartsObject{resolveFn: partsTestDirectResolve}
	var res ObjectResult[*cacheTestPartsObject]
	obj.groupEval = map[LazyGroupKey]LazyEvalFunc{partsTestGroupOut: func(ctx context.Context) error {
		if before != nil {
			if err := before(ctx); err != nil {
				return err
			}
		}
		return c.partHostFor(res.cacheSharedResult()).RunNative(ctx, partsTestGroupOut, []PartKey{partsTestPartFS}, func(context.Context) error { return nil })
	}}
	res = newPartsReadTestResult(t, c, ctx, obj, field)
	return res
}

// The cache reports the parts of entries that work reads, when a part is
// read for the first time: a consumer that evaluates one part of a
// dependency reads that part, and one it evaluates whole, the whole entry.
// Each report about an entry names every part read of it so far, so a
// reader that missed an earlier one loses nothing; a part read again isn't
// reported again, nor an entry read whole. An entry the cache no longer
// holds leaves nothing behind.
func TestCacheReportsPartsRead(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var reported [][]PartRead
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil, WithPartReadReport(func(reads []PartRead) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, engineWideReads(reads))
	}))
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, c)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })

	part := newPartsReadTestValue(t, c, ctx, "read-a-part", nil)
	whole := newPartsReadTestValue(t, c, ctx, "read-whole", nil)
	consumer := newPartsReadTestValue(t, c, ctx, "consumer", func(ctx context.Context) error {
		if err := c.EvaluateParts(ctx, part, partsTestPartFS); err != nil {
			return err
		}
		if err := c.EvaluateParts(ctx, whole, partsTestPartFS); err != nil {
			return err
		}
		return c.Evaluate(ctx, whole)
	})
	assert.NilError(t, c.EvaluateParts(ctx, consumer, partsTestPartFS))
	assert.NilError(t, c.EvaluateParts(ctx, part, partsTestPartFS))
	assert.NilError(t, c.EvaluateParts(ctx, whole, partsTestPartFS))
	c.partReads.flush()
	assert.NilError(t, c.EvaluateParts(ctx, part, partsTestPartXMeta))
	assert.NilError(t, c.EvaluateParts(ctx, part, partsTestPartFS))
	c.partReads.flush()

	id := func(r ObjectResult[*cacheTestPartsObject]) uint64 { return uint64(r.cacheSharedResult().id) }
	mu.Lock()
	assert.DeepEqual(t, [][]PartRead{
		{
			{ResultID: id(consumer), Part: partsTestPartFS},
			{ResultID: id(part), Part: partsTestPartFS},
			{ResultID: id(whole)},
		},
		{
			{ResultID: id(part), Part: partsTestPartFS},
			{ResultID: id(part), Part: partsTestPartXMeta},
		},
	}, reported)
	mu.Unlock()

	cacheTestReleaseSession(t, c, ctx)
	// A lazy evaluation's operation can still be ending when ReleaseSession
	// returns, and the release's cleanup, which forgets the entries' reads,
	// then runs when it ends.
	assert.NilError(t, c.WaitSessionRelease(ctx, cacheTestSessionID(t, ctx)))
	c.partReads.mu.Lock()
	defer c.partReads.mu.Unlock()
	assert.Equal(t, 0, len(c.partReads.seen), "released entries' reads are forgotten")
}

// A flush, as the cache's close makes, returns only once a report already
// under way, such as the timer's, has been made: no read is reported after
// the cache closed.
func TestPartReadFlushWaitsForAReportUnderWay(t *testing.T) {
	t.Parallel()
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	var mu sync.Mutex
	var reports [][]PartRead
	log := &partReadLog{seen: map[sharedResultID]*partReadsSeen{}, pending: map[sharedResultID]struct{}{}}
	log.report = func(reads []PartRead) {
		mu.Lock()
		reports = append(reports, reads)
		first := len(reports) == 1
		mu.Unlock()
		if first {
			close(started)
			<-release
		}
	}
	log.record(1, []PartKey{"fs"})
	go log.flush()
	<-started

	closed := make(chan struct{})
	go func() {
		log.flush()
		close(closed)
	}()
	select {
	case <-closed:
		t.Fatal("the close's flush returned while the timer's report was under way")
	case <-time.After(200 * time.Millisecond):
	}
	once.Do(func() { close(release) })
	<-closed
	mu.Lock()
	defer mu.Unlock()
	assert.DeepEqual(t, reports, [][]PartRead{{{ResultID: 1, Part: "fs"}}})
}

// A read is reported once work asks for the part, not once the part is made:
// a consumer waiting on a dependency still running has read it, and a service
// weighing the dependency as it completes knows so.
func TestCacheReportsAPartReadWhileItsWorkRuns(t *testing.T) {
	t.Parallel()
	var mu sync.Mutex
	var reported []PartRead
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil, WithPartReadReport(func(reads []PartRead) {
		mu.Lock()
		defer mu.Unlock()
		reported = append(reported, engineWideReads(reads)...)
	}))
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, c)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })

	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }) })
	running := newPartsReadTestValue(t, c, ctx, "running", func(context.Context) error {
		close(started)
		<-release
		return nil
	})
	done := make(chan error, 1)
	go func() { done <- c.EvaluateParts(ctx, running, partsTestPartFS) }()
	<-started

	c.partReads.flush()
	mu.Lock()
	assert.DeepEqual(t, []PartRead{{ResultID: uint64(running.cacheSharedResult().id), Part: partsTestPartFS}}, reported)
	mu.Unlock()
	once.Do(func() { close(release) })
	assert.NilError(t, <-done)
}

// readsTestCache is a cache that reports part reads into reported, and a
// call maker over it: a call of field, session-bound or uncached, whose
// resolver runs fn, reported to telemetry when cached.
func readsTestCache(t *testing.T) (context.Context, *Cache, string, *[]PartRead, *sync.Mutex, func(ctx context.Context, field string, sessionBound, uncached bool, fn func(context.Context) error)) {
	t.Helper()
	var mu sync.Mutex
	reported := &[]PartRead{}
	ctx := cacheTestContext(t.Context())
	c, err := NewCache(ctx, "", nil, nil, WithPartReadReport(func(reads []PartRead) {
		mu.Lock()
		defer mu.Unlock()
		*reported = append(*reported, reads...)
	}))
	assert.NilError(t, err)
	ctx = ContextWithCache(ctx, c)
	t.Cleanup(func() { assert.NilError(t, c.CloseDiscardingPersistence()) })
	session := cacheTestSessionID(t, ctx)
	srv := cacheTestPartsServer(t)
	call := func(ctx context.Context, field string, sessionBound, uncached bool, fn func(context.Context) error) {
		t.Helper()
		frame := &ResultCall{Kind: ResultCallKindField, Type: NewResultCallType((&cacheTestPartsObject{}).Type()), Field: field}
		if sessionBound {
			arg, err := resultCallArgFromInput(ctx, PerSessionInput.Name, NewString(session), false)
			assert.NilError(t, err)
			frame.ImplicitInputs = []*ResultCallArg{arg}
		}
		_, err := c.GetOrInitCall(ctx, session, srv, &CallRequest{ResultCall: frame, DoNotCache: uncached, CacheReported: !uncached}, func(ctx context.Context) (AnyResult, error) {
			if err := fn(ctx); err != nil {
				return nil, err
			}
			return NewObjectResultForCall(&cacheTestPartsObject{resolveFn: partsTestDirectResolve}, srv, frame)
		})
		assert.NilError(t, err)
	}
	return ctx, c, session, reported, &mu, call
}

// sessionReadsOf are the reads among reported that carry a session, with
// their times left out, which the tests only check lie in the test.
func sessionReadsOf(t *testing.T, reported []PartRead, before time.Time) []PartRead {
	t.Helper()
	var out []PartRead
	for _, read := range reported {
		if read.Session == "" {
			continue
		}
		assert.Assert(t, !read.At.Before(before) && !read.At.After(time.Now()), "a read's time is when it was made")
		read.At = time.Time{}
		out = append(out, read)
	}
	return out
}

// A read counts when no call above it, up to the session's top, is a cached
// call that isn't session-bound: every session that gets there makes it
// again. It is reported once per part and session, with the session and when
// it was made, beside the engine-wide report. So a read at the session's top
// counts, and so does one in an uncached call or a session-bound call there,
// and a second read of a part in the session isn't reported again. A cached
// call's own reads don't count, nor does an uncached or session-bound call's
// under it, nor the evaluation of a result a counted read reads, which reads
// on that result's call's account.
func TestCacheReportsTheReadsEverySessionMakes(t *testing.T) {
	t.Parallel()
	ctx, c, session, reported, mu, call := readsTestCache(t)
	atTop := newPartsReadTestValue(t, c, ctx, "read-at-the-session-top", nil)
	byUncached := newPartsReadTestValue(t, c, ctx, "read-by-an-uncached-call", nil)
	bySessionBound := newPartsReadTestValue(t, c, ctx, "read-by-a-session-bound-call", nil)
	readByEvaluation := newPartsReadTestValue(t, c, ctx, "read-by-an-evaluation", nil)
	evaluated := newPartsReadTestValue(t, c, ctx, "evaluated-for-the-uncached-call", func(ctx context.Context) error {
		return c.EvaluateParts(ctx, readByEvaluation, partsTestPartFS)
	})
	byCached := newPartsReadTestValue(t, c, ctx, "read-by-a-cached-call", nil)
	underCached := newPartsReadTestValue(t, c, ctx, "read-under-a-cached-call", nil)
	before := time.Now()
	assert.NilError(t, c.EvaluateParts(ctx, atTop, partsTestPartFS))
	call(ctx, "uncached-at-the-top", false, true, func(ctx context.Context) error {
		if err := c.EvaluateParts(ctx, byUncached, partsTestPartXMeta); err != nil {
			return err
		}
		if err := c.EvaluateParts(ctx, byUncached, partsTestPartFS); err != nil {
			return err
		}
		if err := c.EvaluateParts(ctx, byUncached, partsTestPartFS); err != nil {
			return err
		}
		return c.EvaluateParts(ctx, evaluated, partsTestPartFS)
	})
	call(ctx, "session-bound-at-the-top", true, false, func(ctx context.Context) error {
		return c.EvaluateParts(ctx, bySessionBound, partsTestPartFS)
	})
	call(ctx, "cached-at-the-top", false, false, func(ctx context.Context) error {
		if err := c.EvaluateParts(ctx, byCached, partsTestPartFS); err != nil {
			return err
		}
		call(ctx, "uncached-under-the-cached-call", false, true, func(ctx context.Context) error {
			call(ctx, "session-bound-under-the-cached-call", true, false, func(ctx context.Context) error {
				return c.EvaluateParts(ctx, underCached, partsTestPartFS)
			})
			return nil
		})
		return nil
	})
	// The same session reads a part again.
	assert.NilError(t, c.EvaluateParts(ctx, byUncached, partsTestPartFS))
	c.partReads.flush()

	id := func(r ObjectResult[*cacheTestPartsObject]) uint64 { return uint64(r.cacheSharedResult().id) }
	mu.Lock()
	defer mu.Unlock()
	assert.DeepEqual(t, []PartRead{
		{ResultID: id(atTop), Part: partsTestPartFS, Session: session},
		{ResultID: id(byUncached), Part: partsTestPartXMeta, Session: session},
		{ResultID: id(byUncached), Part: partsTestPartFS, Session: session},
		{ResultID: id(evaluated), Part: partsTestPartFS, Session: session},
		{ResultID: id(bySessionBound), Part: partsTestPartFS, Session: session},
	}, sessionReadsOf(t, *reported, before))
	engineWide := map[uint64][]PartKey{}
	for _, read := range engineWideReads(*reported) {
		engineWide[read.ResultID] = append(engineWide[read.ResultID], read.Part)
	}
	assert.DeepEqual(t, []PartKey{partsTestPartFS}, engineWide[id(readByEvaluation)])
	assert.DeepEqual(t, []PartKey{partsTestPartFS}, engineWide[id(byCached)])
	assert.DeepEqual(t, []PartKey{partsTestPartFS}, engineWide[id(underCached)])
}

// A nested client's requests, such as a module's while it answers a function
// call, are made from the session's context, and carry from the call they
// answer both its recipe and what part reads made under it count as
// (CurrentReadScope, ContextWithReadScope). So under a session-bound call at
// the session's top, a read at the request's top and an uncached call's read
// count, as they would in that call's own resolver. Under a cached call,
// which runs only when its lookup misses, neither does, and neither does a
// read under a cached call made in a request under a session-bound one.
func TestCacheReportsPartsReadUnderANestedClientsRequest(t *testing.T) {
	t.Parallel()
	ctx, c, session, reported, mu, call := readsTestCache(t)
	atTopUnderSessionBound := newPartsReadTestValue(t, c, ctx, "read-at-a-request's-top-under-a-session-bound-call", nil)
	uncachedUnderSessionBound := newPartsReadTestValue(t, c, ctx, "read-by-an-uncached-call-under-a-session-bound-call", nil)
	cachedInRequest := newPartsReadTestValue(t, c, ctx, "read-by-a-cached-call-in-a-request-under-a-session-bound-call", nil)
	atTopUnderCached := newPartsReadTestValue(t, c, ctx, "read-at-a-request's-top-under-a-cached-call", nil)
	uncachedUnderCached := newPartsReadTestValue(t, c, ctx, "read-by-an-uncached-call-under-a-cached-call", nil)
	// request makes a request as a nested client does for the call answered
	// runs under: from the session's context, carrying that call's recipe
	// and read scope.
	request := func(answered context.Context, fn func(context.Context) error) error {
		return fn(ContextWithReadScope(ContextWithCallerRecipe(ctx, CallerRecipe(answered)), CurrentReadScope(answered)))
	}
	before := time.Now()
	call(ctx, "session-bound-at-the-top", true, false, func(answered context.Context) error {
		return request(answered, func(ctx context.Context) error {
			if err := c.EvaluateParts(ctx, atTopUnderSessionBound, partsTestPartFS); err != nil {
				return err
			}
			call(ctx, "uncached-under-the-session-bound-call", false, true, func(ctx context.Context) error {
				return c.EvaluateParts(ctx, uncachedUnderSessionBound, partsTestPartFS)
			})
			call(ctx, "cached-in-the-request", false, false, func(ctx context.Context) error {
				call(ctx, "uncached-under-it", false, true, func(ctx context.Context) error {
					return c.EvaluateParts(ctx, cachedInRequest, partsTestPartFS)
				})
				return nil
			})
			return nil
		})
	})
	call(ctx, "cached-at-the-top", false, false, func(answered context.Context) error {
		return request(answered, func(ctx context.Context) error {
			if err := c.EvaluateParts(ctx, atTopUnderCached, partsTestPartFS); err != nil {
				return err
			}
			call(ctx, "uncached-under-the-cached-call", false, true, func(ctx context.Context) error {
				return c.EvaluateParts(ctx, uncachedUnderCached, partsTestPartFS)
			})
			return nil
		})
	})
	c.partReads.flush()

	id := func(r ObjectResult[*cacheTestPartsObject]) uint64 { return uint64(r.cacheSharedResult().id) }
	mu.Lock()
	defer mu.Unlock()
	assert.DeepEqual(t, []PartRead{
		{ResultID: id(atTopUnderSessionBound), Part: partsTestPartFS, Session: session},
		{ResultID: id(uncachedUnderSessionBound), Part: partsTestPartFS, Session: session},
	}, sessionReadsOf(t, *reported, before))
}
