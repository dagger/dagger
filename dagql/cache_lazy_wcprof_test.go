package dagql

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/wcprof"
)

// TestLazyOpCoversWhatItsWaitersWaitFor: what a lazy evaluation does after
// its body and before it releases its waiters (here a hold at the last step
// before the release) is inside its op, so a waiter's wait doesn't outlast
// the op it waits on.
func TestLazyOpCoversWhatItsWaitersWaitFor(t *testing.T) {
	const hold = 50 * time.Millisecond
	wcprof.EnsureRecorder()
	profCtx, testOp := wcprof.BeginOp(wcprof.ContextWithProfiling(t.Context()), wcprof.OpKindInternal, "test", wcprof.OpOpts{})
	defer testOp.End(wcprof.OutcomeOK)
	ctx := cacheTestContext(profCtx)
	c, err := NewCache(ctx, "", nil, nil)
	require.NoError(t, err)
	ctx = ContextWithCache(ctx, c)
	srv := cacheTestServer(t)
	frame := &ResultCall{
		Kind:  ResultCallKindField,
		Type:  NewResultCallType((&cacheTestObject{}).Type()),
		Field: "lazy-wait-test",
	}
	resAny, err := c.GetOrInitCall(ctx, cacheTestSessionID(t, ctx), srv, &CallRequest{ResultCall: frame},
		func(context.Context) (AnyResult, error) {
			return cacheTestObjectResultWithValue(t, srv, frame, &cacheTestObject{
				Value:    1,
				lazyEval: func(context.Context) error { return nil },
			}), nil
		})
	require.NoError(t, err)
	c.testAfterLazyEvalFinish = func(*lazyEvalAttempt) { time.Sleep(hold) }
	require.NoError(t, c.Evaluate(ctx, resAny.(ObjectResult[*cacheTestObject])))

	var buf bytes.Buffer
	require.NoError(t, wcprof.Active().WriteDump(&buf, false))
	header, events, err := wcprof.ReadDump(&buf)
	require.NoError(t, err)
	var lazy *wcprof.DumpEvent
	for i, ev := range events {
		if ev.Type == "op" && ev.OpID > testOp.ID() && ev.OpKind == wcprof.OpKindLazy.String() && header.Strings[ev.ClassID] == "Query.lazy-wait-test" {
			lazy = &events[i]
		}
	}
	require.NotNil(t, lazy, "no lazy op for the evaluation")
	require.GreaterOrEqual(t, lazy.EndNS-lazy.StartNS, int64(hold), "the lazy op ends before the work its waiters wait for")
	var waited bool
	for _, ev := range events {
		if ev.Type == "wait" && ev.TargetID == lazy.OpID {
			waited = true
			require.Less(t, ev.EndNS-lazy.EndNS, int64(hold/2), "a waiter's wait outlasts the lazy op it waits on")
		}
	}
	require.True(t, waited, "no wait on the lazy op")
}
