package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// A hit's span says what its own time after its lookup is made of. A record
// from the Cloud is loaded at its first use: its payload, and that of the
// result it references, decoded. While the load resolves that reference, a
// writer holds the graph lock as part work; then, as the span ends, the
// service holds it while the hit's state is read. The hit's carrier says when
// the load ended and when the cache handed the hit back, that its load
// decoded both entries, and that its work waited for the graph lock behind
// part work and behind the service's hold. A second hit on the same value
// decodes nothing and waits for nothing.
func TestHitSaysWhatItsLoadDecodedAndWaitedFor(t *testing.T) {
	t.Parallel()
	set := firstUseTestCloud(t)

	ctx, engine, srv := transferTestCache(t)
	reply, err := engine.MergeValues(ctx, cloudCacheID, set.bundle)
	require.NoError(t, err)
	root := firstUseTestEntry(t, set.bundle, reply, set.bad)

	// A writer that holds the graph lock as work until another acquisition
	// waits for it, and a little longer, then lets it go.
	const hold = 10 * time.Millisecond
	holdGraph := func(work cacheWork) (held, released chan struct{}) {
		held, released = make(chan struct{}), make(chan struct{})
		go func() {
			defer close(released)
			engine.lockGraphFor(context.Background(), work)
			close(held)
			for deadline := time.Now().Add(time.Minute); !engine.egraphMu.Waiting(); time.Sleep(time.Millisecond) {
				if time.Now().After(deadline) {
					engine.egraphMu.Unlock()
					t.Error("nothing waited for the graph lock")
					return
				}
			}
			time.Sleep(hold)
			engine.egraphMu.Unlock()
		}()
		return held, released
	}
	var partReleased chan struct{}
	transferDecodeHooks.Store(root, func(ctx context.Context, dec *PersistDecodeContext, value *transferTestValue) error {
		held, released := holdGraph(workPart)
		<-held
		partReleased = released
		child, err := dec.ResultRef(ctx, value.Child)
		require.NoError(t, err)
		require.Equal(t, "dep", child.Unwrap().(*transferTestValue).Text)
		return nil
	})
	t.Cleanup(func() { transferDecodeHooks.Delete(root) })

	call := func() (*CacheDecision, AnyResult) {
		frame := &ResultCall{Kind: ResultCallKindField, Field: "fu-bad", Type: NewResultCallType((&transferTestValue{}).Type())}
		req := &CallRequest{ResultCall: frame, IsPersistable: true, CacheEvidence: NewCacheDecision()}
		res, err := engine.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
			t.Fatal("the call hits")
			return nil, nil
		})
		require.NoError(t, err)
		require.Equal(t, CacheOutcomeHit, req.CacheEvidence.Outcome)
		return req.CacheEvidence, res
	}

	ev, res := call()
	<-partReleased
	require.Equal(t, int64(2), ev.HitLoad().Decoded, "the hit's own value and the result its payload references")
	require.NotZero(t, ev.LookupAtUnixNano)
	require.GreaterOrEqual(t, ev.HitLoadedAtUnixNano, ev.LookupAtUnixNano+int64(hold)/2, "the load waited for the part work")
	require.GreaterOrEqual(t, ev.HitServedAtUnixNano, ev.HitLoadedAtUnixNano)
	require.Greater(t, ev.HitLoad().Waits["graph.part"], time.Duration(0), "its reference's resolution waited for the part work's hold")

	held, released := holdGraph(workHold)
	<-held
	_, ok := ev.ResultState(ctx, res)
	require.True(t, ok)
	<-released
	require.Greater(t, ev.HitLoad().Waits["graph.hold"], time.Duration(0), "the state's read at the span's end waited for the service's hold")

	again, _ := call()
	require.Zero(t, again.HitLoad().Decoded, "nothing is left to decode")
	require.Empty(t, again.HitLoad().Waits)
	require.GreaterOrEqual(t, again.HitServedAtUnixNano, again.HitLoadedAtUnixNano)
	require.GreaterOrEqual(t, again.HitLoadedAtUnixNano, again.LookupAtUnixNano)

	// A value published here, whose dependency attachment ended with its
	// publication, is no wait either when it hits again.
	_, _, hit := firstUseTestCall(t, ctx, engine, srv, "fu-local")
	require.False(t, hit, "the first call runs")
	frame := &ResultCall{Kind: ResultCallKindField, Field: "fu-local", Type: NewResultCallType((&transferTestValue{}).Type())}
	req := &CallRequest{ResultCall: frame, IsPersistable: true, CacheEvidence: NewCacheDecision()}
	_, err = engine.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		t.Fatal("the call hits")
		return nil, nil
	})
	require.NoError(t, err)
	require.Equal(t, CacheOutcomeHit, req.CacheEvidence.Outcome)
	require.Empty(t, req.CacheEvidence.HitLoad().Waits, "an attachment already ended is no wait")
	require.NotZero(t, req.CacheEvidence.HitServedAtUnixNano)
}
