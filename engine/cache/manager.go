package cache

import (
	"context"
	"fmt"
	"sync"
	"time"

	"dagger.io/dagger"
	"github.com/moby/buildkit/cache"
	cacheconfig "github.com/moby/buildkit/cache/config"
	remotecache "github.com/moby/buildkit/cache/remotecache/v1"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/compression"
	"github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type manager struct {
	ManagerConfig
	client     Service
	layerstore LayerStore
	localCache solver.CacheManager
	s3Config   *S3LayerStoreConfig

	mu                 sync.RWMutex
	inner              solver.CacheManager
	startCloseCh       chan struct{} // closed when shutdown should start
	doneCh             chan struct{} // closed when shutdown is complete
	stopCacheMountSync func(context.Context) error
}

type ManagerConfig struct {
	KeyStore    solver.CacheKeyStorage
	ResultStore solver.CacheResultStorage
	Worker      worker.Worker
	ServiceURL  string
}

func NewManager(ctx context.Context, managerConfig ManagerConfig) (Manager, error) {
	localCache := solver.NewCacheManager(ctx, "local", managerConfig.KeyStore, managerConfig.ResultStore)
	m := &manager{
		ManagerConfig: managerConfig,
		localCache:    localCache,
		startCloseCh:  make(chan struct{}),
		doneCh:        make(chan struct{}),
	}

	if managerConfig.ServiceURL == "" {
		return defaultCacheManager{m.localCache}, nil
	}
	bklog.G(ctx).Debugf("using cache service at %s", managerConfig.ServiceURL)

	serviceClient, err := newClient(managerConfig.ServiceURL)
	if err != nil {
		return nil, err
	}
	m.client = serviceClient

	config, err := m.client.GetConfig(ctx, GetConfigRequest{})
	if err != nil {
		return nil, err
	}
	if config.ImportPeriod == 0 || config.ExportPeriod == 0 || config.ExportTimeout == 0 {
		return nil, fmt.Errorf("invalid cache config: import/export periods must be non-zero")
	}

	switch {
	case config.S3 != nil:
		m.layerstore, err = NewS3LayerStore(ctx, *config.S3)
		if err != nil {
			return nil, err
		}
		m.s3Config = config.S3
	default:
		return nil, fmt.Errorf("invalid cache config: no supported remote store configured")
	}

	// do an initial synchronous import at start
	// TODO: make this non-fatal (but ensure no inconsistent state in failure case)
	if err := m.Import(ctx); err != nil {
		return nil, err
	}
	// loop for periodic async imports
	go func() {
		for {
			select {
			case <-time.After(config.ImportPeriod):
			case <-m.startCloseCh:
				return
			}
			importContext, cancel := context.WithTimeout(context.Background(), time.Minute)
			go func() {
				<-m.startCloseCh
				cancel()
			}()
			if err := m.Import(importContext); err != nil {
				bklog.G(ctx).WithError(err).Error("failed to import cache")
			}
		}
	}()

	// loop for periodic async exports
	go func() {
		defer close(m.doneCh)
		var shutdown bool
		for {
			select {
			case <-time.After(config.ExportPeriod):
			case <-m.startCloseCh:
				shutdown = true
				// always run a final export before shutdown
			}
			exportCtx, cancel := context.WithTimeout(context.Background(), config.ExportTimeout)
			defer cancel()
			if err := m.Export(exportCtx); err != nil {
				bklog.G(ctx).WithError(err).Error("failed to export cache")
			}
			if shutdown {
				return
			}
		}
	}()

	return m, nil
}

func (m *manager) Export(ctx context.Context) error {
	var cacheKeys []CacheKey
	var links []Link

	err := m.KeyStore.Walk(func(id string) error {
		cacheKey := CacheKey{ID: id}
		err := m.KeyStore.WalkBacklinks(id, func(linkedID string, linkInfo solver.CacheInfoLink) error {
			link := Link{
				ID:       id,
				LinkedID: linkedID,
				Input:    int(linkInfo.Input),
				Digest:   linkInfo.Digest,
				Selector: linkInfo.Selector,
			}
			links = append(links, link)
			return nil
		})
		if err != nil {
			return err
		}
		err = m.KeyStore.WalkResults(id, func(cacheResult solver.CacheResult) error {
			res, err := m.ResultStore.Load(ctx, cacheResult)
			if err != nil {
				// the ref may be lazy or pruned, just skip it
				bklog.G(ctx).Debugf("skipping cache result %s for %s: %v", cacheResult.ID, id, err)
				return nil
			}
			defer res.Release(context.Background()) // TODO: hold on until later export?
			workerRef, ok := res.Sys().(*worker.WorkerRef)
			if !ok {
				bklog.G(ctx).Debugf("skipping cache result %s for %s: not an immutable ref", cacheResult.ID, id)
				return nil
			}
			cacheRef := workerRef.ImmutableRef
			cacheKey.Results = append(cacheKey.Results, Result{
				ID:          cacheRef.ID(),
				CreatedAt:   cacheResult.CreatedAt,
				Description: cacheRef.GetDescription(),
			})
			return nil
		})
		if err != nil {
			return err
		}
		cacheKeys = append(cacheKeys, cacheKey)
		return nil
	})
	if err != nil {
		return err
	}

	updateCacheRecordsResp, err := m.client.UpdateCacheRecords(ctx, UpdateCacheRecordsRequest{
		CacheKeys: cacheKeys,
		Links:     links,
	})
	if err != nil {
		return err
	}
	recordsToExport := updateCacheRecordsResp.ExportRecords
	if len(recordsToExport) == 0 {
		return nil
	}

	updatedRecords := make([]RecordLayers, 0, len(recordsToExport))
	for _, record := range recordsToExport {
		cacheRef, err := m.Worker.CacheManager().Get(ctx, record.CacheRefID, nil, cache.NoUpdateLastUsed)
		if err != nil {
			// the ref may be lazy or pruned, just skip it
			bklog.G(ctx).Debugf("skipping cache ref for export %s: %v", record.CacheRefID, err)
			continue
		}
		remotes, err := cacheRef.GetRemotes(ctx, true, cacheconfig.RefConfig{
			Compression: compression.Config{
				Type: compression.Zstd,
			},
		}, false, nil)
		if err != nil {
			return err
		}
		if len(remotes) == 0 {
			bklog.G(ctx).Errorf("skipping cache ref for export %s: no remotes", record.CacheRefID)
			continue
		}
		if len(remotes) > 1 {
			bklog.G(ctx).Debugf("multiple remotes for cache ref %s, using the first one", record.CacheRefID)
		}
		remote := remotes[0]
		for _, layer := range remote.Descriptors {
			if err := m.layerstore.PushLayer(ctx, layer, remote.Provider); err != nil {
				return err
			}
		}
		updatedRecords = append(updatedRecords, RecordLayers{
			RecordDigest: record.Digest,
			Layers:       remote.Descriptors,
		})
	}

	if err := m.client.UpdateCacheLayers(ctx, UpdateCacheLayersRequest{
		UpdatedRecords: updatedRecords,
	}); err != nil {
		return err
	}

	return nil
}

func (m *manager) Import(ctx context.Context) error {
	cacheConfig, err := m.client.ImportCache(ctx)
	if err != nil {
		return err
	}

	descProvider := remotecache.DescriptorProvider{}
	for _, layer := range cacheConfig.Layers {
		providerPair, err := m.descriptorProviderPair(layer)
		if err != nil {
			return err
		}
		descProvider[layer.Blob] = *providerPair
	}

	chain := remotecache.NewCacheChains()
	if err := remotecache.ParseConfig(*cacheConfig, descProvider, chain); err != nil {
		return err
	}

	keyStore, resultStore, err := remotecache.NewCacheKeyStorage(chain, m.Worker)
	if err != nil {
		return err
	}
	importedCache := solver.NewCacheManager(ctx, m.ID(), keyStore, resultStore)
	newInner := solver.NewCombinedCacheManager([]solver.CacheManager{importedCache}, m.localCache)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.inner = newInner
	return nil
}

func (m *manager) StartCacheMountSynchronization(ctx context.Context, daggerClient dagger.Client) error {
	stopSync, err := startS3CacheMountSync(ctx, m.s3Config, daggerClient)
	if err != nil {
		return err
	}
	m.stopCacheMountSync = stopSync
	return nil
}

// Close will block until the final export has finished or ctx is canceled.
func (m *manager) Close(ctx context.Context) error {
	close(m.startCloseCh)
	select {
	case <-m.doneCh:
	case <-ctx.Done():
	}
	return nil
}

func (m *manager) ID() string {
	return "enginecache"
}

func (m *manager) Query(inp []solver.CacheKeyWithSelector, inputIndex solver.Index, dgst digest.Digest, outputIndex solver.Index) ([]*solver.CacheKey, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.Query(inp, inputIndex, dgst, outputIndex)
}

func (m *manager) Records(ctx context.Context, ck *solver.CacheKey) ([]*solver.CacheRecord, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.Records(ctx, ck)
}

func (m *manager) Load(ctx context.Context, rec *solver.CacheRecord) (solver.Result, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.inner.Load(ctx, rec)
}

func (m *manager) Save(key *solver.CacheKey, s solver.Result, createdAt time.Time) (*solver.ExportableCacheKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.inner.Save(key, s, createdAt)
}

func (m *manager) descriptorProviderPair(layerMetadata remotecache.CacheLayer) (*remotecache.DescriptorProviderPair, error) {
	if layerMetadata.Annotations == nil {
		return nil, fmt.Errorf("missing annotations for layer %s", layerMetadata.Blob)
	}

	annotations := map[string]string{}
	if layerMetadata.Annotations.DiffID == "" {
		return nil, fmt.Errorf("missing diffID for layer %s", layerMetadata.Blob)
	}
	annotations["containerd.io/uncompressed"] = layerMetadata.Annotations.DiffID.String()
	if !layerMetadata.Annotations.CreatedAt.IsZero() {
		createdAt, err := layerMetadata.Annotations.CreatedAt.MarshalText()
		if err != nil {
			return nil, err
		}
		annotations["buildkit/createdat"] = string(createdAt)
	}
	return &remotecache.DescriptorProviderPair{
		Provider: m.layerstore,
		Descriptor: ocispecs.Descriptor{
			MediaType:   layerMetadata.Annotations.MediaType,
			Digest:      layerMetadata.Blob,
			Size:        layerMetadata.Annotations.Size,
			Annotations: annotations,
		},
	}, nil
}

type Manager interface {
	solver.CacheManager
	StartCacheMountSynchronization(context.Context, dagger.Client) error
	Close(context.Context) error
}

type defaultCacheManager struct {
	solver.CacheManager
}

func (defaultCacheManager) StartCacheMountSynchronization(ctx context.Context, client dagger.Client) error {
	return nil
}

func (defaultCacheManager) Close(context.Context) error {
	return nil
}
