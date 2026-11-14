package dagql

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Pricing and bundling a root again, with nothing changed, groups the calls
// kept on its entries by term only once: the grouping is kept with the entry.
func TestKeptCallTermsAreGroupedOnce(t *testing.T) {
	t.Parallel()
	test := newReceiverCallTest(t)
	var built atomic.Int32
	test.cloud.testCallTermsBuilt = func() { built.Add(1) }
	now := time.Now().Unix()
	_, err := test.cloud.StoredSizes(test.ctx, []uint64{test.number}, now, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, built.Load())
	_, err = test.cloud.StoredSizes(test.ctx, []uint64{test.number}, now, nil)
	require.NoError(t, err)
	built1, err := test.cloud.StoredBundle(test.ctx, []uint64{test.number}, now, nil)
	require.NoError(t, err)
	require.Len(t, built1.Bundle.Calls, 1)
	require.EqualValues(t, 1, built.Load(), "nothing changed since the first grouping")
}
