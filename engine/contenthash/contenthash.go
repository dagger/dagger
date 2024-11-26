package contenthash

import (
	"context"

	"github.com/moby/buildkit/cache"
	"github.com/opencontainers/go-digest"
)

const (
	keyContentHashKey = "dagger.contentHashKey"
	contentHashIndex  = keyContentHashKey + ":"
)

func SearchContentHash(ctx context.Context, store cache.MetadataStore, dgst digest.Digest) ([]CacheRefMetadata, error) {
	var results []CacheRefMetadata
	mds, err := store.Search(ctx, contentHashIndex+dgst.Encoded(), false)
	if err != nil {
		return nil, err
	}
	for _, md := range mds {
		results = append(results, CacheRefMetadata{md})
	}
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
