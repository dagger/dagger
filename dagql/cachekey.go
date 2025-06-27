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

// CachePerSession is a CacheKeyFunc that scopes the cache key to the session by mixing in the session ID to the original digest of the operation.
// It should be used when the operation should be run for each session, but not more than once for a given session.
func CachePerSession[P Typed, A any](
	ctx context.Context,
	inst Instance[P],
	args A,
	cacheCfg CacheConfig,
) (*CacheConfig, error) {
	return CachePerSessionObject(ctx, inst, args, cacheCfg)
}

// CachePerSessionObject is the same as CachePerSession but when you have a dagql.Object instead of a dagql.Instance.
func CachePerSessionObject[A any](
	ctx context.Context,
	_ Object,
	_ A,
	cacheCfg CacheConfig,
) (*CacheConfig, error) {
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get client metadata: %w", err)
	}
	if clientMD.SessionID == "" {
		return nil, fmt.Errorf("session ID not found in context")
	}

	cacheCfg.Digest = HashFrom(cacheCfg.Digest.String(), clientMD.SessionID)
	return &cacheCfg, nil
}

// this could all be un-generic'd and repeated per-API. might be cleaner at the end of the day.
type CacheControllableArgs interface {
	CacheType() CacheControlType
}

type CacheControlType int

const (
	CacheTypeUnset CacheControlType = iota
	CacheTypePerClient
	CacheTypePerCall
)

func CacheAsRequested[T Typed, A CacheControllableArgs](ctx context.Context, i Instance[T], a A, cc CacheConfig) (*CacheConfig, error) {
	switch a.CacheType() {
	case CacheTypePerClient:
		return CachePerClient(ctx, i, a, cc)
	case CacheTypePerCall:
		return CachePerCall(ctx, i, a, cc)
	default:
		return &cc, nil
	}
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

// CachePerSchema is a CacheKeyFunc that scopes the cache key to the schema of
// the provided server.
//
// This should be used only in scenarios where literally the schema is all that
// determines the result, irrespective of what client is making the call.
func CachePerSchema[P Typed, A any](srv *Server) func(context.Context, Instance[P], A, CacheConfig) (*CacheConfig, error) {
	return func(
		ctx context.Context,
		_ Instance[P],
		_ A,
		cfg CacheConfig,
	) (*CacheConfig, error) {
		cfg.Digest = HashFrom(
			cfg.Digest.String(),
			srv.SchemaDigest().String(),
		)
		return &cfg, nil
	}
}

// CachePerClientSchema is a CacheKeyFunc that scopes the cache key to both the
// client and the current schema of the provided server.
//
// This should be used by anything that should invalidate when the schema
// changes, but also has an element of per-client dynamism.
func CachePerClientSchema[P Typed, A any](srv *Server) func(context.Context, Instance[P], A, CacheConfig) (*CacheConfig, error) {
	return func(
		ctx context.Context,
		_ Instance[P],
		_ A,
		cfg CacheConfig,
	) (*CacheConfig, error) {
		clientMD, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to get client metadata: %w", err)
		}
		if clientMD.ClientID == "" {
			return nil, fmt.Errorf("client ID not found in context")
		}
		cfg.Digest = HashFrom(
			cfg.Digest.String(),
			srv.SchemaDigest().String(),
			clientMD.ClientID,
		)
		return &cfg, nil
	}
}

func HashFrom(ins ...string) digest.Digest {
	h := xxh3.New()
	for _, in := range ins {
		h.WriteString(in)
		h.Write([]byte{0}) // separate all inputs with a null byte to help avoid collisions
	}
	return digest.NewDigest(XXH3, h)
}
