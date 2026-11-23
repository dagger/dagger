package dagql

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// unchangedRowsTestBuild builds a stored bundle of the roots, for held.
func unchangedRowsTestBuild(t *testing.T, ctx context.Context, cloud *Cache, held *HeldBy, roots ...uint64) ValueBundle {
	t.Helper()
	built, err := cloud.StoredBundle(ctx, roots, time.Now().Unix(), held)
	require.NoError(t, err)
	return built.Bundle
}

// A bundle the Cloud builds carries the cache's start count, and each value
// it sends in full its entry's change stamp, so that an engine can tell the
// same record at the same stamp from a changed one.
func TestStoredBundleCarriesTheStampOfEachRecord(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, p := storedBundleTestCloud(t, "stamped")
	bundle := unchangedRowsTestBuild(t, ctx, cloud, nil, r)
	require.Empty(t, bundle.SenderCache, "a cache with no identity names none")
	stamps := map[uint64]uint64{}
	for _, value := range bundle.Values {
		stamps[value.SenderNumber] = value.SenderStamp
	}
	cloud.egraphMu.RLock()
	want := map[uint64]uint64{r: cloud.resultsByID[sharedResultID(r)].changedAt, p: cloud.resultsByID[sharedResultID(p)].changedAt}
	cloud.egraphMu.RUnlock()
	require.Equal(t, want, stamps)
	stored, _, err := cloud.SetStoredPart(ctx, r, testLiveOffer(), 0)
	require.NoError(t, err)
	require.True(t, stored)
	again := unchangedRowsTestBuild(t, ctx, cloud, nil, r)
	for _, value := range again.Values {
		if value.SenderNumber == r {
			require.Greater(t, value.SenderStamp, stamps[r], "a part stored on the entry moves its stamp")
		} else {
			require.Equal(t, stamps[value.SenderNumber], value.SenderStamp, "its dependency is unchanged")
		}
	}
}

// withoutChangeStamps returns values with their change stamps cleared, and
// bundleWithoutChangeStamps the bundle with them and its start count
// cleared: stamps count one start's writes of the cache that built the
// bundle, so a test comparing what two starts or two caches send leaves
// them out.
func withoutChangeStamps(values []TransferredValue) []TransferredValue {
	out := make([]TransferredValue, len(values))
	for i, value := range values {
		value.SenderStamp = 0
		out[i] = value
	}
	return out
}

func bundleWithoutChangeStamps(bundle ValueBundle) ValueBundle {
	bundle.Values = withoutChangeStamps(bundle.Values)
	bundle.SenderCache = ""
	return bundle
}

// A stored size counts a value's change stamp as the stamp stands, without
// measuring the value again: a call kept on its entry moves the stamp, and
// leaves the record, dependencies, replacement count and expiry its measure
// depends on as they were. The Cloud prices bundles under its work lock, and
// calls are kept on its entries all through a fill.
func TestStoredSizesFollowAStampWithoutMeasuringAgain(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	// A body the worker compressed between the two measures would be measured
	// again, as it should.
	storedTestHoldWhole(cloud)
	test := newCallRecordTest(t, ctx, cloud)
	before := unchangedRowsTestBuild(t, ctx, cloud, nil, test.number)
	_, err := cloud.StoredSizes(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	encodes := cloud.StoredSizeEncodes()
	test.attach(t, ctx, test.span(t, true, 0))
	after := unchangedRowsTestBuild(t, ctx, cloud, nil, test.number)
	require.Greater(t, after.Values[0].SenderStamp, before.Values[0].SenderStamp, "the kept call moved the stamp")
	sizes, err := cloud.StoredSizes(ctx, []uint64{test.number}, time.Now().Unix(), nil)
	require.NoError(t, err)
	require.Equal(t, encodes, cloud.StoredSizeEncodes(), "nothing is measured again")
	// The size counts the stamp as it stands: within the few bytes a record
	// that references differ by, where the stamp's field alone is more.
	with, err := json.Marshal(after.Values[0])
	require.NoError(t, err)
	without := after.Values[0]
	without.SenderStamp = 0
	bare, err := json.Marshal(without)
	require.NoError(t, err)
	require.Greater(t, len(with)-len(bare), 8)
	storedSizesTestClose(t, storedSizesTestMeasure(t, after), sizes)
}
