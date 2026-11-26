package dagql

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

// An engine that restored a set file holds a retention edge on each record
// the file retains, and a call's decision says the call itself kept nothing
// when it doesn't: a call that keeps nothing and hits such a record reports
// the record's edge in its result's state, and that it persisted nothing; a
// persistable call that hits it persisted it.
func TestAHitOnARestoredRecordSaysWhetherTheCallKeptIt(t *testing.T) {
	t.Parallel()
	actx, a, asrv := transferTestCache(t)
	value := &transferTestValue{Text: "restored"}
	made := persistedListTestResult(t, actx, a, asrv, "restored-record", value)
	bundle := exportTestBundle(t, actx, a, made)

	bctx, b, bsrv := transferTestCache(t)
	reply, err := b.RestoreValues(bctx, bundle, nil, "a set file")
	require.NoError(t, err)
	require.Len(t, reply.Roots, 1)
	require.True(t, reply.Roots[0].Retained, "the file retains its root")
	require.NoError(t, reply.Release(bctx))

	frame := &ResultCall{Kind: ResultCallKindField, Field: "restored-record", Type: NewResultCallType(value.Type()), FollowsFromInputs: true}
	hit := func(session string, persistable bool) (*CacheDecision, CacheResultState) {
		t.Helper()
		ev := NewCacheDecision()
		res, err := b.GetOrInitCall(bctx, session, bsrv, &CallRequest{ResultCall: frame, CacheEvidence: ev, IsPersistable: persistable}, func(context.Context) (AnyResult, error) {
			t.Fatal("the restored record serves the call")
			return nil, nil
		})
		require.NoError(t, err)
		require.Equal(t, CacheOutcomeHit, ev.Outcome)
		require.True(t, ev.HitRemote, "the hit is the restored record")
		state, ok := ev.ResultState(bctx, res)
		require.True(t, ok)
		return ev, state
	}

	ev, state := hit("session-keeps-nothing", false)
	require.True(t, state.Retained, "the entry holds the file's edge")
	require.False(t, ev.Persisted, "a call that keeps nothing persisted nothing")

	ev, state = hit("session-persistable", true)
	require.True(t, state.Retained)
	require.True(t, ev.Persisted, "a persistable hit keeps its result")
}
