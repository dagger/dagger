package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// factsTestKept is what a reader keeps of a holding's description from one
// change to the next: all of it but its sessions, offered parts, content
// digest and count of dependents, which a reader reads fresh when it needs
// them.
func factsTestKept(t *testing.T, cloud *Cache, key HolderKey) RemoteHoldingInfo {
	t.Helper()
	held, ok := cloud.RemoteHoldingInfo(key)
	require.True(t, ok, "holding %v", key)
	h := held.Holding
	h.Sessions, h.OfferedParts, h.ContentDigest, h.Dependents = nil, nil, "", 0
	return h
}

// A write that changes only what no reader keeps of a holding names no
// holding: a holding depending on it, which raises its count of dependents;
// a session's hold of it, and that session's end; a content digest it
// learns; and a part it is offered. Each leaves what a reader keeps of it as
// it was. On a whole pull request's fill, a stopping engine's pass after its
// session's end read again whole every holding the session had held: one
// read 10,762 again and found 157 changed.
func TestChangeReportsNameNoHoldingForWhatNoReaderKeeps(t *testing.T) {
	t.Parallel()
	ctx, cloud := storedPartTestCache(t, WithBlobStore())
	base, parent := HolderKey{"cache-a", 1}, HolderKey{"cache-a", 2}
	baseRecipe, parentRecipe := testDigest("facts-base"), testDigest("facts-parent")
	_, err := cloud.AttachRemoteHolding(ctx, base, RemoteHolding{Recipe: baseRecipe, Request: baseRecipe, Field: "facts-base", TypeName: "Directory"})
	require.NoError(t, err)
	kept := factsTestKept(t, cloud, base)

	change, err := cloud.AttachRemoteHolding(ctx, parent, RemoteHolding{Recipe: parentRecipe, Request: parentRecipe, Field: "facts-parent", TypeName: "Directory", Deps: []uint64{base.Number}})
	require.NoError(t, err)
	require.Contains(t, change.Holdings, parent)
	require.NotContains(t, change.Holdings, base, "a holding depending on base raises base's count of dependents alone")
	require.Equal(t, kept, factsTestKept(t, cloud, base))

	change, err = cloud.AddRemoteHold(ctx, base, "s1")
	require.NoError(t, err)
	require.NotContains(t, change.Holdings, base, "a session's hold changes base's sessions alone")
	change = cloud.ReleaseRemoteSession(ctx, "cache-a", "s1")
	require.NotContains(t, change.Holdings, base, "the session's end changes base's sessions alone")
	require.Contains(t, change.Candidates, base, "and still makes base a candidate for collection")
	require.Equal(t, kept, factsTestKept(t, cloud, base))

	change, found, err := cloud.UpdateRemoteHolding(ctx, base, RemoteHoldingUpdate{ContentDigest: testDigest("facts-content")})
	require.NoError(t, err)
	require.True(t, found)
	require.NotContains(t, change.Holdings, base, "a content digest learned changes base's content digest alone")
	require.Equal(t, kept, factsTestKept(t, cloud, base))

	change, found, err = cloud.ApplyHeldValueState(ctx, base, HeldValueState{OfferedParts: []PersistedPartAddress{{Part: "snapshot"}}})
	require.NoError(t, err)
	require.True(t, found)
	require.NotContains(t, change.Holdings, base, "a part offered changes base's offered parts alone")
	held, ok := cloud.RemoteHoldingInfo(base)
	require.True(t, ok)
	require.NotEmpty(t, held.Holding.OfferedParts, "base is offered the part")
	require.Equal(t, kept, factsTestKept(t, cloud, base))
}
