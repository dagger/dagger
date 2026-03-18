package core

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/containerd/containerd/v2/core/content"
	bkcache "github.com/dagger/dagger/engine/snapshots"
	"github.com/dagger/dagger/internal/buildkit/executor/oci"
	bksession "github.com/dagger/dagger/internal/buildkit/session"
	"github.com/dagger/dagger/internal/buildkit/util/leaseutil"
	"github.com/moby/locker"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	engineclient "github.com/dagger/dagger/engine/client"
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/filesync"
	"github.com/dagger/dagger/engine/server/resource"
)

// Query forms the root of the DAG and houses all necessary state and
// dependencies for evaluating queries.
type Query struct {
	Server

	// An Env value propagated to a module function call, i.e. from LLM.
	CurrentEnv *call.ID
}

var ErrNoCurrentModule = fmt.Errorf("no current module")

// APIs from the server+session+client that are needed by core APIs
type Server interface {
	// Stitch in the given module to the list being served to the current client
	ServeModule(ctx context.Context, mod dagql.ObjectResult[*Module], includeDependencies bool) error

	// If the current client is coming from a function, return the module that function is from
	CurrentModule(context.Context) (dagql.ObjectResult[*Module], error)

	// If the current client is a module client or a client created by a module function, returns that module.
	ModuleParent(context.Context) (dagql.ObjectResult[*Module], error)

	// If the current client is coming from a function, return the function call metadata
	CurrentFunctionCall(context.Context) (*FunctionCall, error)

	// Return the list of deps being served to the current client
	CurrentServedDeps(context.Context) (*ModDeps, error)

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

	// The Client metadata of a specific client ID within the same session as the
	// current client.
	SpecificClientMetadata(context.Context, string) (*engine.ClientMetadata, error)

	// The default deps of every user module (currently just core)
	DefaultDeps(context.Context) (*ModDeps, error)

	// The DagQL query cache for the current client's session
	Cache(context.Context) (*dagql.SessionCache, error)

	// The DagQL server for the current client's session
	Server(context.Context) (*dagql.Server, error)

	// Mix in this http endpoint+handler to the current client's session
	MuxEndpoint(context.Context, string, http.Handler) error

	// The secret store for the current client
	Secrets(context.Context) (*SecretStore, error)

	// The socket store for the current client
	Sockets(context.Context) (*SocketStore, error)

	// Add client-isolated resources like secrets, sockets, etc. to the current client's session based
	// on anything embedded in the given ID. skipTopLevel, if true, will result in the leaf selection
	// of the ID to be skipped when walking the ID to find these resources.
	AddClientResourcesFromID(ctx context.Context, id *resource.ID, sourceClientID string, skipTopLevel bool) error

	// The auth provider for the current client
	Auth(context.Context) (*auth.RegistryAuthProvider, error)

	// The buildkit APIs for the current client
	Buildkit(context.Context) (*buildkit.Client, error)

	// The services for the current client's session
	Services(context.Context) (*Services, error)

	// The default platform for the engine as a whole
	Platform() Platform

	// The content store for the engine as a whole
	OCIStore() content.Store

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

	// Gets the buildkit cache manager
	BuildkitCache() bkcache.SnapshotManager

	// Gets the buildkit session manager
	BuildkitSession() *bksession.Manager

	// Gets the local source
	FileSyncer() *filesync.FileSyncer

	// A global lock for the engine, can be used to synchronize access to
	// shared resources between multiple potentially concurrent calls.
	Locker() *locker.Locker

	// A shared engine-wide salt used when creating cache keys for secrets based on their plaintext
	SecretSalt() []byte

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

func CurrentDagqlCache(ctx context.Context) (*dagql.SessionCache, error) {
	if srv := dagql.CurrentDagqlServer(ctx); srv != nil && srv.Cache != nil {
		return srv.Cache, nil
	}

	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, fmt.Errorf("current query: %w", err)
	}
	cache, err := q.Cache(ctx)
	if err != nil {
		return nil, fmt.Errorf("query cache: %w", err)
	}
	return cache, nil
}

func NewRoot(srv Server, envID *call.ID) *Query {
	return &Query{Server: srv, CurrentEnv: envID}
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
	return &q
}

func (q *Query) WithPipeline(name, desc string) *Query {
	return q.Clone()
}

func (q *Query) NewHost() *Host {
	return &Host{}
}

func (q *Query) NewModule() *Module {
	return &Module{
		Deps: NewModDeps(q, nil),
	}
}

// ModDepsForCall loads the module dependencies referenced by the given result call.
func (q *Query) ModDepsForCall(ctx context.Context, rootCall *dagql.ResultCall) (*ModDeps, error) {
	defaultDeps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("default deps: %w", err)
	}
	dag, err := CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap schema: %w", err)
	}

	deps := defaultDeps
	seenCalls := map[*dagql.ResultCall]struct{}{}
	seenResultIDs := map[uint64]struct{}{}
	seenModuleResultIDs := map[uint64]struct{}{}

	var appendModule func(dagql.ObjectResult[*Module]) error
	appendModule = func(inst dagql.ObjectResult[*Module]) error {
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
		if inst.Self().Toolchains != nil {
			for _, entry := range inst.Self().Toolchains.Entries() {
				if err := appendModule(entry.Module); err != nil {
					return fmt.Errorf("toolchain module for %q: %w", inst.Self().Name(), err)
				}
			}
		}
		return nil
	}

	var walkLiteral func(*dagql.ResultCallLiteral) error
	var walkRef func(*dagql.ResultCallRef) error
	var walkCall func(*dagql.ResultCall) error

	walkLiteral = func(lit *dagql.ResultCallLiteral) error {
		if lit == nil {
			return nil
		}
		switch lit.Kind {
		case dagql.ResultCallLiteralKindResultRef:
			return walkRef(lit.ResultRef)
		case dagql.ResultCallLiteralKindList:
			for _, item := range lit.ListItems {
				if err := walkLiteral(item); err != nil {
					return err
				}
			}
		case dagql.ResultCallLiteralKindObject:
			for _, field := range lit.ObjectFields {
				if field == nil {
					continue
				}
				if err := walkLiteral(field.Value); err != nil {
					return fmt.Errorf("field %q: %w", field.Name, err)
				}
			}
		}
		return nil
	}

	walkRef = func(ref *dagql.ResultCallRef) error {
		if ref == nil {
			return nil
		}
		if ref.Call != nil {
			return walkCall(ref.Call)
		}
		if ref.ResultID == 0 {
			return nil
		}
		if _, seen := seenResultIDs[ref.ResultID]; seen {
			return nil
		}
		seenResultIDs[ref.ResultID] = struct{}{}
		res, err := dag.Cache.LoadResultByResultID(ctx, dag, ref.ResultID)
		if err != nil {
			return fmt.Errorf("load result %d: %w", ref.ResultID, err)
		}
		if modInst, ok := res.(dagql.ObjectResult[*Module]); ok {
			if err := appendModule(modInst); err != nil {
				return err
			}
		}
		call, err := res.ResultCall()
		if err != nil {
			return fmt.Errorf("result %d call: %w", ref.ResultID, err)
		}
		return walkCall(call)
	}

	walkCall = func(call *dagql.ResultCall) error {
		if call == nil {
			return nil
		}
		if _, seen := seenCalls[call]; seen {
			return nil
		}
		seenCalls[call] = struct{}{}

		if call.Module != nil {
			if err := walkRef(call.Module.ResultRef); err != nil {
				return fmt.Errorf("module %q: %w", call.Module.Name, err)
			}
		}
		if err := walkRef(call.Receiver); err != nil {
			return fmt.Errorf("receiver: %w", err)
		}
		for _, arg := range call.Args {
			if arg == nil {
				continue
			}
			if err := walkLiteral(arg.Value); err != nil {
				return fmt.Errorf("arg %q: %w", arg.Name, err)
			}
		}
		for _, input := range call.ImplicitInputs {
			if input == nil {
				continue
			}
			if err := walkLiteral(input.Value); err != nil {
				return fmt.Errorf("implicit input %q: %w", input.Name, err)
			}
		}
		return nil
	}

	if err := walkCall(rootCall); err != nil {
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
