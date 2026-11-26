package dagql

import (
	"testing"
	"unsafe"

	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// A cache's held parts share their platform, and its layers their media type
// and description, with every part and layer that has them alike: the cache
// holds one copy of each, however many parts and layers name it. Each part
// still reads back as it was stored.
func TestStoredPartsShareTheirPlatformsAndLayerNames(t *testing.T) {
	t.Parallel()
	ctx, cloud, batch := storedBenchCloud(t)
	platformed := storedBenchPart(1 << 40)
	platformed.Address.Part = "platformed"
	platformed.Value.Platform = &ocispecs.Platform{OS: "linux", Architecture: "arm64"}
	for _, number := range batch[:2] {
		part, err := clonePartOffers([]PersistedPartOffer{platformed})
		require.NoError(t, err)
		part[0].Value.Platform = &ocispecs.Platform{OS: "linux", Architecture: "arm64"}
		stored, _, err := cloud.SetStoredPart(ctx, number, part[0], 0)
		require.NoError(t, err)
		require.True(t, stored)
	}

	cloud.egraphMu.RLock()
	defer cloud.egraphMu.RUnlock()
	descriptions := map[string]map[*byte]bool{}
	mediaTypes := map[string]map[*byte]bool{}
	note := func(seen map[string]map[*byte]bool, s string) {
		if seen[s] == nil {
			seen[s] = map[*byte]bool{}
		}
		seen[s][unsafe.StringData(s)] = true
	}
	platforms := map[*ocispecs.Platform]bool{}
	for _, res := range cloud.resultsByID {
		for _, part := range res.storedPartsLocked() {
			if part.held.Value.Platform != nil {
				platforms[part.held.Value.Platform] = true
			}
			for _, layer := range part.layers {
				note(descriptions, layer.layer.Description)
				note(mediaTypes, layer.layer.Descriptor.MediaType)
			}
		}
	}
	require.Len(t, platforms, 1, "two parts of one platform hold %d copies of it", len(platforms))
	require.Len(t, descriptions, storedBenchLayers)
	for description, copies := range descriptions {
		require.Len(t, copies, 1, "layers described %q hold %d copies of their description", description, len(copies))
	}
	for mediaType, copies := range mediaTypes {
		require.Len(t, copies, 1, "layers of media type %q hold %d copies of it", mediaType, len(copies))
	}

	got, ok := cloud.resultsByID[sharedResultID(batch[0])].storedParts.find(mustPartAddressKey(t, platformed.Address))
	require.True(t, ok)
	require.Equal(t, platformed.Value, got.offer().Value)
	require.Equal(t, platformed.Chain.Layers, got.offer().Chain.Layers)
}

func mustPartAddressKey(t *testing.T, address PersistedPartAddress) string {
	t.Helper()
	key, err := partAddressKey(address)
	require.NoError(t, err)
	return key
}
