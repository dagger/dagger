package dagql

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// routeTestCloud has an engine compute the field's result and export it to
// a Cloud, and returns the bundle the Cloud sends a fresh engine for it.
func routeTestCloud(t *testing.T, field string) ValueBundle {
	t.Helper()
	actx, a, asrv := transferTestCache(t)
	res := persistedListTestResult(t, actx, a, asrv, field, &transferTestValue{Text: field})
	exported := exportTestBundle(t, actx, a, res)
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", exported)
	require.NoError(t, err)
	require.Len(t, reply.Roots, 1)
	got, err := cloud.StoredBundle(ctx, []uint64{reply.Roots[0].Number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	bundle, err := cloneValueBundle(got.Bundle)
	require.NoError(t, err)
	return bundle
}

// routeTestHit looks the field up on the engine with its decision armed,
// and returns the decision of a lookup that hit.
func routeTestHit(t *testing.T, ctx context.Context, c *Cache, srv *Server, field string) *CacheDecision {
	t.Helper()
	frame := &ResultCall{Kind: ResultCallKindField, Field: field, Type: NewResultCallType((&transferTestValue{}).Type())}
	req := &CallRequest{ResultCall: frame, IsPersistable: true, CacheEvidence: NewCacheDecision()}
	_, err := c.GetOrInitCall(ctx, "test-session", srv, req, func(context.Context) (AnyResult, error) {
		t.Fatalf("%s ran: its merged record didn't serve it", field)
		return nil, nil
	})
	require.NoError(t, err)
	require.Equal(t, CacheOutcomeHit, req.CacheEvidence.Outcome)
	return req.CacheEvidence
}

// A first use of a merged record is counted by the route its merge brought
// it by, beside what each route merged; a merge counts as used once any of
// its records is first used. The hit that first uses a record says its
// route and its merge, and a later hit of it no first use.
func TestFirstUsesCountByRoute(t *testing.T) {
	t.Parallel()
	ctx, engine, srv := transferTestCache(t)
	set := routeTestCloud(t, "route-set-file")
	placed := routeTestCloud(t, "route-placed")
	unused := routeTestCloud(t, "route-placed-unused")
	looked := routeTestCloud(t, "route-lookup")

	reply, err := engine.RestoreValues(ctx, set, nil, "check.whole")
	require.NoError(t, err)
	if reply.Release != nil {
		require.NoError(t, reply.Release(ctx))
	}
	_, err = engine.MergeServiceValues(ctx, placed, MergeSource{Route: MergeRoutePlacement, Merge: 7})
	require.NoError(t, err)
	_, err = engine.MergeServiceValues(ctx, unused, MergeSource{Route: MergeRoutePlacement, Merge: 8})
	require.NoError(t, err)
	_, err = engine.MergeServiceValues(ctx, looked, MergeSource{Route: MergeRouteLookup, Merge: 9})
	require.NoError(t, err)
	// The same record sent again is held already: carried, not merged.
	_, err = engine.MergeServiceValues(ctx, placed, MergeSource{Route: MergeRoutePlacement, Merge: 10})
	require.NoError(t, err)
	require.Empty(t, engine.FirstUse().Paths, "merging prepares nothing")

	fromFile := routeTestHit(t, ctx, engine, srv, "route-set-file")
	require.True(t, fromFile.HitRemote)
	require.Equal(t, MergeRouteSetFile, fromFile.HitMergedRoute)
	require.Equal(t, "check.whole", fromFile.HitMergedFile)
	require.Zero(t, fromFile.HitMerge)
	require.True(t, fromFile.HitFirstUse)

	fromLookup := routeTestHit(t, ctx, engine, srv, "route-lookup")
	require.Equal(t, MergeRouteLookup, fromLookup.HitMergedRoute)
	require.Equal(t, uint64(9), fromLookup.HitMerge)
	require.Empty(t, fromLookup.HitMergedFile)
	require.True(t, fromLookup.HitFirstUse)

	fromPlacement := routeTestHit(t, ctx, engine, srv, "route-placed")
	require.Equal(t, MergeRoutePlacement, fromPlacement.HitMergedRoute)
	require.Equal(t, uint64(7), fromPlacement.HitMerge, "the merge that installed it, not the one that carried it again")
	require.True(t, fromPlacement.HitFirstUse)
	again := routeTestHit(t, ctx, engine, srv, "route-placed")
	require.Equal(t, MergeRoutePlacement, again.HitMergedRoute)
	require.False(t, again.HitFirstUse, "the record was prepared by its first hit")

	stats := engine.FirstUse()
	routes := stats.Routes
	require.Len(t, routes, 3, "%+v", routes)
	file, placement, lookup := routes["set_file"], routes["placement"], routes["lookup"]
	require.Equal(t, int64(1), file.Merges)
	require.Equal(t, int64(1), file.Sent)
	require.Equal(t, int64(1), file.Merged)
	require.Equal(t, int64(1), file.FirstUses)
	require.Equal(t, int64(1), file.Used)
	require.Positive(t, file.Total)

	require.Equal(t, int64(3), placement.Merges)
	require.Equal(t, int64(3), placement.Sent)
	require.Equal(t, int64(2), placement.Merged, "the record sent again was held already")
	require.Equal(t, int64(1), placement.FirstUses)
	require.Equal(t, int64(1), placement.Used, "one of the three merges brought anything used")

	require.Equal(t, int64(1), lookup.Merges)
	require.Equal(t, int64(1), lookup.Merged)
	require.Equal(t, int64(1), lookup.FirstUses)
	require.Equal(t, int64(1), lookup.Used)

	require.Equal(t, stats.Prepared+stats.Refused, file.FirstUses+placement.FirstUses+lookup.FirstUses, "every first use came by a route")
	require.Equal(t, stats.Total, file.Total+placement.Total+lookup.Total)
}
