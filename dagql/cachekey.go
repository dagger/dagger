package dagql

import (
	"context"
	"fmt"

	"github.com/moby/buildkit/identity"
	"github.com/opencontainers/go-digest"
	"github.com/zeebo/xxh3"

	"github.com/dagger/dagger/engine"
)

const (
	XXH3 digest.Algorithm = "xxh3"
)

// CachePerClient is a CacheKeyFunc that scopes the cache key to the client by mixing in the client ID to the original digest of the operation.
// It should be used when the operation should be run for each client, but not more than once for a given client.
// Canonical examples include loading client filesystem data or referencing client-side sockets/ports.
func CachePerClient[P Typed, A any](
	ctx context.Context,
	inst Instance[P],
	args A,
	cacheCfg CacheConfig,
) (*CacheConfig, error) {
	return CachePerClientObject(ctx, inst, args, cacheCfg)
}

// CachePerClientObject is the same as CachePerClient but when you have a dagql.Object instead of a dagql.Instance.
func CachePerClientObject[A any](
	ctx context.Context,
	_ Object,
	_ A,
	cacheCfg CacheConfig,
) (*CacheConfig, error) {
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	if clientMD.ClientID == "" {
		return nil, fmt.Errorf("client ID not found in context")
	}

	cacheCfg.Digest = HashFrom(cacheCfg.Digest.String(), clientMD.ClientID)
	return &cacheCfg, nil
}

// CachePerCall results in the API always running when called, but the returned result from that call is cached.
// For instance, the API may return a snapshot of some live mutating state; in that case the first call to get the snapshot
// should always run but if the returned object is passed around it should continue to be that snapshot rather than the API
// always re-running.
func CachePerCall[P Typed, A any](
	_ context.Context,
	_ Instance[P],
	_ A,
	cacheCfg CacheConfig,
) (*CacheConfig, error) {
	randID := identity.NewID()
	cacheCfg.Digest = HashFrom(randID)
	return &cacheCfg, nil
}

func HashFrom(ins ...string) digest.Digest {
	h := xxh3.New()
	for _, in := range ins {
		h.WriteString(in)
	}
	return digest.NewDigest(XXH3, h)
}
