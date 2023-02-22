package remotecache

import (
	"context"
	"os"
	"strings"

	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/remotes/docker"
	"github.com/dagger/dagger/internal/engine"
	"github.com/moby/buildkit/cache/remotecache"
	"github.com/moby/buildkit/cache/remotecache/azblob"
	"github.com/moby/buildkit/cache/remotecache/gha"
	registryremotecache "github.com/moby/buildkit/cache/remotecache/registry"
	s3remotecache "github.com/moby/buildkit/cache/remotecache/s3"
	"github.com/moby/buildkit/session"
	"github.com/moby/buildkit/util/bklog"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
	"github.com/pkg/errors"
)

func ResolveCacheExporterFunc(sm *session.Manager, resolverFn docker.RegistryHosts) remotecache.ResolveCacheExporterFunc {
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
			impl, err = s3remotecache.ResolveCacheExporterFunc()(ctx, g, attrs)
		case "azblob":
			impl, err = azblob.ResolveCacheExporterFunc()(ctx, g, attrs)
		default:
			bklog.G(ctx).Debugf("unsupported cache type %s, defaulting export off", cacheType)
			// leaving impl nil will cause buildkit to not export cache
		}
		if err != nil {
			return nil, err
		}
		return impl, nil
	}
}

func ResolveCacheImporterFunc(sm *session.Manager, cs content.Store, hosts docker.RegistryHosts) remotecache.ResolveCacheImporterFunc {
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
			impl, desc, err = s3remotecache.ResolveCacheImporterFunc()(ctx, g, attrs)
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
