package dagql

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// placementTestEntry attaches a holding and returns the number of the Cloud
// entry it sits on.
func placementTestEntry(t *testing.T, cloud *Cache, key HolderKey, h RemoteHolding) uint64 {
	t.Helper()
	_, err := cloud.AttachRemoteHolding(t.Context(), key, h)
	require.NoError(t, err)
	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	number := cloud.holderEntries[key]
	require.NotZero(t, number)
	return uint64(number)
}

// PlacePart relocates a part's services and makes its owner exactly those
// services; a service with no mapping leaves the part unplaced.
func TestPlacePart(t *testing.T) {
	t.Parallel()
	part := testLiveOffer()
	part.Value.Services = []TransferredServiceBinding{{ServiceResultID: 7, Hostname: "db"}, {ServiceResultID: 8, Hostname: "cache"}}
	part.Owner.DependencyIDs = []uint64{3, 7, 8}
	placed, ok := PlacePart(part, func(service uint64) (uint64, bool) { return service * 10, true })
	require.True(t, ok)
	require.Equal(t, []uint64{70, 80}, []uint64{placed.Value.Services[0].ServiceResultID, placed.Value.Services[1].ServiceResultID})
	require.Equal(t, []uint64{70, 80}, placed.Owner.DependencyIDs, "the owner lists the mapped services and nothing else")
	require.Equal(t, []uint64{3, 7, 8}, part.Owner.DependencyIDs, "the input is left as it was")

	_, ok = PlacePart(part, func(service uint64) (uint64, bool) { return 0, service != 8 })
	require.False(t, ok, "not placed")
}

// PlaceOffer maps each service to the receiving engine's unexpired holding
// on an entry of the service entry's class, the lowest number first; an
// expired holding is never chosen, and a service the engine holds nothing of
// leaves the part unplaced.
func TestPlaceOffer(t *testing.T) {
	t.Parallel()
	cloud := newCloudCache(t)
	withContent := func(field string, expires int64) RemoteHolding {
		h := holdingOf(field)
		h.ContentDigest = testDigest("service-content")
		h.ExpiresAtUnix = expires
		return h
	}
	service := placementTestEntry(t, cloud, HolderKey{"engine-e", 20}, withContent("service", 0))
	placementTestEntry(t, cloud, HolderKey{"engine-e", 10}, withContent("service-other-recipe", time.Now().Add(-time.Hour).Unix()))
	placementTestEntry(t, cloud, HolderKey{"engine-e", 30}, withContent("service-third-recipe", 0))
	unheld := placementTestEntry(t, cloud, HolderKey{"engine-x", 5}, holdingOf("unheld-service"))

	part := testLiveOffer()
	part.Value.Services = []TransferredServiceBinding{{ServiceResultID: service, Hostname: "db"}}
	part.Owner.DependencyIDs = []uint64{service, 999}
	placed, ok, err := cloud.PlaceOffer(HolderKey{"engine-e", 1}, part)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, uint64(20), placed.Value.Services[0].ServiceResultID, "the lowest unexpired holding: 10 has expired")
	require.Equal(t, []uint64{20}, placed.Owner.DependencyIDs, "exactly the mapped service")

	part.Value.Services = append(part.Value.Services, TransferredServiceBinding{ServiceResultID: unheld, Hostname: "other"})
	_, ok, err = cloud.PlaceOffer(HolderKey{"engine-e", 1}, part)
	require.NoError(t, err)
	require.False(t, ok, "a service engine-e holds nothing of: not placed")
}
