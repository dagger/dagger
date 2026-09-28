package dagql

import (
	"context"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// BenchmarkRemoteCollectionCompaction measures the Cloud's collection under
// session-end churn over a live set: each operation is one session of two
// holdings, applied, ended and collected, as the service does when a session's
// end is read at its count. The checks' clock moves at the full-scale replay's
// rate of session ends, 88,000 in 164.7 s. It reports the class checks per
// collection and per 10 s of that clock, the compactions, the collection's
// mean time and its longest call, which holds egraphMu throughout.
func BenchmarkRemoteCollectionCompaction(b *testing.B) {
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
				require.NoError(b, cloud.AddRemoteHold(ctx, key, session))
			}
			for i := range liveCount {
				// Each live entry's term takes the one before it as its input.
				attach(HolderKey{"cache-live", uint64(i + 1)}, "live", holding(fmt.Sprintf("live-%d", i), (i+liveCount-1)%liveCount))
			}
			advance := testEqClassClock(cloud)
			const perCollection = 164_700 * time.Millisecond / 88_000
			_, err = cloud.CollectRemoteHoldings(ctx, nil)
			require.NoError(b, err)
			cloud.egraphMu.RLock()
			checks := cloud.eqClassChecks
			cloud.egraphMu.RUnlock()

			var total, longest time.Duration
			compactions := 0
			b.ResetTimer()
			for i := range b.N {
				advance(perCollection)
				session := "churn-" + strconv.Itoa(i)
				for j := range 2 {
					field := fmt.Sprintf("churn-%d-%d", i, j)
					attach(HolderKey{"cache-churn", uint64(2*i + j + 1)}, session, holding(field, (i*2+j)%liveCount))
				}
				candidates := cloud.ReleaseRemoteSession(ctx, "cache-churn", session)
				cloud.egraphMu.RLock()
				before := cloud.eqClassSlotsLocked()
				cloud.egraphMu.RUnlock()
				start := time.Now()
				collected, err := cloud.CollectRemoteHoldings(ctx, candidates)
				took := time.Since(start)
				require.NoError(b, err)
				require.Len(b, collected, 2)
				total += took
				if took > longest {
					longest = took
				}
				cloud.egraphMu.RLock()
				if cloud.eqClassSlotsLocked() < before {
					compactions++
				}
				cloud.egraphMu.RUnlock()
			}
			b.StopTimer()
			cloud.egraphMu.RLock()
			checks = cloud.eqClassChecks - checks
			cloud.egraphMu.RUnlock()
			b.ReportMetric(float64(checks)/float64(b.N), "checks/collection")
			b.ReportMetric(float64(checks)/(float64(b.N)*perCollection.Seconds()/10), "checks/10s")
			b.ReportMetric(float64(compactions), "compactions")
			b.ReportMetric(float64(total.Nanoseconds())/float64(b.N), "collect-ns/collection")
			b.ReportMetric(float64(longest.Microseconds())/1000, "longest-collect-ms")
		})
	}
}
