package dagql

import (
	"context"
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
