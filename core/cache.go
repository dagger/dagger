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
