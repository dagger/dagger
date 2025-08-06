package core

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"runtime"
	"slices"
	"strings"

	bkcache "github.com/moby/buildkit/cache"
	bkcontainer "github.com/moby/buildkit/frontend/gateway/container"
	bkmounts "github.com/moby/buildkit/solver/llbsolver/mounts"
	"github.com/moby/buildkit/solver/pb"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
)

// CacheVolume is a persistent volume with a globally scoped identifier.
type CacheVolume struct {
	Keys []string
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

func (cache *CacheVolume) Clone() *CacheVolume {
	cp := *cache
	cp.Keys = slices.Clone(cp.Keys)
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

func (cache *CacheVolume) Restore(ctx context.Context, source dagql.ObjectResult[*Directory]) error {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return fmt.Errorf("get current query: %w", err)
	}

	dir := source.Self()

	// Create a temporary container that mounts both the cache volume and source directory
	// Then use rsync or cp to atomically sync the contents
	tempContainer, err := NewContainer(query.Platform())
	if err != nil {
		return fmt.Errorf("failed to create temp container: %w", err)
	}

	// Use a minimal base image for the operation
	tempContainer, err = tempContainer.FromRefString(ctx, "alpine:latest")
	if err != nil {
		return fmt.Errorf("failed to create temp container: %w", err)
	}

	// Mount the cache volume at /cache
	tempContainer, err = tempContainer.WithMountedCache(ctx, "/cache", cache, nil, CacheSharingModeShared, "")
	if err != nil {
		return fmt.Errorf("failed to mount cache volume: %w", err)
	}

	// Mount the source directory at /source
	tempContainer, err = tempContainer.WithMountedDirectory(ctx, "/source", dir, "", false)
	if err != nil {
		return fmt.Errorf("failed to mount source directory: %w", err)
	}

	// Clear the cache directory and copy source contents atomically
	tempContainer, err = tempContainer.WithExec(ctx, ContainerExecOpts{
		Args: []string{"sh", "-c", "rm -rf /cache/* /cache/.[^.]* && cp -rp /source/. /cache/"},
	}, nil)
	if err != nil {
		return fmt.Errorf("failed to restore cache contents: %w", err)
	}

	return nil
}

func (cache *CacheVolume) Snapshot(ctx context.Context) (*Directory, error) {
	query, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("get current query: %w", err)
	}

	bkCache := query.BuildkitCache()
	session := query.BuildkitSession()
	mm := bkmounts.NewMountManager(fmt.Sprintf("cache %s", cache.Sum()), bkCache, session)

	bkSessionGroup, ok := buildkit.CurrentBuildkitSessionGroup(ctx)
	if !ok {
		return nil, fmt.Errorf("no buildkit session group in context")
	}

	p, err := bkcontainer.PrepareMounts(ctx, mm, bkCache, bkSessionGroup, "/", []*pb.Mount{
		{
			Input:     pb.Empty,
			Output:    pb.SkipOutput,
			MountType: pb.MountType_CACHE,
			CacheOpt: &pb.CacheOpt{
				ID:      cache.Sum(),
				Sharing: pb.CacheSharingOpt_SHARED,
			},
		},
	}, nil, func(m *pb.Mount, ref bkcache.ImmutableRef) (bkcache.MutableRef, error) {
		desc := fmt.Sprintf("mount %s from cache %s", m.Dest, cache.Sum())
		return bkCache.New(ctx, ref, bkSessionGroup, bkcache.WithDescription(desc))
	}, runtime.GOOS)
	if err != nil {
		return nil, fmt.Errorf("prepare mounts: %w", err)
	}

	if len(p.Actives) == 0 {
		return nil, fmt.Errorf("no active mounts found for cache %s", cache.Sum())
	}

	immutableRef, err := p.Actives[0].Ref.Commit(ctx)
	if err != nil {
		return nil, fmt.Errorf("commit cache mount: %w", err)
	}

	dir, err := NewScratchDirectory(ctx, query.Platform())
	if err != nil {
		return nil, fmt.Errorf("create scratch directory: %w", err)
	}
	dir.Result = immutableRef
	return dir, nil
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
