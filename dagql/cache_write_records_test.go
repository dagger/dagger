package dagql

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// A write's change names the holdings it changed, and no other write's. A
// prune drops its edges each in a hold of its own: a write of another
// engine's holding between two of its drops hands back its own holding
// alone, and the prune's report names the holdings whose executed mark its
// drop cleared.
func TestAWritesChangeNamesOnlyItsOwnHoldings(t *testing.T) {
	t.Parallel()
	ctx, cloud, r, _ := storedBundleTestCloud(t, "root")
	info, ok := cloud.EntryInfo(r)
	require.True(t, ok)
	sender := HolderKey{Cache: "cache-a", Number: bundleSenderNumberForTest(t, cloud, r)}
	_, err := cloud.AttachRemoteHolding(ctx, sender, RemoteHolding{Recipe: info.Recipes[0], Field: "bundle-root", TypeName: "transferTestValue", Executed: true})
	require.NoError(t, err)
	require.True(t, storedHoldingsTestInfo(t, cloud, sender).Executed)

	between := HolderKey{Cache: "cache-between", Number: 1}
	var (
		once          sync.Once
		betweenChange RemoteChange
		betweenErr    error
	)
	cloud.testAfterRetentionDrop = func(sharedResultID) {
		once.Do(func() {
			betweenChange, betweenErr = cloud.AttachRemoteHolding(ctx, between, holdingOf("between-1"))
		})
	}
	report, err := cloud.PruneMetadataEstimate(ctx, 2, 1)
	require.NoError(t, err)
	require.Positive(t, report.DroppedValues)
	require.NoError(t, betweenErr)
	require.False(t, storedHoldingsTestInfo(t, cloud, sender).Executed, "the drop cleared the sender's mark")

	require.Equal(t, []HolderKey{between}, betweenChange.Holdings, "the write between two of the prune's drops")
	require.Contains(t, report.Change.Holdings, sender, "the prune's own report")
}
