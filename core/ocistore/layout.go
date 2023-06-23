package ocistore

import (
	"context"
	"fmt"
	"io"
	"path"
	"time"

	"github.com/containerd/containerd/content"
	bkgw "github.com/moby/buildkit/frontend/gateway/client"
	"github.com/moby/buildkit/solver/pb"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type layoutStore struct {
	unimplementedStore

	ref        bkgw.Reference
	sourcePath string
}

var _ content.Store = (*layoutStore)(nil)

// NewLayoutStore returns a content store that uses the Buildkit gateway to
// lazily fetch its content from the provided ref and source path.
func NewLayoutStore(
	ctx context.Context,
	gw bkgw.Client,
	def *pb.Definition,
	sourcePath string, // optional path beneath the def's result
) (content.Store, error) {
	res, err := gw.Solve(ctx, bkgw.SolveRequest{
		Definition: def,
		Evaluate:   true,
	})
	if err != nil {
		return nil, err
	}

	ref, err := res.SingleRef()
	if err != nil {
		return nil, err
	}

	if ref == nil {
		return nil, fmt.Errorf("no ref returned")
	}

	return &layoutStore{ref: ref, sourcePath: sourcePath}, nil
}

func (c *layoutStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	stat, err := c.ref.StatFile(ctx, bkgw.StatRequest{
		Path: c.blobPath(dgst),
	})
	if err != nil {
		return content.Info{}, err
	}

	t := time.Unix(stat.ModTime/int64(time.Second), stat.ModTime%int64(time.Second))

	return content.Info{
		Digest:    dgst,
		Size:      stat.Size_,
		CreatedAt: t,
		UpdatedAt: t,
	}, nil
}

func (c *layoutStore) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	blobPath := c.blobPath(desc.Digest)

	stat, err := c.ref.StatFile(ctx, bkgw.StatRequest{
		Path: blobPath,
	})
	if err != nil {
		return nil, err
	}

	return &refReaderAt{
		ctx:  ctx,
		ref:  c.ref,
		name: blobPath,
		size: stat.Size_,
	}, nil
}

func (c *layoutStore) Walk(ctx context.Context, fn content.WalkFunc, filters ...string) error {
	algos, err := c.ref.ReadDir(ctx, bkgw.ReadDirRequest{
		Path: c.path("blobs"),
	})
	if err != nil {
		return err
	}

	for _, algo := range algos {
		if !algo.IsDir() {
			continue
		}

		algoName := digest.Algorithm(algo.Path)

		blobs, err := c.ref.ReadDir(ctx, bkgw.ReadDirRequest{
			Path: c.path("blobs", algo.Path),
		})
		if err != nil {
			return err
		}

		for _, blob := range blobs {
			t := time.Unix(blob.ModTime/int64(time.Second), blob.ModTime%int64(time.Second))

			err := fn(content.Info{
				Digest:    digest.NewDigestFromEncoded(algoName, blob.Path),
				Size:      blob.Size_,
				CreatedAt: t,
				UpdatedAt: t,
			})
			if err != nil {
				return err
			}
		}
	}

	return nil
}

func (c *layoutStore) path(parts ...string) string {
	return path.Join(append([]string{c.sourcePath}, parts...)...)
}

func (c *layoutStore) blobPath(dgst digest.Digest) string {
	return c.path("blobs", dgst.Algorithm().String(), dgst.Encoded())
}

type refReaderAt struct {
	ctx  context.Context
	ref  bkgw.Reference
	name string
	size int64
}

var _ content.ReaderAt = (*refReaderAt)(nil)

func (f *refReaderAt) ReadAt(p []byte, off int64) (int, error) {
	if off >= f.size {
		return 0, io.EOF
	}

	content, err := f.ref.ReadFile(f.ctx, bkgw.ReadRequest{
		Filename: f.name,
		Range: &bkgw.FileRange{
			Offset: int(off),
			Length: len(p),
		},
	})
	if err != nil {
		return 0, err
	}

	n := copy(p, content)
	if n < len(p) {
		return n, fmt.Errorf("short read: %d < %d", n, len(p))
	}

	return n, nil
}

func (f *refReaderAt) Size() int64 {
	return f.size
}

func (f *refReaderAt) Close() error {
	return nil
}
