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
	QueryOpts

	// The current pipeline.
	Pipeline pipeline.Path
}

var ErrNoCurrentModule = fmt.Errorf("no current module")

// Settings for Query that are shared across all instances for a given DaggerServer
type QueryOpts struct {
	Server

	Services *Services

	Secrets *SecretStore

	Auth *auth.RegistryAuthProvider

	OCIStore     content.Store
	LeaseManager *leaseutil.Manager

	// The default platform.
	Platform Platform

	// The default deps of every user module (currently just core)
	DefaultDeps *ModDeps

	// The DagQL query cache.
	Cache dagql.Cache

	Buildkit *buildkit.Client

	MainClientCallerID string
}

type Server interface {
	ServeModule(context.Context, *Module) error
	CurrentModule(context.Context) (*Module, error)
	CurrentFunctionCall(context.Context) (*FunctionCall, error)
	CurrentServedDeps(context.Context) (*ModDeps, error)
	MuxEndpoint(context.Context, string, http.Handler) error
}

func NewRoot(opts QueryOpts) *Query {
	return &Query{QueryOpts: opts}
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
	bootstrap, err := q.DefaultDeps.Schema(ctx)
	if err != nil {
		return nil, fmt.Errorf("bootstrap schema: %w", err)
	}
	deps := q.DefaultDeps
	for _, modID := range id.Modules() {
		mod, err := dagql.NewID[*Module](modID.ID()).Load(ctx, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("load source mod: %w", err)
		}
		deps = deps.Append(mod.Self)
	}
	return deps, nil
}
