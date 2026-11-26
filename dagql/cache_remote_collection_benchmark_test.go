package dagql

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// BenchmarkRemoteCollection measures the Cloud's collection under
// session-end churn over a live set: each operation is one session of two
// holdings, applied, ended and collected, as the service does when a session's
// end is read at its count. Each collection frees the classes it ends. It
// reports the collection's mean time and its longest call, which holds
// egraphMu throughout, and the class slots in use at the end, which stay at
// the live set's however long the churn runs.
func BenchmarkRemoteCollection(b *testing.B) {
	for _, liveCount := range []int{10_000, 175_000} {
		b.Run("live="+strconv.Itoa(liveCount), func(b *testing.B) {
			ctx := context.Background()
			cloud, err := NewCache(ctx, "", nil, nil)
			require.NoError(b, err)
			b.Cleanup(func() { require.NoError(b, cloud.CloseDiscardingPersistence()) })
			holding := func(field string, input int) RemoteHolding {
				h := holdingOf(field)
				h.Request = testDigest(field + "-alias")
				h.ContentDigest = testDigest(field + "-content")
				h.Term.Inputs = append(h.Term.Inputs, testDigest(fmt.Sprintf("live-%d", input)))
				return h
			}
			attach := func(key HolderKey, session string, h RemoteHolding) {
				_, err := cloud.AttachRemoteHolding(ctx, key, h)
				require.NoError(b, err)
				require.NoError(b, errOf(cloud.AddRemoteHold(ctx, key, session)))
			}
			for i := range liveCount {
				// Each live entry's term takes the one before it as its input.
				attach(HolderKey{"cache-live", uint64(i + 1)}, "live", holding(fmt.Sprintf("live-%d", i), (i+liveCount-1)%liveCount))
			}
			_, _, err = cloud.CollectRemoteHoldings(ctx, nil, nil)
			require.NoError(b, err)
			cloud.egraphMu.RLock()
			liveSlots := cloud.eqClassSlotsLocked()
			cloud.egraphMu.RUnlock()

			var total, longest time.Duration
			b.ResetTimer()
			for i := range b.N {
				session := "churn-" + strconv.Itoa(i)
				for j := range 2 {
					field := fmt.Sprintf("churn-%d-%d", i, j)
					attach(HolderKey{"cache-churn", uint64(2*i + j + 1)}, session, holding(field, (i*2+j)%liveCount))
				}
				candidates := cloud.ReleaseRemoteSession(ctx, "cache-churn", session).Candidates
				start := time.Now()
				collected, _, err := cloud.CollectRemoteHoldings(ctx, candidates, nil)
				took := time.Since(start)
				require.NoError(b, err)
				require.Len(b, collected, 2)
				total += took
				if took > longest {
					longest = took
				}
			}
			b.StopTimer()
			cloud.egraphMu.RLock()
			slots := cloud.eqClassSlotsLocked()
			cloud.egraphMu.RUnlock()
			b.ReportMetric(float64(total.Nanoseconds())/float64(b.N), "collect-ns/collection")
			b.ReportMetric(float64(longest.Microseconds())/1000, "longest-collect-ms")
			b.ReportMetric(float64(slots), "class-slots")
			require.Equal(b, liveSlots, slots, "the churn leaves the live set's slots")
		})
	}
}
