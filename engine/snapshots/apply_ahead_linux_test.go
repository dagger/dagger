package snapshots_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/dagger/dagger/engine/snapshots/testutil"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"
)

// A chain layer's blob is decompressed ahead of its apply: the blob is read
// from the content store while the apply has yet to read anything, and the
// tree is the layer's.
func TestImportChainOverlayDecompressesALayerAheadOfItsApply(t *testing.T) {
	t.Parallel()
	producer, consumer := testutil.NewStore(t), testutil.NewOverlayStore(t)
	chain := layeredChain(t, producer, "layer", 1)
	size := chain.Layers[0].Descriptor.Size
	consumer.BeforeApply = func(ctx context.Context, _ ocispecs.Descriptor) error {
		deadline := time.After(10 * time.Second)
		for consumer.ContentRead.Load() < size {
			select {
			case <-deadline:
				return errors.New("the layer's blob was not read ahead of its apply")
			case <-ctx.Done():
				return context.Cause(ctx)
			case <-time.After(time.Millisecond):
			}
		}
		return nil
	}
	ref := importChain(t, consumer, supplied(chain, chain.Provider))
	require.Equal(t, map[string]string{"layer-00.txt": "file 640 layer 0"}, testutil.OverlayTree(t, ref))
	require.EqualValues(t, 1, consumer.Applies.Load())
}
