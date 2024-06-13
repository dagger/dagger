package core

import (
	"context"
	"fmt"
	"net/http"

	"github.com/containerd/containerd/content"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
)

// Query forms the root of the DAG and houses all necessary state and
// dependencies for evaluating queries.
type Query struct {
	Server

	// The current pipeline.
	Pipeline pipeline.Path
}

var ErrNoCurrentModule = fmt.Errorf("no current module")

// APIs from the server+session+client that are needed by core APIs
type Server interface {
	// Stitch in the given module to the list being served to the current client
	ServeModule(context.Context, *Module) error

	// If the current client is coming from a function, return the module that function is from
	CurrentModule(context.Context) (*Module, error)

	// If the current client is coming from a function, return the function call metadata
	CurrentFunctionCall(context.Context) (*FunctionCall, error)

	// Return the list of deps being served to the current client
	CurrentServedDeps(context.Context) (*ModDeps, error)

	// The ClientID of the main client caller (i.e. the one who created the session, typically the CLI
	// invoked by the user)
	MainClientCallerID(context.Context) (string, error)

	// The default deps of every user module (currently just core)
	DefaultDeps(context.Context) (*ModDeps, error)

	// The DagQL query cache for the current client's session
	Cache(context.Context) (dagql.Cache, error)

	// Mix in this http endpoint+handler to the current client's session
	MuxEndpoint(context.Context, string, http.Handler) error

	// The secret store for the current client
	Secrets(context.Context) (*SecretStore, error)

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

	// The lease manager for the engine as a whole
	LeaseManager() *leaseutil.Manager
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
	q = q.Clone()
	q.Pipeline = q.Pipeline.Add(pipeline.Pipeline{
		Name:        name,
		Description: desc,
	})
	return q
}

func (q *Query) NewContainer(platform Platform) *Container {
	return &Container{
		Query:    q,
		Platform: platform,
	}
}

func (q *Query) NewSecret(name string, accessor string) *Secret {
	return &Secret{
		Query:    q,
		Name:     name,
		Accessor: accessor,
	}
}

func (q *Query) NewHost() *Host {
	return &Host{
		Query: q,
	}
}

func (q *Query) NewModule() *Module {
	return &Module{
		Query: q,
	}
}

func (q *Query) NewContainerService(ctx context.Context, ctr *Container) *Service {
	connectServiceEffect(ctx)
	return &Service{
		Query:     q,
		Container: ctr,
	}
}

func (q *Query) NewTunnelService(ctx context.Context, upstream dagql.Instance[*Service], ports []PortForward) *Service {
	connectServiceEffect(ctx)
	return &Service{
		Query:          q,
		TunnelUpstream: &upstream,
		TunnelPorts:    ports,
	}
}

func (q *Query) NewHostService(ctx context.Context, upstream string, ports []PortForward, sessionID string) *Service {
	connectServiceEffect(ctx)
	return &Service{
		Query:         q,
		HostUpstream:  upstream,
		HostPorts:     ports,
		HostSessionID: sessionID,
	}
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
		deps = deps.Append(mod.Self)
	}
	return deps, nil
}
