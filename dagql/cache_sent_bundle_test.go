package dagql

import (
	"context"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// What the Cloud keeps of a bundle it sent until the engine's reply comes
// (ValueBundle.Sent) is a fraction of the bundle: each row's Cloud entry and
// the run its record named, where the bundle keeps each row's record, parts
// and dependencies, and its records' compressed bodies, which a
// replacement or a prune would otherwise let go of.
//
// Not parallel: it reads the process's live heap, which tests running
// beside it would change.
func TestSentBundleKeepsAFractionOfTheBundle(t *testing.T) {
	const records = 500
	bundle := storedBodyTestExportOf(t, records)
	ctx := cacheTestContext(context.Background())
	cloud, err := NewCache(ctx, "", nil, nil, WithBlobStore())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cloud.CloseDiscardingPersistence()) })
	roots := storedPersistenceTestMerge(t, ctx, cloud, bundle)
	storedBodyTestUse(t, ctx, cloud, roots)

	live := func() int64 {
		runtime.GC()
		runtime.GC()
		var ms runtime.MemStats
		runtime.ReadMemStats(&ms)
		return int64(ms.HeapAlloc)
	}
	before := live()
	built := storedBodyTestUse(t, ctx, cloud, roots)
	withBundle := live() - before
	sent := built.Sent()
	built = ValueBundle{}
	withSent := live() - before
	runtime.KeepAlive(built)
	require.Equal(t, records, sent.Len())
	t.Logf("%d records: the built bundle %d bytes live, what is kept of it once sent %d", records, withBundle, withSent)
	require.Less(t, withSent*10, withBundle, "kept once sent, against the bundle")
	require.Less(t, withSent, int64(64*records), "kept once sent: a few words a row")
	runtime.KeepAlive(sent)
}

// A sent bundle names each row's Cloud entry by its ordinal, whatever the
// rows' order, and no entry for an ordinal the bundle didn't send.
func TestSentBundleNamesEachRowByItsOrdinal(t *testing.T) {
	t.Parallel()
	for name, values := range map[string][]TransferredValue{
		"in order":     {{Ordinal: 1, SenderNumber: 10}, {Ordinal: 2, SenderNumber: 20}, {Ordinal: 3, SenderNumber: 30}},
		"out of order": {{Ordinal: 2, SenderNumber: 20}, {Ordinal: 3, SenderNumber: 30}, {Ordinal: 1, SenderNumber: 10}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			sent := ValueBundle{Values: values}.Sent()
			require.Equal(t, 3, sent.Len())
			for _, ordinal := range []TransferOrdinal{1, 2, 3} {
				entry, ok := sent.SenderNumber(ordinal)
				require.True(t, ok, "ordinal %d", ordinal)
				require.Equal(t, uint64(ordinal)*10, entry, "ordinal %d", ordinal)
			}
			for _, ordinal := range []TransferOrdinal{0, 4} {
				_, ok := sent.SenderNumber(ordinal)
				require.False(t, ok, "ordinal %d", ordinal)
			}
		})
	}
}
