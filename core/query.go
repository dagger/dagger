package core

import (
	"context"
	"fmt"
	"net/http"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
	"github.com/vito/progrock"
)

// Query forms the root of the DAG and houses all necessary state and
// dependencies for evaluating queries.
type Query struct {
	QueryOpts
	Buildkit *buildkit.Client

	// The current pipeline.
	Pipeline pipeline.Path
}

var ErrNoCurrentModule = fmt.Errorf("no current module")

// Settings for Query that are shared across all instances for a given DaggerServer
type QueryOpts struct {
	ProgrockSocketPath string

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

	BuildkitOpts *buildkit.Opts
	Recorder     *progrock.Recorder

	// The metadata of client calls.
	// For the special case of the main client caller, the key is just empty string.
	// This is never explicitly deleted from; instead it will just be garbage collected
	// when this server for the session shuts down
	ClientCallContext map[digest.Digest]*ClientCallContext
	ClientCallMu      *sync.RWMutex

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	Endpoints  map[string]http.Handler
	EndpointMu *sync.RWMutex
}

func NewRoot(ctx context.Context, opts QueryOpts) (*Query, error) {
	bk, err := buildkit.NewClient(ctx, opts.BuildkitOpts)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}

	// NOTE: context.WithoutCancel is used because if the provided context is canceled, buildkit can
	// leave internal progress contexts open and leak goroutines.
	bk.WriteStatusesTo(context.WithoutCancel(ctx), opts.Recorder)

	return &Query{
		QueryOpts: opts,
		Buildkit:  bk,
	}, nil
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

func (q *Query) MuxEndpoint(ctx context.Context, path string, handler http.Handler) error {
	q.EndpointMu.Lock()
	defer q.EndpointMu.Unlock()
	q.Endpoints[path] = handler
	return nil
}

type ClientCallContext struct {
	Root *Query

	// the DAG of modules being served to this client
	Deps *ModDeps

	// If the client is itself from a function call in a user module, these are set with the
	// metadata of that ongoing function call
	Module *Module
	FnCall *FunctionCall

	ProgrockParent string
}

func (q *Query) ServeModuleToMainClient(ctx context.Context, modMeta dagql.Instance[*Module]) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	if clientMetadata.ModuleCallerDigest != "" {
		return fmt.Errorf("cannot serve module to client %s", clientMetadata.ClientID)
	}

	mod := modMeta.Self

	q.ClientCallMu.Lock()
	defer q.ClientCallMu.Unlock()
	callCtx, ok := q.ClientCallContext[""]
	if !ok {
		return fmt.Errorf("client call not found")
	}
	callCtx.Deps = callCtx.Deps.Append(mod)
	return nil
}

func (q *Query) RegisterFunctionCall(
	ctx context.Context,
	dgst digest.Digest,
	deps *ModDeps,
	mod *Module,
	call *FunctionCall,
	progrockParent string,
) error {
	if dgst == "" {
		return fmt.Errorf("cannot register function call with empty digest")
	}

	q.ClientCallMu.Lock()
	defer q.ClientCallMu.Unlock()
	_, ok := q.ClientCallContext[dgst]
	if ok {
		return nil
	}
	newRoot, err := NewRoot(ctx, q.QueryOpts)
	if err != nil {
		return err
	}
	q.ClientCallContext[dgst] = &ClientCallContext{
		Root:           newRoot,
		Deps:           deps,
		Module:         mod,
		FnCall:         call,
		ProgrockParent: progrockParent,
	}
	return nil
}

func (q *Query) CurrentModule(ctx context.Context) (*Module, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ModuleCallerDigest == "" {
		return nil, fmt.Errorf("%w: main client caller has no module", ErrNoCurrentModule)
	}

	q.ClientCallMu.RLock()
	defer q.ClientCallMu.RUnlock()
	callCtx, ok := q.ClientCallContext[clientMetadata.ModuleCallerDigest]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}
	return callCtx.Module, nil
}

func (q *Query) CurrentFunctionCall(ctx context.Context) (*FunctionCall, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ModuleCallerDigest == "" {
		return nil, fmt.Errorf("%w: main client caller has no function", ErrNoCurrentModule)
	}

	q.ClientCallMu.RLock()
	defer q.ClientCallMu.RUnlock()
	callCtx, ok := q.ClientCallContext[clientMetadata.ModuleCallerDigest]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}

	return callCtx.FnCall, nil
}

func (q *Query) CurrentServedDeps(ctx context.Context) (*ModDeps, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, ok := q.ClientCallContext[clientMetadata.ModuleCallerDigest]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}
	return callCtx.Deps, nil
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
