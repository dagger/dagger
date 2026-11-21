package dagql

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The placement rule relocates a part's services and makes its owner exactly
// those services; a service with no mapping leaves the part unplaced.
func TestPlacementRule(t *testing.T) {
	t.Parallel()
	part := testLiveOffer()
	part.Value.Services = []TransferredServiceBinding{{ServiceResultID: 7, Hostname: "db"}, {ServiceResultID: 8, Hostname: "cache"}}
	part.Owner.DependencyIDs = []uint64{3, 7, 8}
	copies, err := clonePartOffers([]PersistedPartOffer{part, part})
	require.NoError(t, err)
	placed, ok := placeOwnedPart(copies[0], func(service uint64) (uint64, bool) { return service * 10, true })
	require.True(t, ok)
	require.Equal(t, []uint64{70, 80}, []uint64{placed.Value.Services[0].ServiceResultID, placed.Value.Services[1].ServiceResultID})
	require.Equal(t, []uint64{70, 80}, placed.Owner.DependencyIDs, "the owner lists the mapped services and nothing else")

	_, ok = placeOwnedPart(copies[1], func(service uint64) (uint64, bool) { return 0, service != 8 })
	require.False(t, ok, "not placed")
}
