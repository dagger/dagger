package contenthash

import (
	"context"
	"errors"
	"testing"

	"github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/hashicorp/golang-lru/v2/simplelru"
	"github.com/moby/locker"
	"github.com/opencontainers/go-digest"
)

type testMetadata struct {
	cache.RefMetadata
	id       string
	external map[string][]byte
	writeErr error
}

func (m *testMetadata) ID() string { return m.id }
func (m *testMetadata) GetExternal(key string) ([]byte, error) {
	return m.external[key], nil
}
func (m *testMetadata) SetExternal(key string, contents []byte) error {
	if m.writeErr != nil {
		return m.writeErr
	}
	m.external[key] = append([]byte(nil), contents...)
	return nil
}

func TestSetCacheContextPreservesDigestAfterEviction(t *testing.T) {
	ctx := context.Background()
	lru, err := simplelru.NewLRU[string, *cacheContext](1, nil)
	if err != nil {
		t.Fatal(err)
	}
	manager := &cacheManager{locker: locker.New(), lru: lru}
	origin := &testMetadata{id: "mutable", external: map[string][]byte{}}
	source, err := newCacheContext(origin)
	if err != nil {
		t.Fatal(err)
	}
	// An import has already computed this digest. Rebinding it to a
	// committed snapshot must preserve it even after the in-memory tree goes.
	expected := digest.FromString("imported package contents")
	source.txn = source.tree.Txn()
	source.txn.Insert(convertPathToKey("/pkg"), &CacheRecord{Type: CacheRecordTypeDir, Digest: expected})
	destination := &testMetadata{id: "immutable", external: map[string][]byte{}}
	if err := manager.SetCacheContext(ctx, destination, source); err != nil {
		t.Fatal(err)
	}
	if len(destination.external[keyContentHash]) == 0 {
		t.Fatal("committed snapshot has no persisted content hashes")
	}
	other := &testMetadata{id: "other", external: map[string][]byte{}}
	if _, err := manager.GetCacheContext(ctx, other); err != nil {
		t.Fatal(err)
	}
	if manager.lru.Contains(destination.ID()) {
		t.Fatal("test did not evict the committed snapshot")
	}
	reloaded, err := manager.GetCacheContext(ctx, destination)
	if err != nil {
		t.Fatal(err)
	}
	actual, err := reloaded.Checksum(ctx, nil, "/pkg", ChecksumOpts{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("digest changed after eviction: got %s, want %s", actual, expected)
	}
}

func TestSetCacheContextReturnsPersistenceError(t *testing.T) {
	ctx := context.Background()
	source, err := newCacheContext(&testMetadata{id: "mutable", external: map[string][]byte{}})
	if err != nil {
		t.Fatal(err)
	}
	want := errors.New("metadata write failed")
	err = getDefaultManager().SetCacheContext(ctx, &testMetadata{id: "immutable", writeErr: want}, source)
	if !errors.Is(err, want) {
		t.Fatalf("got %v, want persistence error", err)
	}
}
