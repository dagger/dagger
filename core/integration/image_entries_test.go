package core

import (
	"context"
	"encoding/json"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/distconsts"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// The engine image ships its entries' blobs uncompressed: the image's own
// layer compresses them for download, and an engine applies each with no
// decompression the first time it uses it. Every layer the bundle offers,
// a pulled image's included, is a tar, and its blob is the tar itself.
func (CacheSuite) TestImageEntriesAreUncompressed(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	raw, err := devEngineContainer(c).File(distconsts.EngineContainerImageEntriesDir + "/bundle.json").Contents(ctx)
	require.NoError(t, err)
	var bundle dagql.ValueBundle
	require.NoError(t, json.Unmarshal([]byte(raw), &bundle))
	layers := 0
	for _, output := range bundle.Outputs {
		if output.Chain == nil {
			continue
		}
		for _, layer := range output.Chain.Layers {
			layers++
			d := layer.Descriptor.Digest
			require.Equal(t, "application/vnd.oci.image.layer.v1.tar", layer.Descriptor.MediaType, "layer %s", d)
			// The blob is the layer's tar itself: any uncompressed digest it
			// names is its own.
			if diffID, ok := layer.Descriptor.Annotations["containerd.io/uncompressed"]; ok {
				require.Equal(t, d.String(), diffID, "layer %s", d)
			}
		}
	}
	require.NotZero(t, layers, "the bundle offers layers")
}
