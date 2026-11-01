package snapshots_test

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/containerd/containerd/v2/pkg/labels"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
	"github.com/klauspost/compress/zstd"
	digest "github.com/opencontainers/go-digest"
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

// A layer whose blob holds a whole tar followed by a truncated zstd frame
// fails its import, as the applier that decompresses as it reads fails it
// while draining what follows the tar.
func TestImportChainOverlayFailsATruncatedZstdLayer(t *testing.T) {
	t.Parallel()
	consumer := testutil.NewOverlayStore(t)
	ctx := context.Background()
	var raw bytes.Buffer
	tw := tar.NewWriter(&raw)
	require.NoError(t, tw.WriteHeader(&tar.Header{Name: "a.txt", Mode: 0o644, ModTime: testutil.FileTime, Typeflag: tar.TypeReg, Size: 5}))
	_, err := tw.Write([]byte("first"))
	require.NoError(t, err)
	require.NoError(t, tw.Close())
	frame := func(data []byte) []byte {
		var out bytes.Buffer
		zw, err := zstd.NewWriter(&out)
		require.NoError(t, err)
		_, err = zw.Write(data)
		require.NoError(t, err)
		require.NoError(t, zw.Close())
		return out.Bytes()
	}
	next := frame(bytes.Repeat([]byte("trailing bytes "), 1000))
	blob := append(frame(raw.Bytes()), next[:len(next)/2]...)
	desc := ocispecs.Descriptor{
		MediaType:   ocispecs.MediaTypeImageLayerZstd,
		Digest:      digest.FromBytes(blob),
		Size:        int64(len(blob)),
		Annotations: map[string]string{labels.LabelUncompressed: digest.FromBytes(raw.Bytes()).String()},
	}
	require.NoError(t, content.WriteBlob(ctx, consumer.Content, "truncated-zstd", bytes.NewReader(blob), desc))
	_, err = consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: []bkcache.ExportLayer{{Descriptor: desc}}})
	var contentErr *bkcache.ChainContentError
	require.ErrorAs(t, err, &contentErr)
	require.Equal(t, "apply", contentErr.Stage)
}
