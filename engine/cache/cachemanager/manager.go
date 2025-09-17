package cachemanager

import (
	"context"
	"os"

	"github.com/dagger/dagger/engine/distconsts"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver/mounts"
	"github.com/moby/buildkit/worker"
)

type ManagerConfig struct {
	KeyStore     solver.CacheKeyStorage
	ResultStore  solver.CacheResultStorage
	Worker       worker.Worker
	MountManager *mounts.MountManager
	EngineID     string
}

const (
	LocalCacheID = "local"
)

var contentStoreLayers = map[string]struct{}{}

func init() {
	layerInfo, _ := os.ReadDir(distconsts.EngineContainerBuiltinContentDir + "/blobs/sha256/")

	for _, li := range layerInfo {
		contentStoreLayers[li.Name()] = struct{}{}
	}
}

func NewManager(ctx context.Context, managerConfig ManagerConfig) (Manager, error) {
	localCache := solver.NewCacheManager(ctx, LocalCacheID, managerConfig.KeyStore, managerConfig.ResultStore)
	return defaultCacheManager{localCache}, nil
}

type Manager interface {
	solver.CacheManager
}

type defaultCacheManager struct {
	solver.CacheManager
}

var _ Manager = defaultCacheManager{}

func (c defaultCacheManager) ReleaseUnreferenced(ctx context.Context) error {
	return c.CacheManager.ReleaseUnreferenced(ctx)
}
