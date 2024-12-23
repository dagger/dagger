package schema

import (
	"context"
	"fmt"

	"github.com/opencontainers/go-digest"
	"github.com/zeebo/xxh3"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

const (
	XXH3 digest.Algorithm = "xxh3"
)

// CachePerClient is a CacheKeyFunc that scopes the cache key to the client by mixing in the client ID to the original digest of the operation.
// It should be used when the operation should be run for each client, but not more than once for a given client.
// Canonical examples include loading client filesystem data or referencing client-side sockets/ports.
func CachePerClient[P dagql.Typed, A any](ctx context.Context, _ dagql.Instance[P], _ A, origDgst digest.Digest) (digest.Digest, error) {
	// scope the cache key to the client by mixing in the client ID to the original digest
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get client metadata: %w", err)
	}
	if clientMD.ClientID == "" {
		return "", fmt.Errorf("client ID not found in context")
	}
	return hashFrom(origDgst.String(), clientMD.ClientID), nil
}

func hashFrom(ins ...string) digest.Digest {
	h := xxh3.New()
	for _, in := range ins {
		h.WriteString(in)
	}
	return digest.NewDigest(XXH3, h)
}
