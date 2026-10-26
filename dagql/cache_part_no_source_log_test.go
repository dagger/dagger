package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// A part that no source can supply is computed, and logged once: the entry as
// the remote cache knows it, the step and the part, and why no source could
// supply it, among how many candidates and held addresses.
func TestPartWithoutSourceLogged(t *testing.T) {
	chain := newChainFixture(t, "no-source-log")
	for _, tc := range []struct {
		name       string
		setup      func(*testing.T, *exhaustionFixture)
		reason     string
		candidates int64
		offers     int64
	}{
		{
			name:       "no address",
			setup:      func(*testing.T, *exhaustionFixture) {},
			reason:     "no_offer",
			candidates: 1,
		},
		{
			name: "a download that failed",
			setup: func(t *testing.T, f *exhaustionFixture) {
				f.transport.fault("https://fixed.invalid/no-source-log/1", contentTestFault{corrupt: true, stallAfter: -1})
				f.attach(t, f.receiver, exhaustionOffer(chain, "", true))
			},
			reason:     "download_failed",
			candidates: 1,
			offers:     1,
		},
		{
			name: "an equivalent entry's expired address",
			setup: func(t *testing.T, f *exhaustionFixture) {
				f.addDonor(t)
				f.attach(t, f.donor, exhaustionOffer(chain, "", false))
			},
			reason:     "offer_unavailable",
			candidates: 2,
			offers:     1,
		},
		{
			name: "an equivalent entry still being written",
			setup: func(t *testing.T, f *exhaustionFixture) {
				busy := &transferTestValue{Text: "pending"}
				f.donor = persistedListTestResult(t, f.ctx, f.cache, f.srv, "donor", busy)
				partTestEquivalent(t, f.cache, f.receiver, f.donor)
				busy.unready.Store(true)
			},
			reason:     "holder_busy",
			candidates: 2,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newExhaustionFixture(t, chain)
			tc.setup(t, f)
			require.ErrorIs(t, f.run(), errRenewalFallbackRan)
			require.EqualValues(t, 1, f.operation.runs.Load(), "the part is computed")
			lines := f.logs.lines(t, "remote cache part without a source")
			require.Len(t, lines, 1)
			line := lines[0]
			require.Equal(t, f.cache.Identity().ID, line.fields["cache"])
			require.EqualValues(t, f.receiver.cacheSharedResult().id, line.int(t, "entry"))
			require.Zero(t, line.int(t, "replacements"))
			require.Zero(t, line.int(t, "merged_at"))
			require.Equal(t, "restored", line.fields["origin"])
			require.Equal(t, "receiver", line.fields["field"])
			require.Equal(t, "snapshot", line.fields["part"])
			require.Equal(t, string(LazyGroupWhole), line.fields["group"])
			require.Equal(t, tc.reason, line.fields["reason"])
			require.Equal(t, tc.candidates, line.int(t, "candidates"))
			require.Equal(t, tc.offers, line.int(t, "offers"))
		})
	}
}
