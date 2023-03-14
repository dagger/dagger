package remotecache

import (
	"context"
	"os"
	"strings"

	"dagger.io/dagger"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/leases"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/dagger/dagger/internal/engine"
	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/cache/remotecache/azblob"
	"github.com/moby/buildkit/cache/remotecache/gha"
	registryremotecache "github.com/moby/buildkit/cache/remotecache/registry"
	"github.com/moby/buildkit/cache/remotecache/s3"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func StartDaggerCache(ctx context.Context, sm *session.Manager, cs content.Store, lm leases.Manager, hosts docker.RegistryHosts) (remotecache.ResolveCacheExporterFunc, remotecache.ResolveCacheImporterFunc, <-chan struct{}, error) {
	cacheType, attrs, err := cacheConfigFromEnv()
	if err != nil {
		return nil, nil, nil, err
	}

	doneCh := make(chan struct{}, 1)
	var s3Manager *s3CacheManager
	if cacheType == experimentalDaggerS3CacheType {
		s3Manager, err = newS3CacheManager(ctx, attrs, lm, doneCh)
		if err != nil {
			return nil, nil, nil, err
		}
	}
	return resolveCacheExporterFunc(sm, hosts, s3Manager), resolveCacheImporterFunc(sm, cs, hosts, s3Manager), doneCh, nil
}

func resolveCacheExporterFunc(sm *session.Manager, resolverFn docker.RegistryHosts, s3Manager *s3CacheManager) remotecache.ResolveCacheExporterFunc {
	return func(ctx context.Context, g session.Group, userAttrs map[string]string) (remotecache.Exporter, error) {
		cacheType, attrs, err := cacheConfigFromEnv()
		if err != nil {
			return nil, err
		}
		var impl remotecache.Exporter
		switch cacheType {
		case "registry":
			impl, err = registryremotecache.ResolveCacheExporterFunc(sm, resolverFn)(ctx, g, attrs)
		case "gha":
			impl, err = gha.ResolveCacheExporterFunc()(ctx, g, attrs)
		case "s3":
			impl, err = s3.ResolveCacheExporterFunc()(ctx, g, attrs)
		case experimentalDaggerS3CacheType:
			impl = newS3CacheExporter(s3Manager)
		case "azblob":
			impl, err = azblob.ResolveCacheExporterFunc()(ctx, g, attrs)
		default:
			bklog.G(ctx).Debugf("unsupported cache type %s, defaulting export off", cacheType)
			// leaving impl nil will cause buildkit to not export cache
		}
		if err != nil {
			return nil, err
		}
		if userAttrs != nil {
			userAttrs["mode"] = attrs["mode"]
		}
		return impl, nil
	}
}

func resolveCacheImporterFunc(sm *session.Manager, cs content.Store, hosts docker.RegistryHosts, s3Manager *s3CacheManager) remotecache.ResolveCacheImporterFunc {
	return func(ctx context.Context, g session.Group, userAttrs map[string]string) (remotecache.Importer, ocispecs.Descriptor, error) {
		cacheType, attrs, err := cacheConfigFromEnv()
		if err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
		var impl remotecache.Importer
		var desc ocispecs.Descriptor
		switch cacheType {
		case "registry":
			impl, desc, err = registryremotecache.ResolveCacheImporterFunc(sm, cs, hosts)(ctx, g, attrs)
		case "gha":
			impl, desc, err = gha.ResolveCacheImporterFunc()(ctx, g, attrs)
		case "s3":
			impl, desc, err = s3.ResolveCacheImporterFunc()(ctx, g, attrs)
		case experimentalDaggerS3CacheType:
			impl = s3Manager
		case "azblob":
			impl, desc, err = azblob.ResolveCacheImporterFunc()(ctx, g, attrs)
		default:
			bklog.G(ctx).Debugf("unsupported cache type %s, defaulting to noop", cacheType)
			impl = &noopImporter{}
		}
		if err != nil {
			return nil, ocispecs.Descriptor{}, err
		}
		return impl, desc, nil
	}
}

func StartCacheMountSynchronization(ctx context.Context, daggerClient *dagger.Client) (func(ctx context.Context) error, error) {
	stop := func(ctx context.Context) error { return nil } // default to no-op
	cacheType, attrs, err := cacheConfigFromEnv()
	if err != nil {
		return stop, err
	}
	switch cacheType {
	case "experimental_dagger_s3":
		stop, err = startS3CacheMountSync(ctx, attrs, daggerClient)
	default:
		bklog.G(ctx).Debugf("unsupported cache type %s, defaulting to no cache mount synchronization", cacheType)
	}
	return stop, err
}

func cacheConfigFromEnv() (string, map[string]string, error) {
	envVal, ok := os.LookupEnv(engine.CacheConfigEnvName)
	if !ok {
		return "", nil, nil
	}

	// env is in form k1=v1,k2=v2,...
	kvs := strings.Split(envVal, ",")
	if len(kvs) == 0 {
		return "", nil, nil
	}
	attrs := make(map[string]string)
	for _, kv := range kvs {
		parts := strings.SplitN(kv, "=", 2)
		if len(parts) != 2 {
			return "", nil, errors.Errorf("invalid form for cache config %q", kv)
		}
		attrs[parts[0]] = parts[1]
	}
	typeVal, ok := attrs["type"]
	if !ok {
		return "", nil, errors.Errorf("missing type in cache config: %q", envVal)
	}
	delete(attrs, "type")
	return typeVal, attrs, nil
}
