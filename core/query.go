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
	"github.com/dagger/dagger/dagql/idproto"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"
)

// Query forms the root of the DAG and houses all necessary state and
// dependencies for evaluating queries.
type Query struct {
	Buildkit *buildkit.Client

	// The current pipeline.
	Pipeline pipeline.Path

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

	// The metadata of client calls.
	// For the special case of the main client caller, the key is just empty string.
	// This is never explicitly deleted from; instead it will just be garbage collected
	// when this server for the session shuts down
	clientCallContext map[digest.Digest]*ClientCallContext
	clientCallMu      *sync.RWMutex

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	endpoints  map[string]http.Handler
	endpointMu *sync.RWMutex
}

func NewRoot() *Query {
	return &Query{
		clientCallContext: map[digest.Digest]*ClientCallContext{},
		clientCallMu:      &sync.RWMutex{},
		endpoints:         map[string]http.Handler{},
		endpointMu:        &sync.RWMutex{},
	}
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
	q.endpointMu.Lock()
	defer q.endpointMu.Unlock()
	q.endpoints[path] = handler
	return nil
}

func (q *Query) MuxEndpoints(mux *http.ServeMux) {
	q.endpointMu.RLock()
	defer q.endpointMu.RUnlock()
	for path, handler := range q.endpoints {
		mux.Handle(path, handler)
	}
}

type ClientCallContext struct {
	// the DAG of modules being served to this client
	Deps *ModDeps

	// If the client is itself from a function call in a user module, these are set with the
	// metadata of that ongoing function call
	ModID  *idproto.ID
	FnCall *FunctionCall
}

func (q *Query) ClientCallContext(clientDigest digest.Digest) (*ClientCallContext, bool) {
	q.clientCallMu.RLock()
	defer q.clientCallMu.RUnlock()
	ctx, ok := q.clientCallContext[clientDigest]
	return ctx, ok
}

func (q *Query) InstallDefaultClientContext(deps *ModDeps) {
	q.clientCallMu.Lock()
	defer q.clientCallMu.Unlock()

	q.DefaultDeps = deps

	q.clientCallContext[""] = &ClientCallContext{
		Deps: deps,
	}
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

	q.clientCallMu.Lock()
	defer q.clientCallMu.Unlock()
	callCtx, ok := q.clientCallContext[""]
	if !ok {
		return fmt.Errorf("client call not found")
	}
	callCtx.Deps = callCtx.Deps.Append(mod)
	return nil
}

func (q *Query) RegisterFunctionCall(dgst digest.Digest, deps *ModDeps, modID *idproto.ID, call *FunctionCall) error {
	if dgst == "" {
		return fmt.Errorf("cannot register function call with empty digest")
	}

	q.clientCallMu.Lock()
	defer q.clientCallMu.Unlock()
	_, ok := q.clientCallContext[dgst]
	if ok {
		return nil
	}
	q.clientCallContext[dgst] = &ClientCallContext{
		Deps:   deps,
		ModID:  modID,
		FnCall: call,
	}
	return nil
}

func (q *Query) CurrentModule(ctx context.Context) (dagql.ID[*Module], error) {
	var id dagql.ID[*Module]
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return id, err
	}
	if clientMetadata.ModuleCallerDigest == "" {
		return id, fmt.Errorf("no current module for main client caller")
	}

	q.clientCallMu.RLock()
	defer q.clientCallMu.RUnlock()
	callCtx, ok := q.clientCallContext[clientMetadata.ModuleCallerDigest]
	if !ok {
		return id, fmt.Errorf("client call %s not found", clientMetadata.ModuleCallerDigest)
	}
	return dagql.NewID[*Module](callCtx.ModID), nil
}

func (q *Query) CurrentFunctionCall(ctx context.Context) (*FunctionCall, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ModuleCallerDigest == "" {
		return nil, fmt.Errorf("no current function call for main client caller")
	}

	q.clientCallMu.RLock()
	defer q.clientCallMu.RUnlock()
	callCtx, ok := q.clientCallContext[clientMetadata.ModuleCallerDigest]
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
	callCtx, ok := q.clientCallContext[clientMetadata.ModuleCallerDigest]
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

func (q *Query) NewSecret(name string) *Secret {
	return &Secret{
		Query: q,
		Name:  name,
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

func (q *Query) IDDeps(ctx context.Context, baseDeps *ModDeps, id *idproto.ID) (*ModDeps, error) {
	deps := baseDeps
	for _, modID := range id.Modules() {
		bootstrap, err := q.DefaultDeps.Schema(ctx)
		if err != nil {
			return nil, fmt.Errorf("bootstrap schema: %w", err)
		}
		mod, err := dagql.NewID[*Module](modID).Load(ctx, bootstrap)
		if err != nil {
			return nil, fmt.Errorf("load source mod: %w", err)
		}
		deps = deps.Append(mod.Self)
	}
	return deps, nil
}
