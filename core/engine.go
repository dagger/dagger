package core

import (
	"context"

	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/dagql"
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

// EngineSession describes one session on the engine.
type EngineSession struct {
	SessionID string `field:"true" name:"sessionID" doc:"The session's ID." doNotCache:"live session state"`
	Detached  bool   `field:"true" doc:"Whether the session outlives the client that created it." doNotCache:"live session state"`
	CreatedAt string `field:"true" doc:"When the session was created, in RFC 3339 format." doNotCache:"live session state"`

	ClientList []*EngineSessionClient
}

func (*EngineSession) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EngineSession",
		NonNull:   true,
	}
}

func (*EngineSession) TypeDescription() string {
	return "A session on the Dagger engine (experimental)"
}

// EngineSessionClient describes one client that connected directly to a
// session.
type EngineSessionClient struct {
	ClientID   string                       `field:"true" name:"clientID" doc:"The client's ID." doNotCache:"live session state"`
	Hostname   string                       `field:"true" doc:"The hostname of the machine the client runs on." doNotCache:"live session state"`
	PID        dagql.Nullable[dagql.Int]    `field:"true" name:"pid" doc:"The client's process ID." doNotCache:"live session state"`
	Command    dagql.Nullable[dagql.String] `field:"true" doc:"A short form of the client's command line." doNotCache:"live session state"`
	Background bool                         `field:"true" doc:"Whether the client runs in the background, without a terminal." doNotCache:"live session state"`
	Connected  bool                         `field:"true" doc:"Whether the client's attachables connection is open." doNotCache:"live session state"`
	Provides   []string                     `field:"true" doc:"Attachable kinds this client serves, for example files, secrets, terminal." doNotCache:"live session state"`
	Workspace  dagql.Nullable[dagql.String] `field:"true" doc:"Address of the workspace this client is bound to, if any." doNotCache:"live session state"`

	SessionID   string
	ForwardList []Port
}

func (*EngineSessionClient) Type() *ast.Type {
	return &ast.Type{
		NamedType: "EngineSessionClient",
		NonNull:   true,
	}
}

func (*EngineSessionClient) TypeDescription() string {
	return "A client of a session on the Dagger engine (experimental)"
}

type EngineCache struct {
	MaxUsedSpace  int `field:"true" doc:"The maximum bytes to keep in the cache without pruning."`
	TargetSpace   int `field:"true" doc:"The target number of bytes to keep when pruning."`
	ReservedSpace int `field:"true" doc:"The minimum amount of disk space this policy is guaranteed to retain."`
	MinFreeSpace  int `field:"true" doc:"The target amount of free disk space the garbage collector will attempt to leave."`
}

type EngineCachePruneOptions struct {
	UseDefaultPolicy     bool
	MaxUsedSpace         string
	ReservedSpace        string
	MinFreeSpace         string
	TargetSpace          string
	MaxEstimatedBytes    *int64
	TargetEstimatedBytes *int64
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
	Description               string   `field:"true" doc:"The description of the cache entry."`
	DiskSpaceBytes            int      `field:"true" doc:"The disk space used by the cache entry."`
	CreatedTimeUnixNano       int      `field:"true" doc:"The time the cache entry was created, in Unix nanoseconds."`
	MostRecentUseTimeUnixNano int      `field:"true" doc:"The most recent time the cache entry was used, in Unix nanoseconds."`
	ActivelyUsed              bool     `field:"true" doc:"Whether the cache entry is actively being used."`
	RecordType                string   `field:"true" doc:"The type of the cache record (e.g. regular, internal, frontend, source.local, source.git.checkout, exec.cachemount)."`
	RecordTypes               []string `field:"true" doc:"The storage record types represented by this cache entry."`
	DagqlCall                 string   `field:"true" doc:"The DagQL call that produced this cache entry."`
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
