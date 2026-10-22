package server

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/engine/remotecache"
	"github.com/dagger/dagger/engine/remotecache/protocol"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/engine/snapshots/testutil"
)

// An export compresses the layers it diffs with zstd, and another snapshot
// manager imports the exported chain back to the same file.
func TestRemoteCacheAdapterExportCompressesWithZstd(t *testing.T) {
	t.Parallel()
	e := newAdapterTestEngine(t)
	ready := e.file(t, "ready")
	require.NoError(t, e.cache.Evaluate(e.ctx, ready))
	number := entryNumber(t, ready)
	consumer := testutil.NewStore(t)
	req := protocol.Export{Roots: []uint64{number}, Outputs: []protocol.ExportOutput{{Number: number, Address: snapshotPart}}}
	err := e.adapter.Export(e.ctx, req, func(ctx context.Context, exp remotecache.Export) error {
		require.NotNil(t, exp.Bundle)
		require.Len(t, exp.Bundle.Outputs, 1)
		chain := exp.Bundle.Outputs[0].Chain
		require.NotNil(t, chain)
		require.NotEmpty(t, chain.Layers)
		for _, layer := range chain.Layers {
			require.Equal(t, ocispecs.MediaTypeImageLayerZstd, layer.Descriptor.MediaType)
		}
		ref, err := consumer.Manager.ImportChain(ctx, &bkcache.ExportChain{Layers: chain.Layers, Provider: blobProvider{exp.Blobs}})
		require.NoError(t, err)
		defer func() { require.NoError(t, ref.Release(context.WithoutCancel(ctx))) }()
		data, err := os.ReadFile(filepath.Join(testutil.Root(t, ref), "ready.txt"))
		require.NoError(t, err)
		require.Equal(t, "ready bytes", string(data))
		return nil
	})
	require.NoError(t, err)
}

// blobProvider serves an export's blobs as a consumer reads them back from
// the blob store.
type blobProvider struct{ blobs remotecache.BlobSource }

func (p blobProvider) read(ctx context.Context, dgst digest.Digest) ([]byte, error) {
	body, _, err := p.blobs.ReadBlob(ctx, dgst)
	if err != nil {
		return nil, err
	}
	defer body.Close()
	return io.ReadAll(body)
}

func (p blobProvider) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	data, err := p.read(ctx, dgst)
	if err != nil {
		return content.Info{}, err
	}
	return content.Info{Digest: dgst, Size: int64(len(data))}, nil
}

func (p blobProvider) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	data, err := p.read(ctx, desc.Digest)
	if err != nil {
		return nil, err
	}
	return bytesReaderAt{bytes.NewReader(data)}, nil
}

type bytesReaderAt struct{ *bytes.Reader }

func (bytesReaderAt) Close() error { return nil }
