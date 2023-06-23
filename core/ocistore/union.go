package ocistore

import (
	"context"
	"fmt"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/opencontainers/go-digest"
	ocispecs "github.com/opencontainers/image-spec/specs-go/v1"
)

// UnionStore is a content.Store that delegates to other content.Stores.
type UnionStore struct {
	stores  map[digest.Digest]content.Store
	storesL sync.Mutex

	unimplementedStore
}

// NewUnionStore constructs a new empty union store.
func NewUnionStore() *UnionStore {
	return &UnionStore{
		stores: make(map[digest.Digest]content.Store),
	}
}

var _ content.Store = (*UnionStore)(nil)

// Info delegates to the content.Store that has the requested digest.
func (c *UnionStore) Info(ctx context.Context, dgst digest.Digest) (content.Info, error) {
	store, found := c.stores[dgst]
	if !found {
		return content.Info{}, fmt.Errorf("Info: digest not found: %s", dgst)
	}

	return store.Info(ctx, dgst)
}

// ReaderAt delegates to the content.Store that has the requested digest.
func (c *UnionStore) ReaderAt(ctx context.Context, desc ocispecs.Descriptor) (content.ReaderAt, error) {
	store, found := c.stores[desc.Digest]
	if !found {
		return nil, fmt.Errorf("ReaderAt: digest not found: %s", desc.Digest)
	}

	return store.ReaderAt(ctx, desc)
}

// Install walks over all blob digests available in store and maps them to the
// store in the union.
func (c *UnionStore) Install(ctx context.Context, store content.Store) error {
	return store.Walk(ctx, func(info content.Info) error {
		c.storesL.Lock()
		c.stores[info.Digest] = store
		c.storesL.Unlock()
		return nil
	})
}
