package dagql

import (
	"context"
	"testing"

	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/stretchr/testify/require"
)

// A capture of a finished result takes it while an offer lands on a row
// below it, or while another export of the same engine retires an offer
// there: a transferred record carries no offers, so neither changes what the
// capture copies. Otherwise the export refused the finished result as busy,
// and its content was never stored.
func TestCaptureTakesAResultWhoseRowsTakeOrLoseAnOffer(t *testing.T) {
	t.Parallel()
	for _, change := range []string{"an offer lands", "an offer is retired"} {
		t.Run(change, func(t *testing.T) {
			t.Parallel()
			ctx, c, srv := transferTestCache(t)
			below := persistedListTestResult(t, ctx, c, srv, "below", &transferTestValue{Text: "pending"})
			root := persistedListTestResult(t, ctx, c, srv, "root", String("root"))
			transferTestDependency(c, ctx, root, below)
			row := below.cacheSharedResult()
			offer := func() {
				out, err := c.testOfferParts(ctx, below, []PersistedPartOffer{testLiveOffer()})
				require.NoError(t, err)
				require.Equal(t, OfferAccepted, out[0].Outcome)
			}
			if change == "an offer is retired" {
				offer()
			}
			c.testTransferCopied = func(id uint64) {
				if id != uint64(row.id) {
					return
				}
				switch change {
				case "an offer lands":
					offer()
				case "an offer is retired":
					c.egraphMu.Lock()
					queue, err := c.retirePartOfferLocked(ctx, row, testLiveOffer().Address)
					var releases []OnReleaseFunc
					if err == nil {
						releases, err = c.collectUnownedResultsLocked(ctx, queue)
					}
					c.egraphMu.Unlock()
					require.NoError(t, err)
					require.NoError(t, runOnReleaseFuncs(ctx, releases))
				}
			}
			var values *ExportedValues
			err := c.WithExportedValues(ctx, ValueSelection{Roots: []AnyResult{root}}, config.RefConfig{}, func(_ context.Context, exported *ExportedValues) error {
				values = exported
				return nil
			})
			c.testTransferCopied = nil
			require.NoError(t, err, "the finished result isn't refused as busy")
			require.Empty(t, values.LeftOut)
			require.Len(t, values.Bundle.Roots, 1)
			require.Equal(t, map[uint64]bool{rowNumber(root): true, rowNumber(below): true}, senderNumbers(values.Bundle))
		})
	}
}
