package contenthash

import (
	"context"

	cache "github.com/dagger/dagger/engine/snapshots"
	telemetry "github.com/dagger/otel-go"
	"github.com/opencontainers/go-digest"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

const (
	keyContentHashKey = "dagger.contentHashKey"
	contentHashIndex  = keyContentHashKey + ":"
)

func SearchContentHash(ctx context.Context, store cache.MetadataStore, dgst digest.Digest) (_ []CacheRefMetadata, rerr error) {
	ctx, span := Tracer(ctx).Start(ctx, "contenthash.search", trace.WithAttributes(
		attribute.String("contenthash.digest", dgst.String()),
	))
	defer telemetry.EndWithCause(span, &rerr)

	var results []CacheRefMetadata
	mds, err := store.Search(ctx, contentHashIndex+dgst.Encoded(), false)
	if err != nil {
		return nil, err
	}
	for _, md := range mds {
		results = append(results, CacheRefMetadata{md})
	}
	span.SetAttributes(attribute.Int("contenthash.result_count", len(results)))
	return results, nil
}

type CacheRefMetadata struct {
	cache.RefMetadata
}

func (md CacheRefMetadata) GetContentHashKey() (digest.Digest, bool) {
	dgstStr := md.GetString(keyContentHashKey)
	if dgstStr == "" {
		return "", false
	}
	return digest.Digest(string(digest.Canonical) + ":" + dgstStr), true
}

func (md CacheRefMetadata) SetContentHashKey(dgst digest.Digest) error {
	return md.SetString(keyContentHashKey, dgst.Encoded(), contentHashIndex+dgst.Encoded())
}
