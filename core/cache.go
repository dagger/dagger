package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	bkcache "github.com/dagger/dagger/engine/snapshots"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
)

// CacheVolume is a persistent volume with a globally scoped identifier.
type CacheVolume struct {
	Key       string
	Namespace string
	Source    dagql.Optional[DirectoryID]
	Sharing   CacheSharingMode
	Owner     string

	mu       sync.Mutex
	snapshot bkcache.MutableRef
	selector string

	persistedResultID uint64
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

func NewCache(
	key string,
	namespace string,
	source dagql.Optional[DirectoryID],
	sharing CacheSharingMode,
	owner string,
) *CacheVolume {
	return &CacheVolume{
		Key:       key,
		Namespace: namespace,
		Source:    source,
		Sharing:   sharing,
		Owner:     owner,
	}
}

var _ dagql.OnReleaser = (*CacheVolume)(nil)

func (cache *CacheVolume) OnRelease(ctx context.Context) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cache.snapshot == nil {
		return nil
	}
	err := cache.snapshot.Release(ctx)
	cache.snapshot = nil
	return err
}

func (cache *CacheVolume) getSnapshot() bkcache.MutableRef {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	return cache.snapshot
}

func (cache *CacheVolume) getSnapshotSelector() string {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.selector == "" {
		return "/"
	}
	return cache.selector
}

func (cache *CacheVolume) CacheUsageSize(ctx context.Context) (int64, bool, error) {
	snapshot := cache.getSnapshot()
	if snapshot == nil {
		return 0, false, nil
	}
	size, err := snapshot.Size(ctx)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

func (cache *CacheVolume) CacheUsageIdentity() (string, bool) {
	snapshot := cache.getSnapshot()
	if snapshot == nil {
		return "", false
	}
	return snapshot.ID(), true
}

func (cache *CacheVolume) PersistedResultID() uint64 {
	if cache == nil {
		return 0
	}
	return cache.persistedResultID
}

func (cache *CacheVolume) SetPersistedResultID(resultID uint64) {
	if cache != nil {
		cache.persistedResultID = resultID
	}
}

func (cache *CacheVolume) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	snapshot := cache.getSnapshot()
	if snapshot == nil {
		return nil
	}
	return []dagql.PersistedSnapshotRefLink{
		{
			RefKey: snapshot.SnapshotID(),
			Role:   "snapshot",
			Slot:   cache.getSnapshotSelector(),
		},
	}
}

type persistedCacheVolumePayload struct {
	Key       string           `json:"key"`
	Namespace string           `json:"namespace,omitempty"`
	Sharing   CacheSharingMode `json:"sharing,omitempty"`
	Owner     string           `json:"owner,omitempty"`
}

func (cache *CacheVolume) EncodePersistedObject(ctx context.Context, persistedCache dagql.PersistedObjectCache) (json.RawMessage, error) {
	_ = ctx
	_ = persistedCache
	if cache == nil {
		return nil, fmt.Errorf("encode persisted cache volume: nil cache volume")
	}
	payload, err := json.Marshal(persistedCacheVolumePayload{
		Key:       cache.Key,
		Namespace: cache.Namespace,
		Sharing:   cache.Sharing,
		Owner:     cache.Owner,
	})
	if err != nil {
		return nil, fmt.Errorf("marshal persisted cache volume payload: %w", err)
	}
	return payload, nil
}

func (*CacheVolume) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *call.ID, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedCacheVolumePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted cache volume payload: %w", err)
	}

	cache := NewCache(
		persisted.Key,
		persisted.Namespace,
		dagql.Optional[DirectoryID]{},
		persisted.Sharing,
		persisted.Owner,
	)
	if resultID != 0 {
		links, err := loadPersistedSnapshotLinksByResultID(ctx, dag, resultID, "cache volume")
		if err != nil {
			return nil, err
		}
		for _, link := range links {
			if link.Role != "snapshot" {
				continue
			}
			snapshot, link, err := loadPersistedMutableSnapshotByResultID(ctx, dag, resultID, "cache volume", "snapshot")
			if err != nil {
				return nil, err
			}
			selector := link.Slot
			if selector == "" {
				selector = "/"
			}
			cache.mu.Lock()
			cache.snapshot = snapshot
			cache.selector = selector
			cache.mu.Unlock()
			break
		}
	}
	return cache, nil
}

func (cache *CacheVolume) CacheUsageMayChange() bool {
	return true
}

func (cache *CacheVolume) invalidateSnapshotSize(ctx context.Context) error {
	snapshot := cache.getSnapshot()
	if snapshot == nil {
		return nil
	}
	return snapshot.InvalidateSize(ctx)
}

func (cache *CacheVolume) Sync(ctx context.Context) error {
	return cache.InitializeSnapshot(ctx)
}

func (cache *CacheVolume) PreparePersistedObject(ctx context.Context) error {
	if cache == nil {
		return nil
	}
	snapshot := cache.getSnapshot()
	if snapshot != nil {
		return snapshot.SetCachePolicyRetain()
	}
	return nil
}

func (cache *CacheVolume) InitializeSnapshot(ctx context.Context) error {
	if cache.getSnapshot() != nil {
		return nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return err
	}

	var source dagql.ObjectResult[*Directory]
	if cache.Source.Valid {
		source, err = cache.Source.Value.Load(ctx, srv)
		if err != nil {
			return fmt.Errorf("failed to load cache volume source: %w", err)
		}
	}

	if cache.Owner != "" {
		if source.Self() == nil {
			if err := srv.Select(ctx, srv.Root(), &source, dagql.Selector{Field: "directory"}); err != nil {
				return fmt.Errorf("failed to create scratch source directory for cache owner: %w", err)
			}
		}

		chowned := dagql.ObjectResult[*Directory]{}
		if err := srv.Select(ctx, source, &chowned, dagql.Selector{
			Field: "chown",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(".")},
				{Name: "owner", Value: dagql.String(cache.Owner)},
			},
		}); err != nil {
			return fmt.Errorf("failed to chown cache source directory: %w", err)
		}
		source = chowned
	}

	sourceSelector := "/"
	var sourceRef bkcache.ImmutableRef
	if source.Self() != nil {
		sourceSelector = source.Self().Dir
		if sourceSelector == "" {
			sourceSelector = "/"
		}
		sourceRef, err = source.Self().getSnapshot(ctx)
		if err != nil {
			return fmt.Errorf("failed to get cache source snapshot: %w", err)
		}
	}

	newRef, err := query.BuildkitCache().New(
		ctx,
		sourceRef,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeCacheMount),
		bkcache.WithDescription(fmt.Sprintf("cache volume %q", cache.Key)),
		bkcache.CachePolicyRetain,
	)
	if err != nil {
		return fmt.Errorf("failed to initialize cache volume snapshot: %w", err)
	}

	cache.mu.Lock()
	defer cache.mu.Unlock()
	cache.snapshot = newRef
	if sourceSelector == "" {
		sourceSelector = "/"
	}
	cache.selector = sourceSelector
	return nil
}

// Sum returns a checksum of the cache tokens suitable for use as a cache key.
func (cache *CacheVolume) Sum() string {
	hash := sha256.New()
	for _, tok := range []string{
		cache.Key,
		cache.Namespace,
		string(cache.Sharing),
		cache.Owner,
	} {
		_, _ = hash.Write([]byte(tok + "\x00"))
	}
	if cache.Source.Valid {
		_, _ = hash.Write([]byte(fmt.Sprintf("source:%s\x00", cache.Source.Value.ID().Digest())))
	}

	return base64.StdEncoding.EncodeToString(hash.Sum(nil))
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
