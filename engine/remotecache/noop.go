package remotecache

import (
	"context"

	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/worker"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

var _ remotecache.Importer = &noopImporter{}

type noopImporter struct {
}

func (i *noopImporter) Resolve(ctx context.Context, desc ocispecs.Descriptor, id string, w worker.Worker) (solver.CacheManager, error) {
	return nil, nil
}
