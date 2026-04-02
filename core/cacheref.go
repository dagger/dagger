package core

import (
	"context"

	bkcache "github.com/dagger/dagger/engine/snapshots"
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
