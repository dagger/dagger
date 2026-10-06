package dagql

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"gotest.tools/v3/assert"
)

// Two sessions that execute the same list call concurrently can each get the
// list in a different order (JSONValue.fields walks a Go map). Reading the
// items element by element must still return each session's own list, not a
// mix of both.
func TestNthValueConcurrentListExecutionsDoNotMixItems(t *testing.T) {
	t.Parallel()
	baseCtx := cacheTestContext(t.Context())
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	baseCtx = ContextWithCache(baseCtx, c)
	srv := cacheTestServer(t)

	orders := [][]string{{"a", "b", "c"}, {"c", "a", "b"}}
	var calls atomic.Int32
	secondStarted := make(chan struct{})
	Fields[*cacheTestObject]{
		Func("keys", func(_ context.Context, _ *cacheTestObject, _ struct{}) (Array[String], error) {
			n := calls.Add(1)
			if n == 1 {
				// keep the first execution in flight until the second one starts,
				// so neither call can hit the other's result
				<-secondStarted
			} else {
				close(secondStarted)
			}
			return NewStringArray(orders[n-1]...), nil
		}),
	}.Install(srv)

	parent := cacheTestDetachedObjectResult(&ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "parent",
	}, srv, &cacheTestObject{Value: 1})

	sessions := []context.Context{
		srvToContext(cacheTestSessionContext(baseCtx, "session-1"), srv),
		srvToContext(cacheTestSessionContext(baseCtx, "session-2"), srv),
	}
	lists := make([]AnyResult, len(sessions))
	errs := make([]error, len(sessions))
	var wg sync.WaitGroup
	for i, ctx := range sessions {
		wg.Add(1)
		go func() {
			defer wg.Done()
			lists[i], errs[i] = parent.Select(ctx, srv, Selector{Field: "keys"})
		}()
	}
	wg.Wait()
	for _, err := range errs {
		assert.NilError(t, err)
	}
	assert.Equal(t, int32(2), calls.Load())

	// read the items the way the server builds a list response, one at a
	// time, with the two sessions taking turns to read each position first
	got := make([][]string, len(sessions))
	for nth := 1; nth <= 3; nth++ {
		for j := range sessions {
			i := (nth + j) % len(sessions)
			item, err := lists[i].NthValue(sessions[i], nth)
			assert.NilError(t, err)
			got[i] = append(got[i], item.Unwrap().(String).String())
		}
	}
	for i := range sessions {
		var want []string
		for _, s := range lists[i].Unwrap().(Array[String]) {
			want = append(want, s.String())
		}
		assert.DeepEqual(t, want, got[i])
	}
}
