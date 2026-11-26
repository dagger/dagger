package dagql

import (
	"context"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/snapshots/config"
)

// keptTaughtBundle is a records-only export, from its own engine cache, of
// one result of the field "kept-taught", its frame carrying extras.
func keptTaughtBundle(t *testing.T, extras ...call.ExtraDigest) (ValueBundle, uint64, digest.Digest) {
	t.Helper()
	ctx, c, srv := transferTestCache(t)
	value := String("kept")
	frame := &ResultCall{Kind: ResultCallKindField, Field: "kept-taught", Type: NewResultCallType(value.Type()), ExtraDigests: extras}
	res, err := c.GetOrInitCall(ctx, "test-session", srv, &CallRequest{ResultCall: frame, IsPersistable: true}, func(context.Context) (AnyResult, error) {
		return NewResultForCall(value, frame)
	})
	require.NoError(t, err)
	recipe, err := res.cacheSharedResult().loadResultCall().deriveRecipeDigest(c)
	require.NoError(t, err)
	var bundle ValueBundle
	require.NoError(t, c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{res}}, config.RefConfig{}, func(_ context.Context, values *ExportedValues) error {
		bundle = values.Bundle
		return nil
	}))
	return bundle, rowNumber(res), recipe
}

// The Cloud counts the records it kept whose sender's holding it already
// had, and of those the ones whose identity taught the entry something all
// the same: what a held row in their place wouldn't have taught it. Here
// engine A stores the value. B's first record is kept, its holding made by
// that merge; its second finds the holding and teaches nothing. C's holding
// is there before its record, which carries a digest the entry lacks.
func TestCloudCountsKeptRecordsThatTeachTheirEntry(t *testing.T) {
	t.Parallel()
	cctx, cloud := storedPartTestCache(t, WithBlobStore())
	a, _, recipe := keptTaughtBundle(t)
	b, _, _ := keptTaughtBundle(t)
	c, cNumber, cRecipe := keptTaughtBundle(t, call.ExtraDigest{Digest: digest.FromString("kept-taught-extra"), Label: "test"})
	require.Equal(t, recipe, cRecipe, "an extra digest is no part of the recipe")

	reply, err := cloud.MergeValues(cctx, "engine-a", a)
	require.NoError(t, err)
	require.Equal(t, 0, reply.Rows.Kept)

	reply, err = cloud.MergeValues(cctx, "engine-b", b)
	require.NoError(t, err)
	require.Equal(t, MergeRowCounts{Kept: 1}, reply.Rows, "no holding before: B's holding is made by this merge")
	reply, err = cloud.MergeValues(cctx, "engine-b", b)
	require.NoError(t, err)
	require.Equal(t, MergeRowCounts{Kept: 1, KeptWithHolding: 1}, reply.Rows, "the same record again teaches nothing")

	_, err = cloud.AttachRemoteHolding(cctx, HolderKey{Cache: "engine-c", Number: cNumber}, RemoteHolding{Recipe: recipe, Field: "kept-taught", TypeName: "String"})
	require.NoError(t, err)
	reply, err = cloud.MergeValues(cctx, "engine-c", c)
	require.NoError(t, err)
	require.Equal(t, MergeRowCounts{Kept: 1, KeptWithHolding: 1, KeptTaught: 1}, reply.Rows, "C's extra digest is new to the entry")
}
