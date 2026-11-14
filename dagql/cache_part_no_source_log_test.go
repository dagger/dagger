package dagql

import (
	"fmt"
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
			name: "an equivalent entry still being written, with an address",
			setup: func(t *testing.T, f *exhaustionFixture) {
				busy := &transferTestValue{Text: "pending"}
				f.donor = persistedListTestResult(t, f.ctx, f.cache, f.srv, "donor", busy)
				partTestEquivalent(t, f.cache, f.receiver, f.donor)
				f.attach(t, f.donor, exhaustionOffer(chain, "", true))
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
			require.Equal(t, map[string]uint64{tc.reason: 1}, f.cache.PartsWithoutSource(), "the part is counted by its reason")
			require.Equal(t, tc.candidates, line.int(t, "candidates"))
			require.Equal(t, tc.offers, line.int(t, "offers"))
			require.Zero(t, line.int(t, "kept_parts"), "the receiver's one part is the rebuilt one")
			require.Zero(t, line.int(t, "merged_dependents"), "nothing depends on the receiver")
			require.Equal(t, false, line.fields["merged_dependents_more"])
		})
	}
}

// A rebuilt part is a second execution of its entry's recipe, and the line
// counts what the cache keeps from the first: the merged entries computed
// from it, directly or through other entries. An entry this engine computed
// itself isn't one.
func TestPartWithoutSourceCountsMergedDependents(t *testing.T) {
	chain := newChainFixture(t, "no-source-dependents")
	f := newExhaustionFixture(t, chain)
	direct := persistedListTestResult(t, f.ctx, f.cache, f.srv, "direct", &transferTestValue{Text: "direct"})
	through := persistedListTestResult(t, f.ctx, f.cache, f.srv, "through", &transferTestValue{Text: "through"})
	local := persistedListTestResult(t, f.ctx, f.cache, f.srv, "local", &transferTestValue{Text: "local"})
	require.NoError(t, f.cache.AddExplicitDependency(f.ctx, direct, f.receiver, "test"))
	require.NoError(t, f.cache.AddExplicitDependency(f.ctx, through, direct, "test"))
	require.NoError(t, f.cache.AddExplicitDependency(f.ctx, local, f.receiver, "test"))
	f.cache.egraphMu.Lock()
	direct.cacheSharedResult().imported = true
	through.cacheSharedResult().imported = true
	f.cache.egraphMu.Unlock()
	require.ErrorIs(t, f.run(), errRenewalFallbackRan)
	lines := f.logs.lines(t, "remote cache part without a source")
	require.Len(t, lines, 1)
	require.EqualValues(t, 2, lines[0].int(t, "merged_dependents"), "direct and through were merged; local wasn't")
	require.Equal(t, false, lines[0].fields["merged_dependents_more"])
}

func TestMergedDependentsStopAtTheLimit(t *testing.T) {
	ctx, c, srv := transferTestCache(t)
	root := persistedListTestResult(t, ctx, c, srv, "root", &transferTestValue{Text: "root"})
	for i := range 3 {
		dep := persistedListTestResult(t, ctx, c, srv, fmt.Sprintf("dep%d", i), &transferTestValue{Text: "dep"})
		require.NoError(t, c.AddExplicitDependency(ctx, dep, root, "test"))
		c.egraphMu.Lock()
		dep.cacheSharedResult().imported = true
		c.egraphMu.Unlock()
	}
	c.egraphMu.RLock()
	defer c.egraphMu.RUnlock()
	count, more := c.mergedDependentsLocked(root.cacheSharedResult(), 10)
	require.Equal(t, 3, count)
	require.False(t, more)
	count, more = c.mergedDependentsLocked(root.cacheSharedResult(), 2)
	require.Equal(t, 1, count, "the walk sees the root and one dependent within its limit of 2")
	require.True(t, more)
}

// A rebuild installs only the parts of its group not yet complete: the
// complete ones stay from the first execution and are counted, the rebuilt
// part and parts outside the group aren't.
func TestKeptPartsCountsCompletePartsOfTheGroup(t *testing.T) {
	part := func(name string) PersistedPartAddress { return PersistedPartAddress{Part: PartKey(name)} }
	probe := func(name string, complete bool) PartProbe {
		return PartProbe{Descriptor: PartDescriptor{Address: part(name)}, LocalComplete: complete}
	}
	probes := []PartProbe{probe("fs", true), probe("execMeta", true), probe("mount:/out", false), probe("metadata", true)}
	writeSet := []PersistedPartAddress{part("fs"), part("execMeta"), part("mount:/out")}
	key := func(name string) string {
		k, err := partAddressKey(part(name))
		require.NoError(t, err)
		return k
	}
	require.Equal(t, 2, keptParts(probes, writeSet, key("mount:/out")), "fs and execMeta stay; metadata is outside the group")
	require.Equal(t, 1, keptParts(probes, writeSet, key("fs")), "execMeta stays; mount:/out isn't complete")
	require.Equal(t, 3, keptParts(probes, nil, key("mount:/out")), "with no group, every other complete part stays")
}
