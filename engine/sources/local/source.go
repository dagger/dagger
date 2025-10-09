package local

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/solver"
	"github.com/dagger/dagger/internal/buildkit/solver/pb"
	"github.com/dagger/dagger/internal/buildkit/source"
	srctypes "github.com/dagger/dagger/internal/buildkit/source/types"
)

// Return a new local source placeholder.
func NewSource() source.Source {
	return &localSource{}
}

// localSource is a placeholder for unsupported llb.Local operations.
// We don't want to panic in the solver, so we return an error here but
// we expect that this should never be called on purpose.
// See https://github.com/dagger/dagger/pull/10995#discussion_r2410110392 for more details
type localSource struct{}

func (ls *localSource) Schemes() []string {
	return []string{srctypes.LocalScheme}
}

func (ls *localSource) Identifier(scheme, ref string, attrs map[string]string, platform *pb.Platform) (source.Identifier, error) {
	return nil, fmt.Errorf("unsupported llb.Local operation has been called")
}

func (ls *localSource) Resolve(ctx context.Context, id source.Identifier, sm *session.Manager, _ solver.Vertex) (source.SourceInstance, error) {
	return nil, fmt.Errorf("unsupported llb.Local operation has been called")
}
