package core

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync"

	"github.com/containerd/containerd/content"
	"github.com/moby/buildkit/util/leaseutil"
	"github.com/opencontainers/go-digest"
	"github.com/vektah/gqlparser/v2/ast"

	"github.com/dagger/dagger/auth"
	"github.com/dagger/dagger/core/pipeline"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"github.com/dagger/dagger/engine/slog"
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

	// The metadata of client calls.
	// For the special case of the main client caller, the key is just empty string.
	// This is never explicitly deleted from; instead it will just be garbage collected
	// when this server for the session shuts down
	ClientCallContext  map[string]*ClientCallContext
	ClientCallMu       *sync.RWMutex
	MainClientCallerID string

	// the http endpoints being served (as a map since APIs like shellEndpoint can add more)
	Endpoints  map[string]http.Handler
	EndpointMu *sync.RWMutex
}

func NewRoot(ctx context.Context, opts QueryOpts) (*Query, error) {
	bk, err := buildkit.NewClient(ctx, opts.BuildkitOpts)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}

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
	FnCall *FunctionCall
}

func (q *Query) ServeModule(ctx context.Context, mod *Module) error {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}

	q.ClientCallMu.Lock()
	defer q.ClientCallMu.Unlock()
	callCtx, ok := q.ClientCallContext[clientMetadata.ClientID]
	if !ok {
		return fmt.Errorf("client call not found")
	}
	callCtx.Deps = callCtx.Deps.Append(mod)
	return nil
}

func (q *Query) RegisterCaller(ctx context.Context, call *FunctionCall) (string, error) {
	if call == nil {
		call = &FunctionCall{}
	}
	callCtx := &ClientCallContext{
		FnCall: call,
	}

	currentID := dagql.CurrentID(ctx)
	clientIDInputs := []string{currentID.Digest().String()}
	if !call.Cache {
		// use the ServerID so that we bust cache once-per-session
		clientMetadata, err := engine.ClientMetadataFromContext(ctx)
		if err != nil {
			return "", err
		}
		clientIDInputs = append(clientIDInputs, clientMetadata.ServerID)
	}
	clientIDDigest := digest.FromString(strings.Join(clientIDInputs, " "))

	// only use encoded part of digest because this ID ends up becoming a buildkit Session ID
	// and buildkit has some ancient internal logic that splits on a colon to support some
	// dev mode logic: https://github.com/moby/buildkit/pull/290
	// also trim it to 25 chars as it ends up becoming part of service URLs
	clientID := clientIDDigest.Encoded()[:25]

	slog.ExtraDebug("registering nested caller",
		"client_id", clientID,
		"op", currentID.Display(),
	)

	if call.Module == nil {
		callCtx.Deps = q.DefaultDeps
	} else {
		callCtx.Deps = call.Module.Deps
		// By default, serve both deps and the module's own API to itself. But if SkipSelfSchema is set,
		// only serve the APIs of the deps of this module. This is currently only needed for the special
		// case of the function used to get the definition of the module itself (which can't obviously
		// be served the API its returning the definition of).
		if !call.SkipSelfSchema {
			callCtx.Deps = callCtx.Deps.Append(call.Module)
		}
	}

	q.ClientCallMu.Lock()
	defer q.ClientCallMu.Unlock()
	_, ok := q.ClientCallContext[clientID]
	if ok {
		return clientID, nil
	}

	var err error
	callCtx.Root, err = NewRoot(ctx, q.QueryOpts)
	if err != nil {
		return "", err
	}

	q.ClientCallContext[clientID] = callCtx
	return clientID, nil
}

func (q *Query) CurrentModule(ctx context.Context) (*Module, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ClientID == q.MainClientCallerID {
		return nil, fmt.Errorf("%w: main client caller has no current module", ErrNoCurrentModule)
	}

	q.ClientCallMu.RLock()
	defer q.ClientCallMu.RUnlock()
	callCtx, ok := q.ClientCallContext[clientMetadata.ClientID]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ClientID)
	}
	if callCtx.FnCall.Module == nil {
		return nil, ErrNoCurrentModule
	}
	return callCtx.FnCall.Module, nil
}

func (q *Query) CurrentFunctionCall(ctx context.Context) (*FunctionCall, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	if clientMetadata.ClientID == q.MainClientCallerID {
		return nil, fmt.Errorf("%w: main client caller has no function", ErrNoCurrentModule)
	}

	q.ClientCallMu.RLock()
	defer q.ClientCallMu.RUnlock()
	callCtx, ok := q.ClientCallContext[clientMetadata.ClientID]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ClientID)
	}

	return callCtx.FnCall, nil
}

func (q *Query) CurrentServedDeps(ctx context.Context) (*ModDeps, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return nil, err
	}
	callCtx, ok := q.ClientCallContext[clientMetadata.ClientID]
	if !ok {
		return nil, fmt.Errorf("client call %s not found", clientMetadata.ClientID)
	}
	return callCtx.Deps, nil
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

func (q *Query) NewHostService(upstream string, ports []PortForward, sessionID string) *Service {
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
