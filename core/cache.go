package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"

	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// CacheVolume is a persistent volume with a globally scoped identifier.
type CacheVolume struct {
	Keys []string

	mu        sync.Mutex
	snapshots map[string]bkcache.ImmutableRef
}

func (*CacheVolume) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CacheVolume",
		NonNull:   true,
	}
}

func (*CacheVolume) TypeDescription() string {
	return "A directory whose contents persist across runs."
}

func NewCache(keys ...string) *CacheVolume {
	return &CacheVolume{Keys: keys}
}

var _ dagql.OnReleaser = (*CacheVolume)(nil)

func (cache *CacheVolume) OnRelease(ctx context.Context) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	var errs error
	for _, ref := range cache.snapshots {
		errs = errors.Join(errs, ref.Release(ctx))
	}
	clear(cache.snapshots)
	return errs
}

func (cache *CacheVolume) Clone() *CacheVolume {
	cp := *cache
	cp.Keys = slices.Clone(cp.Keys)
	cp.mu = sync.Mutex{}
	cp.snapshots = nil
	return &cp
}

// Sum returns a checksum of the cache tokens suitable for use as a cache key.
func (cache *CacheVolume) Sum() string {
	hash := sha256.New()
	for _, tok := range cache.Keys {
		_, _ = hash.Write([]byte(tok + "\x00"))
	}

	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
}

func (cache *CacheVolume) snapshotForMount(mountID string) (bkcache.ImmutableRef, bool) {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cache.snapshots == nil {
		cache.snapshots = map[string]bkcache.ImmutableRef{}
	}
	snapshot, ok := cache.snapshots[mountID]
	return snapshot, ok
}

func (cache *CacheVolume) setSnapshotForMount(ctx context.Context, mountID string, ref bkcache.ImmutableRef) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cache.snapshots == nil {
		cache.snapshots = map[string]bkcache.ImmutableRef{}
	}

	prev := cache.snapshots[mountID]
	cache.snapshots[mountID] = ref

	if prev != nil {
		return prev.Release(ctx)
	}
	return nil
}

type CacheSharingMode string

var CacheSharingModes = dagql.NewEnum[CacheSharingMode]()

var (
	CacheSharingModeShared = CacheSharingModes.Register("SHARED",
		"Shares the cache volume amongst many build pipelines")
	CacheSharingModePrivate = CacheSharingModes.Register("PRIVATE",
		"Keeps a cache volume for a single build pipeline")
	CacheSharingModeLocked = CacheSharingModes.Register("LOCKED",
		"Shares the cache volume amongst many build pipelines, but will serialize the writes")
)

func (mode CacheSharingMode) Type() *ast.Type {
	return &ast.Type{
		NamedType: "CacheSharingMode",
		NonNull:   true,
	}
}

func (mode CacheSharingMode) TypeDescription() string {
	return "Sharing mode of the cache volume."
}

func (mode CacheSharingMode) Decoder() dagql.InputDecoder {
	return CacheSharingModes
}

func (mode CacheSharingMode) ToLiteral() call.Literal {
	return CacheSharingModes.Literal(mode)
}

// CacheSharingMode marshals to its lowercased value.
//
// NB: as far as I can recall this is purely for ~*aesthetic*~. GraphQL consts
// are so shouty!
func (mode CacheSharingMode) MarshalJSON() ([]byte, error) {
	return json.Marshal(strings.ToLower(string(mode)))
}

// CacheSharingMode marshals to its lowercased value.
//
// NB: as far as I can recall this is purely for ~*aesthetic*~. GraphQL consts
// are so shouty!
func (mode *CacheSharingMode) UnmarshalJSON(payload []byte) error {
	var str string
	if err := json.Unmarshal(payload, &str); err != nil {
		return err
	}

	*mode = CacheSharingMode(strings.ToUpper(str))

	return nil
}
