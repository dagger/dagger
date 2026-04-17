package core

import (
	"context"

	"github.com/vektah/gqlparser/v2/ast"
)

type Engine struct {
	Name string `field:"true" doc:"The name of the engine instance."`
}

func (*Engine) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Engine",
		NonNull:   true,
	}
}

func (*Engine) TypeDescription() string {
	return "The Dagger engine configuration and state"
}

type EngineCache struct {
	MaxUsedSpace  int `field:"true" doc:"The maximum bytes to keep in the cache without pruning."`
	TargetSpace   int `field:"true" doc:"The target number of bytes to keep when pruning."`
	ReservedSpace int `field:"true" doc:"The minimum amount of disk space this policy is guaranteed to retain."`
	MinFreeSpace  int `field:"true" doc:"The target amount of free disk space the garbage collector will attempt to leave."`
}

type EngineCachePruneOptions struct {
	UseDefaultPolicy bool
	MaxUsedSpace     string
	ReservedSpace    string
	MinFreeSpace     string
	TargetSpace      string
}

func (*EngineCache) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EngineCache",
		NonNull:   true,
	}
}

func (*EngineCache) TypeDescription() string {
	return "A cache storage for the Dagger engine"
}

type EngineCacheEntrySet struct {
	EntryCount     int `field:"true" doc:"The number of cache entries in this set."`
	DiskSpaceBytes int `field:"true" doc:"The total disk space used by the cache entries in this set."`

	EntriesList []*EngineCacheEntry
}

func (*EngineCacheEntrySet) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EngineCacheEntrySet",
		NonNull:   true,
	}
}

func (*EngineCacheEntrySet) TypeDescription() string {
	return "A set of cache entries returned by a query to a cache"
}

func (*EngineCacheEntrySet) Evaluate(context.Context) error {
	return nil
}

func (entrySet *EngineCacheEntrySet) Sync(ctx context.Context) error {
	return entrySet.Evaluate(ctx)
}

type EngineCacheEntry struct {
	Description               string `field:"true" doc:"The description of the cache entry."`
	DiskSpaceBytes            int    `field:"true" doc:"The disk space used by the cache entry."`
	CreatedTimeUnixNano       int    `field:"true" doc:"The time the cache entry was created, in Unix nanoseconds."`
	MostRecentUseTimeUnixNano int    `field:"true" doc:"The most recent time the cache entry was used, in Unix nanoseconds."`
	ActivelyUsed              bool   `field:"true" doc:"Whether the cache entry is actively being used."`
	RecordType                string `field:"true" doc:"The type of the cache record (e.g. regular, internal, frontend, source.local, source.git.checkout, exec.cachemount)."`
}

func (*EngineCacheEntry) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EngineCacheEntry",
		NonNull:   true,
	}
}

func (*EngineCacheEntry) TypeDescription() string {
	return "An individual cache entry in a cache entry set"
}
