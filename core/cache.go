package core

import (
	"context"
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
	Source    dagql.Nullable[dagql.ObjectResult[*Directory]]
	Sharing   CacheSharingMode
	Owner     string

	mu              sync.Mutex
	snapshot        bkcache.MutableRef
	snapshotID      string
	selector        string
	releaseSnapshot func(context.Context) error
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
	source dagql.Nullable[dagql.ObjectResult[*Directory]],
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
var _ dagql.HasDependencyResults = (*CacheVolume)(nil)

func (cache *CacheVolume) AttachDependencyResults(
	ctx context.Context,
	self dagql.AnyResult,
	attach func(dagql.AnyResult) (dagql.AnyResult, error),
) ([]dagql.AnyResult, error) {
	_ = ctx
	_ = self
	if cache == nil || !cache.Source.Valid || cache.Source.Value.Self() == nil {
		return nil, nil
	}
	attached, err := attach(cache.Source.Value)
	if err != nil {
		return nil, fmt.Errorf("attach cache volume source: %w", err)
	}
	typed, ok := attached.(dagql.ObjectResult[*Directory])
	if !ok {
		return nil, fmt.Errorf("attach cache volume source: unexpected result %T", attached)
	}
	cache.Source = dagql.NonNull(typed)
	return []dagql.AnyResult{typed}, nil
}

func (cache *CacheVolume) OnRelease(ctx context.Context) error {
	cache.mu.Lock()
	snapshot := cache.snapshot
	releaseSnapshot := cache.releaseSnapshot
	cache.snapshot = nil
	cache.releaseSnapshot = nil
	cache.mu.Unlock()

	if releaseSnapshot != nil {
		return releaseSnapshot(ctx)
	}
	if snapshot != nil {
		return snapshot.Release(ctx)
	}
	return nil
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

func (cache *CacheVolume) CacheUsageSize(ctx context.Context, sizeProvider dagql.CacheUsageSizeProvider, identity string) (int64, bool, error) {
	cache.mu.Lock()
	snapshot := cache.snapshot
	snapshotID := cache.snapshotID
	cache.mu.Unlock()
	if snapshot != nil {
		if snapshot.SnapshotID() != identity {
			return 0, false, nil
		}
		size, err := snapshot.Size(ctx)
		if err != nil {
			return 0, false, err
		}
		return size, true, nil
	}
	if snapshotID == "" || snapshotID != identity {
		return 0, false, nil
	}
	if sizeProvider == nil {
		return 0, false, nil
	}
	size, err := sizeProvider.SnapshotSize(ctx, snapshotID)
	if err != nil {
		return 0, false, err
	}
	return size, true, nil
}

func (cache *CacheVolume) CacheUsageIdentities() []string {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if cache.snapshot != nil {
		return []string{cache.snapshot.SnapshotID()}
	}
	if cache.snapshotID == "" {
		return nil
	}
	return []string{cache.snapshotID}
}

func (cache *CacheVolume) PersistedSnapshotRefLinks() []dagql.PersistedSnapshotRefLink {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	snapshotID := cache.snapshotID
	if cache.snapshot != nil {
		snapshotID = cache.snapshot.SnapshotID()
	}
	if snapshotID == "" {
		return nil
	}
	return []dagql.PersistedSnapshotRefLink{
		{
			RefKey: snapshotID,
			Role:   "snapshot",
		},
	}
}

type cacheVolumeStore struct {
	mu      sync.Mutex
	entries map[string]*cacheVolumeStoreEntry
}

type cacheVolumeStoreEntry struct {
	ref  bkcache.MutableRef
	refs int
}

type cacheVolumeStoreReleaser struct {
	store      *cacheVolumeStore
	snapshotID string
	once       sync.Once
	err        error
}

func (q *Query) cacheVolumes() *cacheVolumeStore {
	q.cacheVolumeStoreMu.Lock()
	defer q.cacheVolumeStoreMu.Unlock()

	if q.cacheVolumeStore == nil {
		q.cacheVolumeStore = &cacheVolumeStore{
			entries: make(map[string]*cacheVolumeStoreEntry),
		}
	}
	return q.cacheVolumeStore
}

func (s *cacheVolumeStore) acquire(
	ctx context.Context,
	snapshotID string,
	open func(context.Context) (bkcache.MutableRef, error),
) (bkcache.MutableRef, func(context.Context) error, error) {
	if snapshotID == "" {
		return nil, nil, fmt.Errorf("acquire cache volume snapshot: empty snapshot ID")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.entries == nil {
		s.entries = make(map[string]*cacheVolumeStoreEntry)
	}
	if entry, ok := s.entries[snapshotID]; ok {
		entry.refs++
		return entry.ref, (&cacheVolumeStoreReleaser{store: s, snapshotID: snapshotID}).release, nil
	}

	ref, err := open(ctx)
	if err != nil {
		return nil, nil, err
	}
	if ref == nil {
		return nil, nil, fmt.Errorf("acquire cache volume snapshot %q: nil mutable ref", snapshotID)
	}
	if ref.SnapshotID() != snapshotID {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, nil, fmt.Errorf("acquire cache volume snapshot %q: opened snapshot %q", snapshotID, ref.SnapshotID())
	}

	s.entries[snapshotID] = &cacheVolumeStoreEntry{
		ref:  ref,
		refs: 1,
	}
	return ref, (&cacheVolumeStoreReleaser{store: s, snapshotID: snapshotID}).release, nil
}

func (s *cacheVolumeStore) register(ctx context.Context, ref bkcache.MutableRef) (func(context.Context) error, error) {
	if ref == nil {
		return nil, fmt.Errorf("register cache volume snapshot: nil mutable ref")
	}
	snapshotID := ref.SnapshotID()
	if snapshotID == "" {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("register cache volume snapshot: empty snapshot ID")
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.entries == nil {
		s.entries = make(map[string]*cacheVolumeStoreEntry)
	}
	if _, ok := s.entries[snapshotID]; ok {
		_ = ref.Release(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("register cache volume snapshot %q: already registered", snapshotID)
	}

	s.entries[snapshotID] = &cacheVolumeStoreEntry{
		ref:  ref,
		refs: 1,
	}
	return (&cacheVolumeStoreReleaser{store: s, snapshotID: snapshotID}).release, nil
}

func (r *cacheVolumeStoreReleaser) release(ctx context.Context) error {
	r.once.Do(func() {
		r.err = r.store.release(ctx, r.snapshotID)
	})
	return r.err
}

func (s *cacheVolumeStore) release(ctx context.Context, snapshotID string) error {
	s.mu.Lock()
	entry, ok := s.entries[snapshotID]
	if !ok {
		s.mu.Unlock()
		return nil
	}
	entry.refs--
	if entry.refs > 0 {
		s.mu.Unlock()
		return nil
	}
	delete(s.entries, snapshotID)
	ref := entry.ref
	s.mu.Unlock()

	return ref.Release(ctx)
}

type persistedCacheVolumePayload struct {
	Key            string           `json:"key"`
	Namespace      string           `json:"namespace,omitempty"`
	SourceResultID uint64           `json:"sourceResultID,omitempty"`
	Sharing        CacheSharingMode `json:"sharing,omitempty"`
	Owner          string           `json:"owner,omitempty"`
	Selector       string           `json:"selector,omitempty"`
}

func (cache *CacheVolume) EncodePersistedObject(ctx context.Context, persistedCache dagql.PersistedObjectCache) (dagql.PersistedObjectEncoding, error) {
	_ = ctx
	if cache == nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("encode persisted cache volume: nil cache volume")
	}
	var sourceResultID uint64
	if cache.Source.Valid {
		encoded, err := encodePersistedObjectRef(persistedCache, cache.Source.Value, "cache volume source")
		if err != nil {
			return dagql.PersistedObjectEncoding{}, err
		}
		sourceResultID = encoded
	}
	cache.mu.Lock()
	selector := cache.selector
	if selector == "" {
		selector = "/"
	}
	snapshotID := cache.snapshotID
	var snapshotLinks []dagql.PersistedSnapshotRefLink
	if cache.snapshot != nil {
		snapshotID = cache.snapshot.SnapshotID()
	}
	if snapshotID != "" {
		snapshotLinks = []dagql.PersistedSnapshotRefLink{{
			RefKey: snapshotID,
			Role:   "snapshot",
		}}
	}
	cache.mu.Unlock()
	payload, err := json.Marshal(persistedCacheVolumePayload{
		Key:            cache.Key,
		Namespace:      cache.Namespace,
		SourceResultID: sourceResultID,
		Sharing:        cache.Sharing,
		Owner:          cache.Owner,
		Selector:       selector,
	})
	if err != nil {
		return dagql.PersistedObjectEncoding{}, fmt.Errorf("marshal persisted cache volume payload: %w", err)
	}
	return dagql.PersistedObjectEncoding{
		JSON:          payload,
		SnapshotLinks: snapshotLinks,
	}, nil
}

func (cache *CacheVolume) lockKey() (string, error) {
	if cache == nil {
		return "", fmt.Errorf("cache volume lock key: nil cache volume")
	}
	sourceID := ""
	if cache.Source.Valid {
		id, err := cache.Source.Value.ID()
		if err != nil {
			return "", fmt.Errorf("cache volume lock key source ID: %w", err)
		}
		if id != nil {
			sourceID, err = id.Encode()
			if err != nil {
				return "", fmt.Errorf("cache volume lock key source ID: %w", err)
			}
		}
	}
	cache.mu.Lock()
	selector := cache.selector
	cache.mu.Unlock()
	if selector == "" {
		selector = "/"
	}
	payload, err := json.Marshal(struct {
		Key       string           `json:"key"`
		Namespace string           `json:"namespace,omitempty"`
		SourceID  string           `json:"sourceID,omitempty"`
		Sharing   CacheSharingMode `json:"sharing,omitempty"`
		Owner     string           `json:"owner,omitempty"`
		Selector  string           `json:"selector,omitempty"`
	}{
		Key:       cache.Key,
		Namespace: cache.Namespace,
		SourceID:  sourceID,
		Sharing:   cache.Sharing,
		Owner:     cache.Owner,
		Selector:  selector,
	})
	if err != nil {
		return "", fmt.Errorf("marshal cache volume lock key: %w", err)
	}
	return "cache-volume:" + string(payload), nil
}

func (*CacheVolume) DecodePersistedObject(ctx context.Context, dag *dagql.Server, resultID uint64, _ *dagql.ResultCall, payload json.RawMessage) (dagql.Typed, error) {
	var persisted persistedCacheVolumePayload
	if err := json.Unmarshal(payload, &persisted); err != nil {
		return nil, fmt.Errorf("decode persisted cache volume payload: %w", err)
	}

	source := dagql.Nullable[dagql.ObjectResult[*Directory]]{}
	if persisted.SourceResultID != 0 {
		sourceRes, err := loadPersistedObjectResultByResultID[*Directory](ctx, dag, persisted.SourceResultID, "cache volume source")
		if err != nil {
			return nil, err
		}
		if sourceRes.Self() != nil {
			source = dagql.NonNull(sourceRes)
		}
	}

	cache := NewCache(
		persisted.Key,
		persisted.Namespace,
		source,
		persisted.Sharing,
		persisted.Owner,
	)
	cache.selector = persisted.Selector
	if cache.selector == "" {
		cache.selector = "/"
	}
	if resultID == 0 {
		return cache, nil
	}
	links, err := loadPersistedSnapshotLinksByResultID(ctx, dag, resultID, "cache volume")
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.Role != "snapshot" {
			continue
		}
		cache.snapshotID = link.RefKey
		break
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

func (cache *CacheVolume) InitializeSnapshot(ctx context.Context) error {
	cache.mu.Lock()
	defer cache.mu.Unlock()

	if cache.snapshot != nil {
		return nil
	}

	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	if cache.snapshotID != "" {
		ref, releaseSnapshot, err := query.cacheVolumes().acquire(ctx, cache.snapshotID, func(ctx context.Context) (bkcache.MutableRef, error) {
			ref, err := query.SnapshotManager().GetMutableBySnapshotID(
				ctx,
				cache.snapshotID,
				bkcache.NoUpdateLastUsed,
				bkcache.WithRecordType(bkclient.UsageRecordTypeCacheMount),
				bkcache.WithDescription(fmt.Sprintf("cache volume %q", cache.Key)),
			)
			if err != nil {
				return nil, fmt.Errorf("reopen persisted cache volume snapshot %q: %w", cache.snapshotID, err)
			}
			return ref, nil
		})
		if err != nil {
			return err
		}
		cache.snapshot = ref
		cache.releaseSnapshot = releaseSnapshot
		if cache.selector == "" {
			cache.selector = "/"
		}
		return nil
	}

	var source dagql.ObjectResult[*Directory]
	if cache.Source.Valid {
		source = cache.Source.Value
	}

	if cache.Owner != "" {
		if source.Self() == nil {
			srv, err := CurrentDagqlServer(ctx)
			if err != nil {
				return err
			}
			if err := srv.Select(ctx, srv.Root(), &source, dagql.Selector{Field: "directory"}); err != nil {
				return fmt.Errorf("failed to create scratch source directory for cache owner: %w", err)
			}
		}

		srv, err := CurrentDagqlServer(ctx)
		if err != nil {
			return err
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
		dagCache, err := dagql.EngineCache(ctx)
		if err != nil {
			return err
		}
		if err := dagCache.Evaluate(ctx, source); err != nil {
			return fmt.Errorf("evaluate cache source directory: %w", err)
		}
		sourceSelector, err = source.Self().Dir.GetOrEval(ctx, source.Result)
		if err != nil {
			return fmt.Errorf("failed to get cache source selector: %w", err)
		}
		if sourceSelector == "" {
			sourceSelector = "/"
		}
		sourceRef, err = source.Self().Snapshot.GetOrEval(ctx, source.Result)
		if err != nil {
			return fmt.Errorf("failed to get cache source snapshot: %w", err)
		}
	}

	newRef, err := query.SnapshotManager().New(
		ctx,
		sourceRef,
		nil,
		bkcache.WithRecordType(bkclient.UsageRecordTypeCacheMount),
		bkcache.WithDescription(fmt.Sprintf("cache volume %q", cache.Key)),
	)
	if err != nil {
		return fmt.Errorf("failed to initialize cache volume snapshot: %w", err)
	}
	releaseSnapshot, err := query.cacheVolumes().register(ctx, newRef)
	if err != nil {
		return err
	}

	cache.snapshot = newRef
	cache.snapshotID = newRef.SnapshotID()
	cache.releaseSnapshot = releaseSnapshot
	if sourceSelector == "" {
		sourceSelector = "/"
	}
	cache.selector = sourceSelector
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
