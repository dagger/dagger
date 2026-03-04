package containerimage

import (
	"context"

	cache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/session"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func patchImageLayers(ctx context.Context, remote *cache.Remote, history []ocispecs.History, ref cache.ImmutableRef, opts *ImageCommitOpts, _ session.Group) (*cache.Remote, []ocispecs.History, error) {
	remote, history = normalizeLayersAndHistory(ctx, remote, history, ref, opts.OCITypes)
	return remote, history, nil
}
