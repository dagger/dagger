package containerimage

import (
	"context"

	"github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

func patchImageLayers(ctx context.Context, remote *solver.Remote, history []ocispecs.History, ref cache.ImmutableRef, opts *ImageCommitOpts, _ session.Group) (*solver.Remote, []ocispecs.History, error) {
	remote, history = normalizeLayersAndHistory(ctx, remote, history, ref, opts.OCITypes)
	return remote, history, nil
}
