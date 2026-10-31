package dagql

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/opencontainers/go-digest"
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
	kept, ok := storedValueMeasuredLocked(entry)
	miss := storedSizeMissLocked(entry)
	cloud.egraphMu.RUnlock()
	require.True(t, ok, "R's size is kept for its current state")
	entry.storedTransfer.Load().size.Store(nil)
	fresh, err := cloud.measureStoredValue(miss)
	require.NoError(t, err)
	require.Equal(t, fresh.size.bytes, kept, "the kept size is R's with its new dependency")
}

// StoredSizes normalizes and encodes an unmeasured value with the graph lock
// free: a measure never holds egraphMu, and each first measurement counts.
func TestStoredSizesMeasureOutsideTheLock(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	var free, held int
	cloud.testStoredSizeMeasure = func() {
		if cloud.egraphMu.TryLock() {
			cloud.egraphMu.Unlock()
			free++
		} else {
			held++
		}
	}
	before := cloud.StoredSizeEncodes()
	got, err := cloud.StoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	require.Equal(t, 2, got[0].Records)
	require.Equal(t, 2, free, "R and P are measured with the lock free")
	require.Zero(t, held)
	require.Equal(t, before+2, cloud.StoredSizeEncodes())

	again, err := cloud.StoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	require.Equal(t, got, again)
	require.Equal(t, 2, free, "measured sizes are kept")
	require.Equal(t, before+2, cloud.StoredSizeEncodes())
}

// MeasuredStoredSizes never measures: a root whose closure has an unmeasured
// value is answered unmeasured, with nothing normalized or encoded, and
// later roots are measured as if it weren't there.
func TestMeasuredStoredSizesNeverMeasure(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "root")
	before := cloud.StoredSizeEncodes()
	got, err := cloud.MeasuredStoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	require.Equal(t, []StoredSize{{Unmeasured: true}}, got)
	require.Equal(t, before, cloud.StoredSizeEncodes(), "nothing encoded")
	require.Nil(t, storedBundleTestEntry(cloud, r).storedTransfer.Load(), "nothing normalized")

	alone, err := cloud.StoredSizes(ctx, []uint64{p}, nil)
	require.NoError(t, err)
	encodes := cloud.StoredSizeEncodes()
	got, err = cloud.MeasuredStoredSizes(ctx, []uint64{r, p}, nil)
	require.NoError(t, err)
	require.Equal(t, []StoredSize{{Unmeasured: true}, alone[0]}, got, "P is measured beyond the measured roots before it")
	require.Equal(t, encodes, cloud.StoredSizeEncodes())

	measured, err := cloud.StoredSizes(ctx, []uint64{p, r}, nil)
	require.NoError(t, err)
	got, err = cloud.MeasuredStoredSizes(ctx, []uint64{p, r}, nil)
	require.NoError(t, err)
	require.Equal(t, measured, got, "once measured, the sizes are StoredSizes'")
}

// A measure that ends after the cache dropped the value it measured keeps
// nothing: the dropped record isn't brought back onto the entry.
func TestStoredSizesKeepNothingOfADroppedValue(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	entry := storedBundleTestEntry(cloud, r)
	dropped := false
	cloud.testStoredSizeMeasure = func() {
		if dropped {
			return
		}
		dropped = true
		cloud.egraphMu.Lock()
		defer cloud.egraphMu.Unlock()
		_, err := cloud.dropValueLocked(ctx, entry)
		require.NoError(t, err)
	}
	_, err := cloud.StoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	require.True(t, dropped)
	require.Nil(t, entry.storedTransfer.Load(), "the dropped value's record isn't kept")
}

// A held row's size is computed, not encoded, and is its encoding's length.
func TestHeldRowSizeIsItsEncodingLength(t *testing.T) {
	t.Parallel()
	for _, number := range []uint64{1, 9, 10, 12345, 1<<64 - 1} {
		for _, replacements := range []uint64{0, 1, 77, 1<<64 - 1} {
			for _, expires := range []int64{0, 1, -5, 1_760_000_000, -1 << 63} {
				for _, recipe := range []digest.Digest{"xxh3:2d06800538d394c2", heldRowSizeDigest, digest.FromString("x")} {
					data, err := json.Marshal(TransferredValue{Ordinal: TransferOrdinal(number), SenderNumber: number, SenderReplacements: replacements, Record: PersistedRecord{ResultID: number}, ExpiresAtUnix: expires, Held: recipe})
					require.NoError(t, err)
					require.Equal(t, int64(len(data)), heldRowSize(number, replacements, expires, recipe), "number %d, replacements %d, expiry %d, recipe %s", number, replacements, expires, recipe)
				}
			}
		}
	}
}

// A build and a measure that normalize the same record at once leave its one
// kept record measured: the build, publishing after the measure, keeps the
// measured record rather than replacing it with its own.
func TestStoredSizesSurviveABuildNormalizingAtOnce(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	normalized, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	cloud.testStoredTransferNormalized = func() {
		once.Do(func() {
			close(normalized)
			<-release
		})
	}
	built := make(chan error, 1)
	go func() {
		_, err := cloud.StoredBundle(ctx, []uint64{r}, nil)
		built <- err
	}()
	<-normalized
	_, err := cloud.StoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	close(release)
	require.NoError(t, <-built)

	encodes := cloud.StoredSizeEncodes()
	got, err := cloud.MeasuredStoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	require.False(t, got[0].Unmeasured, "the build kept the measured record")
	require.Equal(t, 2, got[0].Records)
	require.Equal(t, encodes, cloud.StoredSizeEncodes())
}

// A measure of a value whose dependencies changed while it measured keeps
// nothing, so it can't replace the size measured for the new dependencies.
func TestStoredSizesKeepNoMeasureOfAnOlderState(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	source, _ := mergeTestSource(t, "sizes-later-owner", "owner")
	reply, err := cloud.MergeValues(ctx, "cache-b", source)
	require.NoError(t, err)
	q := reply.Imported()[0].ResultID
	_, err = cloud.StoredSizes(ctx, []uint64{q}, nil)
	require.NoError(t, err)

	// The newer measure calls the hook too, so it is guarded before it nests.
	changed := false
	cloud.testStoredSizeMeasure = func() {
		if changed {
			return
		}
		changed = true
		part := testLiveOffer()
		part.Owner.DependencyIDs = []uint64{q}
		stored, err := cloud.SetStoredPart(ctx, r, part, 0)
		require.NoError(t, err)
		require.True(t, stored)
		_, err = cloud.StoredSizes(ctx, []uint64{r}, nil)
		require.NoError(t, err)
	}
	_, err = cloud.StoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	require.True(t, changed)

	encodes := cloud.StoredSizeEncodes()
	got, err := cloud.MeasuredStoredSizes(ctx, []uint64{r}, nil)
	require.NoError(t, err)
	require.False(t, got[0].Unmeasured, "the size measured for the new dependencies stays")
	require.Equal(t, 3, got[0].Records, "R, P and the part's owner")
	require.Equal(t, encodes, cloud.StoredSizeEncodes())
}
