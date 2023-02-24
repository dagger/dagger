package remotecache

import (
	"context"
	"strconv"

	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/worker"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// TODO: not sure how well stacking importers like this scales,
// in theory actually merging the manifests would be more efficient
type combinedImporter struct {
	importers []remotecache.Importer
}

func (i *combinedImporter) Resolve(ctx context.Context, desc ocispecs.Descriptor, id string, w worker.Worker) (solver.CacheManager, error) {
	cacheManagers := make([]solver.CacheManager, len(i.importers))
	for i, importer := range i.importers {
		cm, err := importer.Resolve(ctx, desc, id+"-"+strconv.Itoa(i), w)
		if err != nil {
			return nil, err
		}
		cacheManagers[i] = cm
	}
	return solver.NewCombinedCacheManager(cacheManagers, nil), nil
}
