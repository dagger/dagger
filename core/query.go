package core

import (
	"context"
	"fmt"
	"net/http"

	"github.com/containerd/containerd/content"
	bkcache "github.com/dagger/dagger/internal/buildkit/cache"
	bkclient "github.com/dagger/dagger/internal/buildkit/client"
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
	"github.com/dagger/dagger/engine/clientdb"
	"github.com/dagger/dagger/engine/filesync"
	"github.com/dagger/dagger/engine/server/resource"
)

// Query forms the root of the DAG and houses all necessary state and
// dependencies for evaluating queries.
type Query struct {
	Server
}

var ErrNoCurrentModule = fmt.Errorf("no current module")

// APIs from the server+session+client that are needed by core APIs
type Server interface {
	// Stitch in the given module to the list being served to the current client
	ServeModule(ctx context.Context, mod *Module, includeDependencies bool) error

	// If the current client is coming from a function, return the module that function is from
	CurrentModule(context.Context) (*Module, error)

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

	// Prune the local cache of releaseable entries. If useDefaultPolicy is true, use the engine-wide default pruning policy,
	// otherwise prune the whole cache of any releasable entries.
	PruneEngineLocalCacheEntries(context.Context, bool) (*EngineCacheEntrySet, error)

	// The default local cache policy to use for automatic local cache GC.
	EngineLocalCachePolicy() *bkclient.PruneInfo

	// Gets the buildkit cache manager
	BuildkitCache() bkcache.Manager

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
	ClientTelemetry(ctc context.Context, sessID, clientID string) (*clientdb.Queries, func() error, error)
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
	return &q
}

func (q *Query) WithPipeline(name, desc string) *Query {
	return q.Clone()
}

func (q *Query) NewHost() *Host {
	return &Host{}
}

func (q *Query) NewModule() *Module {
	return &Module{}
}

// IDDeps loads the module dependencies of a given ID.
//
// The returned ModDeps extends the inner DefaultDeps with all modules found in
// the ID, loaded by using the DefaultDeps schema.
func (q *Query) IDDeps(ctx context.Context, id *call.ID) (*ModDeps, error) {
	defaultDeps, err := q.DefaultDeps(ctx)
	if err != nil {
		return nil, fmt.Errorf("default deps: %w", err)
	}

	bootstrap, err := defaultDeps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap schema: %w", err)
	}
	deps := defaultDeps
	for _, modID := range id.Modules() {
		mod, err := dagql.NewID[*Module](modID.ID()).Load(ctx, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("load source mod: %w", err)
		}
		deps = deps.Append(mod.Self())
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
