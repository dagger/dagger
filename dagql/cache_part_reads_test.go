package dagql

import (
	"context"
	"sync"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

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
		reported = append(reported, reads)
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
		reported = append(reported, reads...)
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
