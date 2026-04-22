package core

import (
	"context"

	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/opencontainers/go-digest"
)

type cacheRefMetadata struct {
	bkcache.RefMetadata
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
