package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/internal/buildkit/util/contentutil"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/opencontainers/go-digest"
	specs "github.com/opencontainers/image-spec/specs-go/v1"
)

func BuiltInContainer(ctx context.Context, platform Platform, blobDigest string) (*Container, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}

	ctx, release, err := leaseutil.WithLease(ctx, query.LeaseManager(), leaseutil.MakeTemporary)
	if err != nil {
		return nil, err
	}
	defer release(context.WithoutCancel(ctx))

	manifestDigest := digest.Digest(blobDigest)
	info, err := query.BuiltinOCIStore().Info(ctx, manifestDigest)
	if err != nil {
		return nil, fmt.Errorf("lookup builtin image manifest %s: %w", manifestDigest, err)
	}

	manifestDesc := specs.Descriptor{
		Digest:    manifestDigest,
		Size:      info.Size,
		MediaType: specs.MediaTypeImageManifest,
	}
	if err := contentutil.CopyChain(ctx, query.OCIStore(), query.BuiltinOCIStore(), manifestDesc); err != nil {
		return nil, fmt.Errorf("copy builtin image content: %w", err)
	}

	return NewContainer(platform).FromOCIStore(ctx, manifestDesc, "")
}
