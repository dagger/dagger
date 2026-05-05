package core

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/containerd/containerd/v2/core/content"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/moby/locker"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/auth"
	workspacepkg "github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/engineutil"
	serverresolver "github.com/dagger/dagger/engine/server/resolver"
	"google.golang.org/grpc"
)

// Query forms the root of the DAG and houses all necessary state and
// dependencies for evaluating queries.
type Query struct {
	Server

	// ConstructorArgs stores arguments to pass to the entrypoint module's
	// constructor. Set by the `with` field on Query so that entrypoint
	// proxy resolvers can forward them to the constructor.
	ConstructorArgs map[string]dagql.Input
}

var (
	ErrNoCurrentModule    = fmt.Errorf("no current module")
	ErrNoCurrentWorkspace = fmt.Errorf("no current workspace")
)

// APIs from the server+session+client that are needed by core APIs
type Server interface {
	// Handle an HTTP request from a nested Dagger client.
	ServeHTTPToNestedClient(http.ResponseWriter, *http.Request, *engine.ClientMetadata, string, dagql.AnyObjectResult, dagql.Typed, dagql.AnyObjectResult)

	// Stitch in the given module to the list being served to the current client
	ServeModule(ctx context.Context, mod dagql.ObjectResult[*Module], includeDependencies bool, entrypoint bool) error

	// If the current client is coming from a function, return the module that function is from
	CurrentModule(context.Context) (dagql.ObjectResult[*Module], error)

	// If the current client is a module client or a client created by a module function, returns that module.
	ModuleParent(context.Context) (dagql.ObjectResult[*Module], error)

	// If the current client is coming from a function, return the function call metadata
	CurrentFunctionCall(context.Context) (*FunctionCall, error)

	// If the current client is bound to an environment, return that environment.
	CurrentEnv(context.Context) (dagql.ObjectResult[*Env], error)

	// Return the modules being served to the current client
	CurrentServedDeps(context.Context) (*SchemaBuilder, error)

	// The Client metadata of the main client caller (i.e. the one who created
	// the session, typically the CLI invoked by the user)
	MainClientCallerMetadata(context.Context) (*engine.ClientMetadata, error)

	// Metadata about the main client, aka "non-module parent client", aka "NMPC".
	//
	// The NMPC is the nearest ancestor client that is not a module.
	// It is either a caller from the host like the CLI or a nested exec.
	// Useful for figuring out where local sources should be resolved from through
	// chains of dependency modules.
	NonModuleParentClientMetadata(context.Context) (*engine.ClientMetadata, error)

	// The cached workspace result from ensureWorkspaceLoaded.
	CurrentWorkspace(context.Context) (*Workspace, error)

	// A snapshot of the current workspace lockfile for ambient live locking.
	// Returns ok=false when lock-backed workspace access is unavailable.
	CurrentWorkspaceLock(context.Context) (*workspacepkg.Lock, bool, error)

	// Stage a lockfile lookup result for the current workspace's live lock state.
	SetCurrentWorkspaceLookup(context.Context, string, string, []any, workspacepkg.LookupResult) error

	// The Client metadata of a specific client ID within the same session as the
	// current client.
	SpecificClientMetadata(context.Context, string) (*engine.ClientMetadata, error)

	// The default deps of every user module (currently just core)
	DefaultDeps(context.Context) (*SchemaBuilder, error)

	// The telemetry seen-key store for the current client's session.
	TelemetrySeenKeyStore(context.Context) (dagql.TelemetrySeenKeyStore, error)

	// The DagQL server for the current client's session
	Server(context.Context) (*dagql.Server, error)

	// Mix in this http endpoint+handler to the current client's session
	MuxEndpoint(context.Context, string, http.Handler) error

	// The session attachables connection for a specific client ID within the
	// same session as the current client.
	SpecificClientAttachableConn(context.Context, string) (*grpc.ClientConn, error)

	// The auth provider for the current client
	Auth(context.Context) (*auth.RegistryAuthProvider, error)

	// The engine utility client for the current client
	Engine(context.Context) (*engineutil.Client, error)

	// The session-owned registry resolver for the current client.
	RegistryResolver(context.Context) (*serverresolver.Resolver, error)

	// The services for the current client's session
	Services(context.Context) (*Services, error)

	// The default platform for the engine as a whole
	Platform() Platform

	// The content store for the engine as a whole
	OCIStore() content.Store

	// The builtin engine OCI source store.
	BuiltinOCIStore() content.Store

	// The dns configuration for the engine as a whole
	DNS() *oci.DNSConfig

	// The lease manager for the engine as a whole
	LeaseManager() *leaseutil.Manager

	// Return all the cache entries in the local cache. No support for filtering yet.
	EngineLocalCacheEntries(context.Context) (*EngineCacheEntrySet, error)

	// Prune the local cache of releaseable entries. If UseDefaultPolicy is true,
	// use the engine-wide default pruning policy, otherwise prune the whole cache
	// of any releasable entries.
	PruneEngineLocalCacheEntries(context.Context, EngineCachePruneOptions) (*EngineCacheEntrySet, error)

	// The default local cache policy to use for automatic local cache GC.
	EngineLocalCachePolicy() *dagql.CachePrunePolicy

	// Gets the engine snapshot manager.
	SnapshotManager() bkcache.SnapshotManager

	// A global lock for the engine, can be used to synchronize access to
	// shared resources between multiple potentially concurrent calls.
	Locker() *locker.Locker

	// A shared engine-wide salt used when creating cache keys for secrets based on their plaintext
	SecretSalt() []byte

	// Flush telemetry for all clients in the current session.
	FlushSessionTelemetry(ctx context.Context) error

	// Open a client's telemetry database.
	ClientTelemetry(ctc context.Context, sessID, clientID string) (*clientdb.DB, error)

	// The name of the engine
	EngineName() string

	// The list of connected client IDs
	Clients() []string

	// Return a client connected to a cloud engine. If bool return is false, the local engine should be used. Session attachables for the returned client will be proxied back to the calling client.
	CloudEngineClient(
		ctx context.Context,
		module string,
		function string,
		execCmd []string,
	) (
		cloudClient *engineclient.Client,
		useCloudClient bool,
		err error,
	)

	// A mount namespace guaranteed to not have any mounts created by engine operations.
	// Should be used when creating goroutines/processes that unshare a mount namespace,
	// otherwise those unshared mnt namespaces may inherit mounts from engine operations
	// and leak them.
	CleanMountNS() *os.File
}

type queryKey struct{}

func ContextWithQuery(ctx context.Context, q *Query) context.Context {
	return context.WithValue(ctx, queryKey{}, q)
}

func CurrentQuery(ctx context.Context) (*Query, error) {
	q, ok := ctx.Value(queryKey{}).(*Query)
	if !ok {
		return nil, fmt.Errorf("no query in context")
	}
	return q, nil
}

func CurrentDagqlServer(ctx context.Context) (*dagql.Server, error) {
	// Prefer the dagql server explicitly attached to this resolver context.
	// This is required for dynamic schemas (e.g. SDKs implemented as modules)
	// that run selections against a server different from the session's default.
	if srv := dagql.CurrentDagqlServer(ctx); srv != nil {
		return srv, nil
	}

	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("current query: %w", err)
	}
	srv, err := q.Server.Server(ctx)
	if err != nil {
		return nil, fmt.Errorf("query server: %w", err)
	}
	return srv, nil
}

func NewRoot(srv Server) *Query {
	return &Query{Server: srv}
}

func (*Query) Type() *ast.Type {
	return &ast.Type{
		NamedType: "Query",
		NonNull:   true,
	}
}

func (*Query) TypeDescription() string {
	return "The root of the DAG."
}

func (q Query) Clone() *Query {
	cp := q
	if q.ConstructorArgs != nil {
		cp.ConstructorArgs = make(map[string]dagql.Input, len(q.ConstructorArgs))
		for k, v := range q.ConstructorArgs {
			cp.ConstructorArgs[k] = v
		}
	}
	return &cp
}

func (q *Query) WithPipeline(name, desc string) *Query {
	return q.Clone()
}

func (q *Query) NewHost() *Host {
	return &Host{}
}

func (q *Query) NewModule() *Module {
	return &Module{
		Deps: NewSchemaBuilder(q, nil),
	}
}

// ModDepsForCall loads the module dependencies referenced by the given result call.
func (q *Query) ModDepsForCall(ctx context.Context, rootCall *dagql.ResultCall) (*SchemaBuilder, error) {
	defaultDeps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("default deps: %w", err)
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap schema: %w", err)
	}

	deps := defaultDeps
	seenModuleResultIDs := map[uint64]struct{}{}

	appendModule := func(inst dagql.ObjectResult[*Module]) error {
		if inst.Self() == nil {
			return nil
		}
		if !inst.Self().Source.Valid {
			// Bare dag.module() builder shells are intermediate construction results,
			// not source-backed dependency modules to install into a schema.
			return nil
		}
		instID, err := inst.ID()
		if err != nil {
			return fmt.Errorf("module %q handle ID: %w", inst.Self().Name(), err)
		}
		if instID == nil || instID.EngineResultID() == 0 {
			return fmt.Errorf("module %q is not attached", inst.Self().Name())
		}
		if _, seen := seenModuleResultIDs[instID.EngineResultID()]; seen {
			return nil
		}
		seenModuleResultIDs[instID.EngineResultID()] = struct{}{}
		deps = deps.Append(NewUserMod(inst))
		return nil
	}

	cache, err := dagql.EngineCache(ctx)
	if err != nil {
		return nil, fmt.Errorf("engine cache: %w", err)
	}
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("current client metadata: %w", err)
	}
	if clientMetadata.SessionID == "" {
		return nil, fmt.Errorf("empty session ID")
	}
	if err := cache.WalkResultCall(rootCall, func(ref *dagql.ResultCallRef, frame *dagql.ResultCall) error {
		if ref == nil || ref.ResultID == 0 || frame == nil || frame.Type == nil || frame.Type.NamedType != "Module" {
			return nil
		}
		res, err := cache.LoadResultByResultID(ctx, clientMetadata.SessionID, dag, ref.ResultID)
		if err != nil {
			return fmt.Errorf("load module result %d: %w", ref.ResultID, err)
		}
		modInst, ok := res.(dagql.ObjectResult[*Module])
		if !ok {
			return fmt.Errorf("result %d is %T, not module result", ref.ResultID, res)
		}
		return appendModule(modInst)
	}); err != nil {
		return nil, err
	}
	return deps, nil
}

func (q *Query) RequireMainClient(ctx context.Context) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return fmt.Errorf("failed to get client metadata: %w", err)
	}
	mainClientCallerMetadata, err := q.MainClientCallerMetadata(ctx)
	if err != nil {
		return fmt.Errorf("failed to get main client caller ID: %w", err)
	}
	if clientMetadata.ClientID != mainClientCallerMetadata.ClientID {
		return fmt.Errorf("only the main client can call this function")
	}
	return nil
}
