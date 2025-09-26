package core

import (
	"context"
	"fmt"

	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/opencontainers/go-digest"
)

type cacheRefMetadata struct {
	bkcache.RefMetadata
}

// searchHTTPByContentDigest searches for cache entries by their content digest
func SearchHTTPByContentDigest(ctx context.Context, cache bkcache.Accessor, contentDigest digest.Digest) (bkcache.ImmutableRef, error) {
	// We can't directly search by content digest since it's not indexed,
	// but we can search for all HTTP entries and check their content digests
	//
	// First, try to get all HTTP cache entries by searching with empty key
	// This will match all entries that have the HTTP index
	mds, err := cache.Search(ctx, indexHTTP, false)
	if err != nil {
		// If that doesn't work, we need to be more creative
		// Try searching for common URL patterns
		searches := []string{
			indexHTTP + "http://",
			indexHTTP + "https://",
		}

		var allMds []bkcache.RefMetadata
		for _, search := range searches {
			if results, err := cache.Search(ctx, search, true); err == nil {
				allMds = append(allMds, results...)
			}
		}
		mds = allMds
	}

	// Check each metadata entry for matching content digest
	for _, md := range mds {
		cmdMd := cacheRefMetadata{md}
		if cmdMd.getHTTPChecksum() == contentDigest {
			// Found a match!
			ref, err := cache.Get(ctx, md.ID(), nil)
			if err != nil {
				// Cache entry exists but can't load it, continue searching
				continue
			}
			return ref, nil
		}
	}

	return nil, fmt.Errorf("content digest not found in cache: %s", contentDigest)
}

func searchRefMetadata(ctx context.Context, store bkcache.MetadataStore, key string, idx string) ([]cacheRefMetadata, error) {
	mds, err := store.Search(ctx, idx+key, false)
	if err != nil {
		return nil, err
	}
	results := make([]cacheRefMetadata, len(mds))
	for i, md := range mds {
		results[i] = cacheRefMetadata{md}
	}
	return results, nil
}

const keyGitRemote = "git-remote"
const keyGitSnapshot = "git-snapshot"
const indexGitRemote = keyGitRemote + "::"
const indexGitSnapshot = keyGitSnapshot + "::"

func searchGitRemote(ctx context.Context, store bkcache.MetadataStore, remote string) ([]cacheRefMetadata, error) {
	return searchRefMetadata(ctx, store, remote, indexGitRemote)
}
func searchGitSnapshot(ctx context.Context, store bkcache.MetadataStore, key string) ([]cacheRefMetadata, error) {
	return searchRefMetadata(ctx, store, key, indexGitSnapshot)
}

func (md cacheRefMetadata) setGitSnapshot(key string) error {
	return md.SetString(keyGitSnapshot, key, indexGitSnapshot+key)
}
func (md cacheRefMetadata) setGitRemote(key string) error {
	return md.SetString(keyGitRemote, key, indexGitRemote+key)
}

const keyHTTP = "http.url"
const keyHTTPChecksum = "http.checksum"
const keyHTTPETag = "http.etag"
const keyHTTPModTime = "http.modtime"
const indexHTTP = keyHTTP + "::"

func searchHTTPByDigest(ctx context.Context, store bkcache.MetadataStore, urlDigest digest.Digest) ([]cacheRefMetadata, error) {
	return searchRefMetadata(ctx, store, string(urlDigest), indexHTTP)
}

func (md cacheRefMetadata) getHTTPChecksum() digest.Digest {
	return digest.Digest(md.GetString(keyHTTPChecksum))
}
func (md cacheRefMetadata) setHTTPChecksum(urlDgst digest.Digest, d digest.Digest) error {
	return md.SetString(keyHTTPChecksum, d.String(), indexHTTP+urlDgst.String())
}

func (md cacheRefMetadata) getETag() string {
	return md.GetString(keyHTTPETag)
}
func (md cacheRefMetadata) setETag(s string) error {
	return md.SetString(keyHTTPETag, s, "")
}

func (md cacheRefMetadata) getHTTPModTime() string {
	return md.GetString(keyHTTPModTime)
}
func (md cacheRefMetadata) setHTTPModTime(s string) error {
	return md.SetString(keyHTTPModTime, s, "")
}
