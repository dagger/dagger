package schema

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/containerd/containerd/v2/core/content"
	"github.com/dagger/dagger/dagql"
	"github.com/opencontainers/go-digest"
	ocispec "github.com/opencontainers/image-spec/specs-go/v1"
)

// The gated fixture uses the same admitted ImportChain path as a fixed-address
// provider, with bytes carried in the peer's fixture volume.
type fixturePartContentSource struct{ path string }

func (s fixturePartContentSource) Provider(_ context.Context, offer dagql.PersistedPartOffer, _ *dagql.PartDemandState) content.InfoReaderProvider {
	return &fixturePartProvider{path: s.path, offer: offer}
}

type fixturePartProvider struct {
	path  string
	offer dagql.PersistedPartOffer
}

func (p *fixturePartProvider) Info(_ context.Context, id digest.Digest) (content.Info, error) {
	for _, layer := range p.offer.Chain.Layers {
		if layer.Descriptor.Digest == id {
			return content.Info{Digest: id, Size: layer.Descriptor.Size}, nil
		}
	}
	return content.Info{}, fmt.Errorf("fixture blob is outside admitted chain")
}
func (p *fixturePartProvider) ReaderAt(ctx context.Context, desc ocispec.Descriptor) (content.ReaderAt, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	if _, err := p.Info(ctx, desc.Digest); err != nil {
		return nil, err
	}
	if err := desc.Digest.Validate(); err != nil {
		return nil, err
	}
	root, err := fixtureRoot(p.path, "blobs")
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(filepath.Join(desc.Digest.Algorithm().String(), desc.Digest.Encoded()))
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return nil, err
	}
	return fixtureBlobReader{File: file, size: info.Size()}, nil
}

type fixtureBlobReader struct {
	*os.File
	size int64
}

func (r fixtureBlobReader) Size() int64 { return r.size }
func writeFixtureChains(ctx context.Context, path string, chains *dagql.SelectedChains) error {
	root, err := fixtureRoot(path, "blobs")
	if err != nil {
		return err
	}
	defer root.Close()
	for _, entry := range chains.Entries {
		for _, layer := range entry.Layers {
			desc := layer.Descriptor
			if err := desc.Digest.Validate(); err != nil {
				return err
			}
			raw, err := content.ReadBlob(ctx, entry.Provider, desc)
			if err != nil {
				return err
			}
			name := filepath.Join(desc.Digest.Algorithm().String(), desc.Digest.Encoded())
			if err := root.MkdirAll(filepath.Dir(name), 0700); err != nil {
				return err
			}
			file, err := root.OpenFile(name, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600)
			if err != nil {
				return err
			}
			_, err = file.Write(raw)
			closeErr := file.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	return nil
}

func (fixturePartContentSource) Available(dagql.PersistedPartOffer, time.Time) bool { return true }
