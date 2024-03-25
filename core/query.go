package core

import (
	"context"
	"fmt"
	"net/http"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/vektah/gqlparser/v2/ast"
)

// Query forms the root of the DAG and houses all necessary state and
// dependencies for evaluating queries.
type Query struct {
	QueryOpts

	Dag *dagql.Server

	Buildkit *buildkit.Client

	ClientID string

	SecretToken string

	Deps *ModDeps

	FnCall *FunctionCall

	ProgrockParent string

	// The current pipeline.
	Pipeline pipeline.Path
}

var ErrNoCurrentModule = fmt.Errorf("no current module")

// Settings for Query that are shared across all instances for a given DaggerServer
type QueryOpts struct {
	DaggerServer

	ProgrockSocketPath string

	Services *Services

	Secrets *SecretStore

	Auth *auth.RegistryAuthProvider

	OCIStore     content.Store
	LeaseManager *leaseutil.Manager

	// The default platform.
	Platform Platform

	MainClientCallerID string
}

// Methods that Query needs from the over-arching DaggerServer which involve mutating and/or creating
// Query instances
type DaggerServer interface {
	MuxEndpoint(ctx context.Context, path string, handler http.Handler) error
	ServeModule(ctx context.Context, mod *Module) error
	NewRootForCurrentCall(ctx context.Context, call *FunctionCall) (*Query, error)
	NewRootForDependencies(ctx context.Context, deps *ModDeps) (*Query, error)
	NewRootForDynamicID(ctx context.Context, id *call.ID) (*Query, error)
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

func (q *Query) CurrentModule(ctx context.Context) (*Module, error) {
	mod := q.FnCall.Module
	if mod == nil {
		return nil, ErrNoCurrentModule
	}
	return mod, nil
}

func (q *Query) CurrentFunctionCall(ctx context.Context) (*FunctionCall, error) {
	fnCall := q.FnCall
	if fnCall == nil {
		return nil, ErrNoCurrentModule
	}
	return fnCall, nil
}

func (q *Query) WithPipeline(name, desc string, labels []pipeline.Label) *Query {
	q = q.Clone()
	q.Pipeline = q.Pipeline.Add(pipeline.Pipeline{
		Name:        name,
		Description: desc,
		Labels:      labels,
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

func (q *Query) NewContainerService(ctr *Container) *Service {
	return &Service{
		Query:     q,
		Container: ctr,
	}
}

func (q *Query) NewTunnelService(upstream dagql.Instance[*Service], ports []PortForward) *Service {
	return &Service{
		Query:          q,
		TunnelUpstream: &upstream,
		TunnelPorts:    ports,
	}
}

func (q *Query) NewHostService(upstream string, ports []PortForward) *Service {
	return &Service{
		Query:        q,
		HostUpstream: upstream,
		HostPorts:    ports,
	}
}
