package dagql

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// Two entries of one list recipe can be live with different values: one
// expired while still in use, or a session did not cover the other's
// session-resource requirements. Two sessions reading their lists position by
// position, taking turns, must each read their own list, not a mix of both.
func TestNthValueTwoEntriesOfOneListRecipeDoNotMixItems(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		split func(t *testing.T, ctx context.Context, c *Cache, srv *Server, list AnyResult)
	}{
		{
			name: "expired entry in use",
			split: func(t *testing.T, _ context.Context, c *Cache, _ *Server, list AnyResult) {
				c.egraphMu.Lock()
				list.cacheSharedResult().expiresAtUnix = time.Now().Add(-time.Hour).Unix()
				c.egraphMu.Unlock()
			},
		},
		{
			name: "session lacks requirements",
			split: func(t *testing.T, ctx context.Context, c *Cache, srv *Server, list AnyResult) {
				handle := cacheTestVolatileSessionResourceHandle("NTH_VALUE_TWO_ENTRIES")
				assert.NilError(t, c.BindSessionResource(ctx, "session-1", "session-1-client", handle, "a"))
				currentEntryTestRequireHandle(t, ctx, c, srv, "session-1", list, handle)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			baseCtx := cacheTestContext(t.Context())
			c, err := NewCache(baseCtx, "", nil, nil)
			assert.NilError(t, err)
			baseCtx = ContextWithCache(baseCtx, c)
			srv := cacheTestServer(t)

			orders := [][]string{{"a", "b", "c"}, {"c", "a", "b"}}
			var calls atomic.Int32
			keys := Func("keys", func(_ context.Context, _ *cacheTestObject, _ struct{}) (Array[String], error) {
				n := calls.Add(1)
				return NewStringArray(orders[n-1]...), nil
			})
			keys.Spec.TTL = 3600
			Fields[*cacheTestObject]{keys}.Install(srv)

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
			lists[0], err = parent.Select(sessions[0], srv, Selector{Field: "keys"})
			assert.NilError(t, err)
			tc.split(t, sessions[0], c, srv, lists[0])
			lists[1], err = parent.Select(sessions[1], srv, Selector{Field: "keys"})
			assert.NilError(t, err)
			assert.Equal(t, int32(2), calls.Load())
			assert.Assert(t, lists[0].cacheSharedResult() != lists[1].cacheSharedResult())

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
				assert.DeepEqual(t, orders[i], got[i])
			}
		})
	}
}

// Sessions that share one list entry still share its items.
func TestNthValueSharedListEntrySharesItems(t *testing.T) {
	t.Parallel()
	baseCtx := cacheTestContext(t.Context())
	c, err := NewCache(baseCtx, "", nil, nil)
	assert.NilError(t, err)
	baseCtx = ContextWithCache(baseCtx, c)
	srv := cacheTestServer(t)

	var calls atomic.Int32
	Fields[*cacheTestObject]{
		Func("keys", func(_ context.Context, _ *cacheTestObject, _ struct{}) (Array[String], error) {
			calls.Add(1)
			return NewStringArray("a", "b", "c"), nil
		}),
	}.Install(srv)

	parent := cacheTestDetachedObjectResult(&ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "parent",
	}, srv, &cacheTestObject{Value: 1})

	ctx1 := srvToContext(cacheTestSessionContext(baseCtx, "session-1"), srv)
	ctx2 := srvToContext(cacheTestSessionContext(baseCtx, "session-2"), srv)
	list1, err := parent.Select(ctx1, srv, Selector{Field: "keys"})
	assert.NilError(t, err)
	list2, err := parent.Select(ctx2, srv, Selector{Field: "keys"})
	assert.NilError(t, err)
	assert.Equal(t, int32(1), calls.Load())
	assert.Assert(t, list1.cacheSharedResult() == list2.cacheSharedResult())

	for nth := 1; nth <= 3; nth++ {
		item1, err := list1.NthValue(ctx1, nth)
		assert.NilError(t, err)
		item2, err := list2.NthValue(ctx2, nth)
		assert.NilError(t, err)
		assert.Assert(t, item1.cacheSharedResult().id != 0)
		assert.Equal(t, item1.cacheSharedResult().id, item2.cacheSharedResult().id)
		assert.Assert(t, item2.HitCache())
	}
}
