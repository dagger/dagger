package cache

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/containerd/containerd/content"
	"github.com/moby/buildkit/cache"
	cacheconfig "github.com/moby/buildkit/cache/config"
	remotecache "github.com/moby/buildkit/cache/remotecache/v1"
	"github.com/moby/buildkit/solver"
	"github.com/moby/buildkit/solver/llbsolver/mounts"
	"github.com/moby/buildkit/util/bklog"
	"github.com/moby/buildkit/util/compression"
	"github.com/moby/buildkit/worker"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

type manager struct {
	ManagerConfig
	cacheClient   Service
	httpClient    *http.Client
	layerProvider content.Provider
	runtimeConfig Config
	localCache    solver.CacheManager

	mu           sync.RWMutex
	inner        solver.CacheManager
	startCloseCh chan struct{} // closed when shutdown should start
	doneCh       chan struct{} // closed when shutdown is complete

	cacheMountsInit   sync.Once
	syncedCacheMounts map[string]*syncedCacheMount
	seenCacheMounts   *sync.Map
}

type syncedCacheMount struct {
	init  sync.Once
	mount SyncedCacheMountConfig
}

type ManagerConfig struct {
	KeyStore     solver.CacheKeyStorage
	ResultStore  solver.CacheResultStorage
	Worker       worker.Worker
	MountManager *mounts.MountManager
	ServiceURL   string
	Token        string
	EngineID     string
	SyncOnBoot   bool
}

const (
	LocalCacheID            = "local"
	startupImportTimeout    = 1 * time.Minute
	backgroundImportTimeout = 10 * time.Minute
)

func NewManager(ctx context.Context, managerConfig ManagerConfig) (Manager, error) {
	localCache := solver.NewCacheManager(ctx, LocalCacheID, managerConfig.KeyStore, managerConfig.ResultStore)
	m := &manager{
		ManagerConfig: managerConfig,
		localCache:    localCache,
		startCloseCh:  make(chan struct{}),
		doneCh:        make(chan struct{}),
		httpClient:    &http.Client{},
	}

	if managerConfig.Token == "" {
		return defaultCacheManager{m.localCache}, nil
	}
	bklog.G(ctx).Debugf("using cache service at %s", managerConfig.ServiceURL)

	serviceClient, err := newClient(managerConfig.ServiceURL, managerConfig.Token)
	if err != nil {
		return nil, err
	}
	m.cacheClient = serviceClient
	m.layerProvider = &layerProvider{
		httpClient:  m.httpClient,
		cacheClient: m.cacheClient,
	}

	config, err := m.cacheClient.GetConfig(ctx, GetConfigRequest{
		EngineID: m.EngineID,
	})
	if err != nil {
		bklog.G(ctx).WithError(err).Warnf("cache init failed, falling back to local cache")
		return defaultCacheManager{m.localCache}, nil
	}
	if config.ImportPeriod == 0 || config.ExportPeriod == 0 || config.ExportTimeout == 0 {
		return nil, fmt.Errorf("invalid cache config: import/export periods must be non-zero")
	}
	m.runtimeConfig = *config

	importParentCtx, cancelImport := context.WithCancelCause(context.Background())
	go func() {
		<-m.startCloseCh
		cancelImport(errors.New("cache manager closing"))
	}()

	// do an initial synchronous import at start
	m.inner = m.localCache // start out with just the local cache, will be updated if Import succeeds
	startupImportCtx, startupImportCancel := context.WithTimeout(importParentCtx, startupImportTimeout)
	defer startupImportCancel()
	if err := m.Import(startupImportCtx); err != nil {
		// the first import failed, but we can continue with just the local cache to start and retry
		// importing in the background in the loop below
		bklog.G(ctx).WithError(err).Error("failed to import cache at startup")
	}

	// fetch the tenant's cache mount configuration with the list of cache mounts
	m.initSyncedCacheMounts(ctx)

	// if SyncOnBoot is enabled then we synchronize all cache mounts on boot
	if managerConfig.SyncOnBoot {
		bklog.G(ctx).Debug("synchronizing cache mounts on boot")
		start := time.Now()
		if err := m.syncAllCacheMounts(ctx); err != nil {
			bklog.G(ctx).WithError(err).Warn("(optional) cache mount synchronization on boot failed")
		} else {
			bklog.G(ctx).Debugf("finish cache mount synchronization in %s", time.Since(start))
		}
	}

	// loop for periodic async imports
	go func() {
		importTicker := time.NewTicker(config.ImportPeriod)
		defer importTicker.Stop()
		for {
			select {
			case <-importTicker.C:
			case <-m.startCloseCh:
				return
			}
			importContext, cancel := context.WithTimeout(importParentCtx, backgroundImportTimeout)
			if err := m.Import(importContext); err != nil {
				bklog.G(ctx).WithError(err).Error("failed to import cache")
			}
			cancel()
		}
	}()

	// loop for periodic async exports
	go func() {
		defer close(m.doneCh)
		var shutdown bool
		exportTicker := time.NewTicker(config.ExportPeriod)
		defer exportTicker.Stop()
		for {
			select {
			case <-exportTicker.C:
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
	bklog.G(ctx).Debug("starting cache export")
	cacheExportStart := time.Now()
	defer func() {
		bklog.G(ctx).Debugf("finished cache export in %s", time.Since(cacheExportStart))
	}()

	var cacheKeys []CacheKey
	var links []Link

	bklog.G(ctx).Debug("starting cache export key store walk")
	keyStoreWalkStart := time.Now()
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
				// The ref may be lazy or pruned, we'll just skip it, but if it's not found we can
				// also release the result from the key store to save work in the future.
				// The implementation of Release results in not only the result metadata to be cleared,
				// but also the key itself if it has no more results, any links associated with the key,
				// and (recursively) any keys that no longer have any links after removal of the links.
				// It's safe to do this while walking because all the Walk* methods in KeyStore are just
				// a no-op when called with an id that's not found, as opposed to returning an error.
				bklog.G(ctx).Debugf("skipping cache result %s for %s: %v", cacheResult.ID, id, err)

				// TODO: the error we want to match against is `errNotFound` in buildkit's cache
				// package, but that's not exported. Should modify upstream, in meantime have to
				// resort to string matching.
				if strings.HasSuffix(err.Error(), "not found") {
					if err := m.KeyStore.Release(cacheResult.ID); err != nil {
						bklog.G(ctx).WithError(err).Errorf("failed to release cache result %s", cacheResult.ID)
					}
				}
				return nil
			}
			defer res.Release(context.Background()) // TODO: hold on until later export?
			workerRef, ok := res.Sys().(*worker.WorkerRef)
			if !ok {
				bklog.G(ctx).Debugf("skipping cache result %s for %s: not an immutable ref", cacheResult.ID, id)
				return nil
			}
			cacheRef := workerRef.ImmutableRef
			if cacheRef == nil {
				bklog.G(ctx).Debugf("skipping cache result %s for %s: nil", cacheResult.ID, id)
				return nil
			}
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
	bklog.G(ctx).Debugf("finished cache export key store walk in %s", time.Since(keyStoreWalkStart))

	bklog.G(ctx).Debug("calling update cache records")
	updateCacheRecordsStart := time.Now()
	updateCacheRecordsResp, err := m.cacheClient.UpdateCacheRecords(ctx, UpdateCacheRecordsRequest{
		CacheKeys: cacheKeys,
		Links:     links,
	})
	if err != nil {
		return err
	}
	bklog.G(ctx).Debugf("finished update cache records call in %s", time.Since(updateCacheRecordsStart))

	recordsToExport := updateCacheRecordsResp.ExportRecords
	if len(recordsToExport) == 0 {
		bklog.G(ctx).Debug("no cache records to export")
		return nil
	}

	updatedRecords := make([]RecordLayers, 0, len(recordsToExport))
	pushLayersStart := time.Now()
	// keep track of what layers we've already pushed as they can show up multiple times
	// across different cache refs
	pushedLayers := make(map[string]struct{})
	for _, record := range recordsToExport {
		if err := func() error {
			bklog.G(ctx).Debugf("exporting cache ref %s", record.CacheRefID)
			exportCacheRefStart := time.Now()
			defer func() {
				bklog.G(ctx).Debugf("finished exporting cache ref %s in %s", record.CacheRefID, time.Since(exportCacheRefStart))
			}()

			cacheRef, err := m.Worker.CacheManager().Get(ctx, record.CacheRefID, nil, cache.NoUpdateLastUsed)
			if err != nil {
				// the ref may be lazy or pruned, just skip it
				bklog.G(ctx).Debugf("skipping cache ref for export %s: %v", record.CacheRefID, err)
				return nil
			}
			defer cacheRef.Release(context.Background())

			bklog.G(ctx).Debugf("getting remotes for cache ref %s", record.CacheRefID)
			getRemotesStart := time.Now()
			remotes, err := cacheRef.GetRemotes(ctx, true, cacheconfig.RefConfig{
				Compression: compression.Config{
					Type: compression.Zstd,
				},
			}, false, nil)
			if err != nil {
				return err
			}
			bklog.G(ctx).Debugf("finished getting remotes for cache ref %s in %s", record.CacheRefID, time.Since(getRemotesStart))

			if len(remotes) == 0 {
				bklog.G(ctx).Errorf("skipping cache ref for export %s: no remotes", record.CacheRefID)
				return nil
			}
			if len(remotes) > 1 {
				bklog.G(ctx).Debugf("multiple remotes for cache ref %s, using the first one", record.CacheRefID)
			}
			remote := remotes[0]

			bklog.G(ctx).Debugf("pushing layers for cache ref %s", record.CacheRefID)
			pushRefLayersStart := time.Now()
			for _, layer := range remote.Descriptors {
				if _, ok := pushedLayers[layer.Digest.String()]; ok {
					continue
				}
				if err := m.pushLayer(ctx, layer, remote.Provider); err != nil {
					return err
				}
				pushedLayers[layer.Digest.String()] = struct{}{}
			}
			bklog.G(ctx).Debugf("finished pushing layers for cache ref %s in %s", record.CacheRefID, time.Since(pushRefLayersStart))
			updatedRecords = append(updatedRecords, RecordLayers{
				RecordDigest: record.Digest,
				Layers:       remote.Descriptors,
			})
			return nil
		}(); err != nil {
			return err
		}
	}
	bklog.G(ctx).Debugf("finished pushing layers in %s", time.Since(pushLayersStart))

	bklog.G(ctx).Debugf("calling update cache layers")
	updateCacheLayersStart := time.Now()
	if err := m.cacheClient.UpdateCacheLayers(ctx, UpdateCacheLayersRequest{
		UpdatedRecords: updatedRecords,
	}); err != nil {
		return err
	}
	bklog.G(ctx).Debugf("finished update cache layers call in %s", time.Since(updateCacheLayersStart))

	return nil
}

func (m *manager) pushLayer(ctx context.Context, layerDesc ocispecs.Descriptor, provider content.Provider) error {
	bklog.G(ctx).Debugf("pushing layer %s", layerDesc.Digest)
	pushLayerStart := time.Now()

	var skipped bool
	defer func() {
		verbPrefix := "finished"
		if skipped {
			verbPrefix = "skipped"
		}

		bklog.G(ctx).Debugf("%s pushing layer %s in %s", verbPrefix, layerDesc.Digest, time.Since(pushLayerStart))
	}()

	getURLResp, err := m.cacheClient.GetLayerUploadURL(ctx, GetLayerUploadURLRequest{Digest: layerDesc.Digest})
	if err != nil {
		return err
	}

	if skipped = getURLResp.Skip; skipped {
		return nil
	}

	readerAt, err := provider.ReaderAt(ctx, layerDesc)
	if err != nil {
		return err
	}
	defer readerAt.Close()
	reader := content.NewReader(readerAt)

	req, err := http.NewRequest("PUT", getURLResp.URL, reader)
	if err != nil {
		return err
	}
	defer req.Body.Close()
	req.ContentLength = readerAt.Size()
	for k, v := range getURLResp.Headers {
		req.Header.Set(k, v)
	}

	resp, err := m.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if err := checkResponse(resp); err != nil {
		return err
	}
	return nil
}

func (m *manager) Import(ctx context.Context) error {
	bklog.G(ctx).Debug("importing cache")
	importCacheStart := time.Now()
	defer func() {
		bklog.G(ctx).Debugf("finished importing cache in %s", time.Since(importCacheStart))
	}()

	bklog.G(ctx).Debug("calling import cache")
	importCacheCallStart := time.Now()
	cacheConfig, err := m.cacheClient.ImportCache(ctx)
	if err != nil {
		return err
	}
	bklog.G(ctx).Debugf("finished import cache call in %s", time.Since(importCacheCallStart))

	bklog.G(ctx).Debug("creating descriptor provider pairs")
	createDescProviderPairsStart := time.Now()
	descProvider := remotecache.DescriptorProvider{}
	for _, layer := range cacheConfig.Layers {
		providerPair, err := m.descriptorProviderPair(layer)
		if err != nil {
			return err
		}
		descProvider[layer.Blob] = *providerPair
	}
	bklog.G(ctx).Debugf("finished creating descriptor provider pairs in %s", time.Since(createDescProviderPairsStart))

	bklog.G(ctx).Debug("parsing cache config")
	parseCacheConfigStart := time.Now()
	chain := remotecache.NewCacheChains()
	if err := remotecache.ParseConfig(*cacheConfig, descProvider, chain); err != nil {
		return err
	}
	bklog.G(ctx).Debugf("finished parsing cache config in %s", time.Since(parseCacheConfigStart))

	keyStore, resultStore, err := remotecache.NewCacheKeyStorage(chain, m.Worker)
	if err != nil {
		return err
	}
	importedCache := solver.NewCacheManager(ctx, m.ID()+"-import", keyStore, resultStore)
	newInner := solver.NewCombinedCacheManager([]solver.CacheManager{m.localCache, importedCache}, m.localCache)

	m.mu.Lock()
	defer m.mu.Unlock()
	m.inner = newInner
	return nil
}

// Close will block until the final export has finished or ctx is canceled.
func (m *manager) Close(ctx context.Context) (rerr error) {
	close(m.startCloseCh)
	m.UploadCacheMounts(ctx)
	select {
	case <-m.doneCh:
	case <-ctx.Done():
	}
	return rerr
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

func (m *manager) ReleaseUnreferenced(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	// this method isn't in the solver.CacheManager interface (this is how buildkit calls it upstream too)
	if c, ok := m.localCache.(interface {
		ReleaseUnreferenced(context.Context) error
	}); ok {
		return c.ReleaseUnreferenced(ctx)
	}
	return nil
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
	desc := ocispecs.Descriptor{
		MediaType:   layerMetadata.Annotations.MediaType,
		Digest:      layerMetadata.Blob,
		Size:        layerMetadata.Annotations.Size,
		Annotations: annotations,
	}
	return &remotecache.DescriptorProviderPair{
		Provider:   m.layerProvider,
		Descriptor: desc,
	}, nil
}

type Manager interface {
	solver.CacheManager
	DownloadCacheMounts(context.Context, []string) error
	UploadCacheMounts(context.Context) error
	ReleaseUnreferenced(context.Context) error
	Close(context.Context) error
}

type defaultCacheManager struct {
	solver.CacheManager
}

var _ Manager = defaultCacheManager{}

func (defaultCacheManager) DownloadCacheMounts(ctx context.Context, _ []string) error {
	return nil
}

func (defaultCacheManager) UploadCacheMounts(ctx context.Context) error {
	return nil
}

func (c defaultCacheManager) ReleaseUnreferenced(ctx context.Context) error {
	// this method isn't in the solver.CacheManager interface (this is how buildkit calls it upstream too)
	if c, ok := c.CacheManager.(interface {
		ReleaseUnreferenced(context.Context) error
	}); ok {
		return c.ReleaseUnreferenced(ctx)
	}
	return nil
}

func (defaultCacheManager) Close(context.Context) error {
	return nil
}
