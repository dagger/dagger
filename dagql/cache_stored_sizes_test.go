package dagql

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// storedSizesTestMeasure measures a bundle's roots as the service does: the
// values up to each root's ordinal, beyond those before it, each encoded.
func storedSizesTestMeasure(t *testing.T, bundle ValueBundle) []StoredSize {
	t.Helper()
	var sizes []StoredSize
	counted := 0
	for _, root := range bundle.Roots {
		var size StoredSize
		for ; counted < int(root.Ordinal); counted++ {
			data, err := json.Marshal(bundle.Values[counted])
			require.NoError(t, err)
			size.Records++
			size.Bytes += int64(len(data))
		}
		sizes = append(sizes, size)
	}
	return sizes
}

// storedSizesTestClose requires got to have want's records, and bytes within
// a few bytes a record of want's: a bundle numbers references by ordinal,
// StoredSizes by entry number.
func storedSizesTestClose(t *testing.T, want, got []StoredSize) {
	t.Helper()
	require.Len(t, got, len(want))
	for i := range want {
		require.Equal(t, want[i].Records, got[i].Records, "root %d records", i)
		require.InDelta(t, want[i].Bytes, got[i].Bytes, float64(8*want[i].Records), "root %d bytes", i)
	}
}

// StoredSizes measures what a stored bundle of the roots carries, root by
// root, without building it: P alone, then what R adds beyond it; the same
// roots in one bundle; and nothing for a root it lacks or one already
// measured.
func TestStoredSizesMeasureWhatTheBundleCarries(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")

	built, err := cloud.StoredBundle(ctx, []uint64{p, r}, nil)
	require.NoError(t, err)
	got, err := cloud.StoredSizes(ctx, []uint64{p, r}, nil)
	require.NoError(t, err)
	storedSizesTestClose(t, storedSizesTestMeasure(t, built.Bundle), got)
	require.Equal(t, 1, got[0].Records)
	require.Equal(t, 1, got[1].Records)

	built, err = cloud.StoredBundle(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	got, err = cloud.StoredSizes(ctx, []uint64{r, 12345, r}, nil)
	require.NoError(t, err)
	want := storedSizesTestMeasure(t, built.Bundle)
	storedSizesTestClose(t, append(want, StoredSize{}, StoredSize{}), got)
	require.Equal(t, 2, got[0].Records)

	kept := storedBundleTestEntry(cloud, r).storedTransfer.Load()
	require.NotNil(t, kept)
	require.NotNil(t, kept.size.Load(), "the record's size is kept with it")
}

// With a recipient that holds P, R's closure is R and P's held row, as the
// bundle StoredBundle builds with the same HeldBy.
func TestStoredSizesPriceHeldEntriesAsHeldRows(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	heldTestHold(t, ctx, cloud, "cache-b", 7, p, 0)
	held := &HeldBy{Cache: "cache-b", Now: time.Now().Unix()}

	built, err := cloud.StoredBundle(ctx, []uint64{r}, held)
	require.NoError(t, err)
	require.Equal(t, []uint64{p}, built.Held)
	got, err := cloud.StoredSizes(ctx, []uint64{r}, held)
	require.NoError(t, err)
	storedSizesTestClose(t, storedSizesTestMeasure(t, built.Bundle), got)

	full, err := cloud.StoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	require.Equal(t, full[0].Records, got[0].Records)
	require.Less(t, got[0].Bytes, full[0].Bytes, "a held row costs less than P's record")
}

// A stored part that names another entry adds a dependency to its entry
// without replacing the record: the entry's size is measured again.
func TestStoredSizesMeasureAgainAfterAPartAddsADependency(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	_, err := cloud.StoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)

	source, _ := mergeTestSource(t, "sizes-owner", "owner")
	reply, err := cloud.MergeValues(ctx, "cache-b", source)
	require.NoError(t, err)
	q := reply.Imported()[0].ResultID
	part := testLiveOffer()
	part.Owner.DependencyIDs = []uint64{q}
	stored, err := cloud.SetStoredPart(ctx, r, part, 0)
	require.NoError(t, err)
	require.True(t, stored)

	built, err := cloud.StoredBundle(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	got, err := cloud.StoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	storedSizesTestClose(t, storedSizesTestMeasure(t, built.Bundle), got)
	require.Equal(t, 3, got[0].Records, "R, P and the part's owner")

	entry := storedBundleTestEntry(cloud, r)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	kept, err := storedValueSizeLocked(entry)
	require.NoError(t, err)
	entry.storedTransfer.Load().size.Store(nil)
	fresh, err := storedValueSizeLocked(entry)
	require.NoError(t, err)
	require.Equal(t, fresh, kept, "the kept size is R's with its new dependency")
}
