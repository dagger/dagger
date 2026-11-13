package dagql

import (
	"fmt"
	"runtime"
	"testing"

	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// A call row whose identity the cache already holds teaches it nothing, so
// it costs what that identity holds, not what the cache built on it: teaching
// base again, when 2000 other calls take base as their input, allocates no
// more than when 10 do. The cost is counted in allocated bytes, which don't
// vary from run to run; the count is process-wide, so the test doesn't run in
// parallel.
func TestKnownRemoteIdentityCostIsIndependentOfItsUses(t *testing.T) {
	ctx := t.Context()
	bytes := func(uses int) uint64 {
		cloud := newCloudCache(t)
		a := newCloudApplier(t, cloud)
		base := holdingOf("base")
		base.ContentDigest = testDigest("base-content")
		a.call(callRow{key: HolderKey{"cache-a", 1}, session: "a", holding: base})
		for i := range uses {
			use := fmt.Sprint("use-", i)
			a.call(callRow{key: HolderKey{"cache-a", uint64(i + 2)}, session: "a", holding: RemoteHolding{
				Recipe: testDigest(use),
				Field:  "use",
				Term:   &RemoteTerm{Self: testDigest(use + "-self"), Inputs: []digest.Digest{base.Recipe}},
			}})
		}
		cloud.egraphMu.RLock()
		entry, _ := cloud.holdingLocked(HolderKey{"cache-a", 1})
		cloud.egraphMu.RUnlock()
		require.NotNil(t, entry)
		teach := func() {
			cloud.egraphMu.Lock()
			cloud.teachRemoteIdentityLocked(ctx, entry, base.Field, base.Recipe, base.Request, base.ContentDigest, base.Term)
			cloud.egraphMu.Unlock()
		}
		teach()
		return allocatedBytesPerRun(50, teach)
	}
	few, many := bytes(10), bytes(2000)
	t.Logf("a known row's teach allocates %d bytes with 10 uses of its class, %d with 2000", few, many)
	require.LessOrEqual(t, many, few, "a known row's teach grew with the calls that take its result as an input")
}

// allocatedBytesPerRun is testing.AllocsPerRun in bytes.
func allocatedBytesPerRun(runs int, f func()) uint64 {
	defer runtime.GOMAXPROCS(runtime.GOMAXPROCS(1))
	f()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	for range runs {
		f()
	}
	runtime.ReadMemStats(&after)
	return (after.TotalAlloc - before.TotalAlloc) / uint64(runs)
}
