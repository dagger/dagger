package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An upload's parts are stored under one take of the graph lock: a writer
// waits for the readers that hold it once, not once a part. Here the parts
// of a root and of its dependency go in one call, and the lock is taken for
// writing once.
func TestStoredPartsTakeTheLockOnce(t *testing.T) {
	ctx, cloud, r, p := storedBundleTestCloud(t, "batch")
	takers := lockTakers(t, cloud)
	got, _, err := cloud.SetStoredParts(ctx, []StoredPartWrite{
		{Number: r, Part: testLiveOffer()},
		{Number: p, Part: testLiveOffer()},
	})
	require.NoError(t, err)
	require.Equal(t, []StoredPartResult{{Stored: true}, {Stored: true}}, got)
	require.Len(t, takers()["SetStoredParts"], 1, "one take for both parts")
	for _, n := range []uint64{r, p} {
		av, err := cloud.AvailablePart(n, testLiveOffer().Address, 0)
		require.NoError(t, err)
		require.Equal(t, PartStored, av.State, "entry %d's part stored", n)
	}
}

// Each part is admitted as SetStoredPart admits it alone, in order: one the
// cache can't store doesn't keep the others from being stored, and is
// answered with what SetStoredPart would have said of it.
func TestStoredPartsAdmitEachAsAlone(t *testing.T) {
	ctx, cloud, r, p := storedBundleTestCloud(t, "batch-each")
	missing := r + p + 1000
	got, _, err := cloud.SetStoredParts(ctx, []StoredPartWrite{
		{Number: r, Part: testLiveOffer()},
		{Number: missing, Part: testLiveOffer()},
		{Number: p, Part: testLiveOffer(), CopyExpiresAtUnix: 1},
	})
	require.NoError(t, err)
	require.Len(t, got, 3)
	require.True(t, got[0].Stored)
	require.Error(t, got[1].Err, "no such entry, as SetStoredPart says")
	require.False(t, got[1].Stored)
	require.False(t, got[2].Stored, "an expired copy of an unexpired value isn't stored, as alone")
	require.NoError(t, got[2].Err)
}
