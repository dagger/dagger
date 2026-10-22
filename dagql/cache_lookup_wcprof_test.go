package dagql

import (
	"context"
	"slices"
	"testing"

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
