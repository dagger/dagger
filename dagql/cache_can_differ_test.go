package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// An entry the Cloud knows only through holdings can differ unless its
// holdings' call rows declare that it follows from its inputs, and all of
// them do; with no holding, nothing declares it.
func TestAnEntryKnownThroughHoldingsCanDifferUnlessTheyDeclareIt(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	canDiffer := func(key HolderKey) bool {
		cloud.egraphMu.RLock()
		defer cloud.egraphMu.RUnlock()
		entry, _ := cloud.holdingLocked(key)
		require.NotNil(t, entry)
		return cloud.entryCanDifferLocked(entry)
	}
	declared := holdingOf("declared")
	declared.FollowsFromInputs, declared.CallRow = true, true
	a := HolderKey{Cache: "cache-a", Number: 1}
	_, err := cloud.AttachRemoteHolding(ctx, a, declared)
	require.NoError(t, err)
	require.False(t, canDiffer(a))

	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-c", Number: 1}, holdingOf("declared"))
	require.NoError(t, err)
	require.False(t, canDiffer(a), "a holding no call row describes says nothing")
	undeclared := holdingOf("declared")
	undeclared.CallRow = true
	_, err = cloud.AttachRemoteHolding(ctx, HolderKey{Cache: "cache-b", Number: 1}, undeclared)
	require.NoError(t, err)
	require.True(t, canDiffer(a), "two holdings that disagree")

	b := HolderKey{Cache: "cache-a", Number: 2}
	rowed := holdingOf("undeclared")
	rowed.CallRow = true
	_, err = cloud.AttachRemoteHolding(ctx, b, rowed)
	require.NoError(t, err)
	require.True(t, canDiffer(b))
}

// A merge holds its sender's entry with no call row, which says nothing of
// the declaration: the record's frame alone answers for it.
func TestARecordsDeclarationStandsBesideItsSendersHolding(t *testing.T) {
	t.Parallel()
	bundle, _ := mergeTestSource(t, "declared-record", "declared")
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	reply, err := cloud.MergeValues(ctx, "cache-a", bundle)
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	res := cloud.resultsByID[sharedResultID(reply.Values[0].Number)]
	require.NotEmpty(t, res.holderRefsLocked(), "the merge holds the sender's entry")
	require.False(t, cloud.entryCanDifferLocked(res))
}

// A holding's word on the declaration reaches the readers of its facts, which
// ask CanDiffer of them as the Cloud asks of the entry.
func TestAHoldingsFactsCarryItsDeclaration(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	declared := holdingOf("facts-declared")
	declared.FollowsFromInputs, declared.CallRow = true, true
	key := HolderKey{Cache: "cache-a", Number: 1}
	_, err := cloud.AttachRemoteHolding(ctx, key, declared)
	require.NoError(t, err)
	facts, ok := cloud.HoldingFacts(key)
	require.True(t, ok)
	require.False(t, facts.CanDiffer())
	held, ok := cloud.RemoteHoldingInfo(key)
	require.True(t, ok)
	require.False(t, held.Holding.CanDiffer())

	other := HolderKey{Cache: "cache-a", Number: 2}
	_, err = cloud.AttachRemoteHolding(ctx, other, holdingOf("facts-undeclared"))
	require.NoError(t, err)
	facts, ok = cloud.HoldingFacts(other)
	require.True(t, ok)
	require.True(t, facts.CanDiffer(), "no call row described it")
	require.True(t, RemoteHolding{FollowsFromInputs: true}.CanDiffer(), "a declaration only a call row's description gives")
	require.False(t, RemoteHolding{FollowsFromInputs: true, CallRow: true}.CanDiffer())
}
