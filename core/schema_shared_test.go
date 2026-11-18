package core

import (
	"context"
	"encoding/json"
	"fmt"
	goruntime "runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/engineutil"
)

// sharedTestCoreMod stands in for the core module: it forks a prepared base,
// and is identified by content, as CoreMod is by its schema base and view.
type sharedTestCoreMod struct {
	Mod
	base *dagql.Server
}

func (*sharedTestCoreMod) Name() string              { return ModuleName }
func (*sharedTestCoreMod) View() (call.View, bool)   { return "", false }
func (*sharedTestCoreMod) SchemaASTCacheKey() string { return "shared-test-core" }
func (m *sharedTestCoreMod) Same(other Mod) (bool, error) {
	_, ok := other.(*sharedTestCoreMod)
	return ok, nil
}
func (*sharedTestCoreMod) Install(context.Context, *dagql.Server, ...InstallOpts) error {
	panic("the core module is forked, not installed")
}
func (m *sharedTestCoreMod) ForkSchema(ctx context.Context, root *Query, view call.View) (*dagql.Server, error) {
	srv, err := m.base.Fork(ctx, root)
	if err == nil {
		srv.View = view
	}
	return srv, err
}

// clientStateRuntime runs a module whose constructor keeps its greeting
// argument as the object's message, and whose check function returns that
// message and its label. It records the query root each client's calls see.
type clientStateRuntime struct {
	*moduleOwnershipRuntime

	mu    sync.Mutex
	roots map[string]map[*Query]struct{}
}

func (r *clientStateRuntime) AsRuntime() (Runtime, bool)             { return r, true }
func (r *clientStateRuntime) CloneForModuleSource(*ModuleSource) SDK { return r }
func (r *clientStateRuntime) Runtime(context.Context, *SchemaBuilder, dagql.ObjectResult[*ModuleSource]) (ModuleRuntime, error) {
	return r, nil
}

func (r *clientStateRuntime) Call(ctx context.Context, _ *engineutil.ExecutionMetadata, fnCall *FunctionCall, _ dagql.ObjectResult[*Module]) error {
	md, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return err
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return err
	}
	r.mu.Lock()
	if r.roots[md.ClientID] == nil {
		r.roots[md.ClientID] = map[*Query]struct{}{}
	}
	r.roots[md.ClientID][query] = struct{}{}
	r.mu.Unlock()

	arg := func(name string) string {
		for _, in := range fnCall.InputArgs {
			if in.Name == name {
				var s string
				if err := json.Unmarshal(in.Value, &s); err == nil {
					return s
				}
			}
		}
		return ""
	}
	var out any
	switch fnCall.Name {
	case "":
		out = map[string]string{"message": arg("greeting")}
	case "check":
		var parent struct{ Message string }
		if err := json.Unmarshal(fnCall.Parent, &parent); err != nil {
			return err
		}
		out = parent.Message + ":" + arg("label")
	default:
		return fmt.Errorf("unexpected function %q", fnCall.Name)
	}
	payload, err := json.Marshal(out)
	if err != nil {
		return err
	}
	return fnCall.ReturnValue(ctx, JSON(payload))
}

// perClientDepsServer serves each client the core module on the client's
// own root, as the engine's DefaultDeps does, and gives each client a memo
// of its own, as the engine's SchemaBuilderMemo does.
type perClientDepsServer struct {
	*moduleObjectTestServer
	core Mod

	mu    sync.Mutex
	memos map[*Query]*SchemaBuilderMemo
}

func (s *perClientDepsServer) DefaultDeps(ctx context.Context) (*SchemaBuilder, error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	return NewSchemaBuilder(q, []Mod{s.core}), nil
}

func (s *perClientDepsServer) SchemaBuilderMemo(ctx context.Context) (*SchemaBuilderMemo, error) {
	q, err := CurrentQuery(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	memo, ok := s.memos[q]
	if !ok {
		memo = NewSchemaBuilderMemo()
		s.memos[q] = memo
	}
	return memo, nil
}

// Artifact discovery, evaluation and checks select through a standalone
// server for the module (dagqlServerForModule). The clients of a session
// share one for a module, unless its .env defaults make its schema the
// building client's.
func TestModuleServerSharedAcrossClients(t *testing.T) {
	f := newModuleOwnershipTest(t)
	inner, ok := f.root.Server.(*moduleObjectTestServer)
	require.True(t, ok)
	inner.session = NewSchemaBuilderMemo()
	facade := &perClientDepsServer{
		moduleObjectTestServer: inner,
		core:                   &sharedTestCoreMod{base: f.dag},
		memos:                  map[*Query]*SchemaBuilderMemo{},
	}
	setupCtx := f.session("module-server-session")
	mod := f.module(setupCtx, "example", moduleOwnershipModuleOpts{})
	dotenv := f.module(setupCtx, "dotenv", moduleOwnershipModuleOpts{})
	dotenv.Self().Source.Value.Self().UserDefaults = NewEnvFile(true).WithVariable("UNUSED", "1")

	const clients = 8
	load := func(mod dagql.ObjectResult[*Module]) (map[*dagql.Server]struct{}, int64) {
		t.Helper()
		before := SchemaBuilds().ModuleServers
		servers := map[*dagql.Server]struct{}{}
		var mem goruntime.MemStats
		var allocated uint64
		for i := range clients {
			id := fmt.Sprintf("%s-client-%d", mod.Self().Name(), i)
			root := NewRoot(facade)
			ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: id, SessionID: "module-server-session"})
			ctx = ContextWithQuery(dagql.ContextWithCache(ctx, f.cache), root)
			var srv *dagql.Server
			for range 2 {
				var err error
				goruntime.ReadMemStats(&mem)
				allocBefore := mem.TotalAlloc
				srv, err = dagqlServerForModule(ctx, mod)
				goruntime.ReadMemStats(&mem)
				allocated += mem.TotalAlloc - allocBefore
				require.NoError(t, err)
				servers[srv] = struct{}{}
			}
			if _, shared := servers[srv]; shared && len(servers) == 1 && i > 0 {
				require.NotSame(t, root, srv.Root().Unwrap(), "a shared server is on no client's root")
			}
			// The server serves the module to this client.
			var out dagql.String
			require.NoError(t, srv.Select(ctx, srv.Root(), &out,
				dagql.Selector{Field: mod.Self().Name()},
				dagql.Selector{Field: "check", Args: []dagql.NamedInput{{Name: "label", Value: dagql.NewString(id)}}},
			))
			require.Equal(t, id, out.String())
		}
		built := SchemaBuilds().ModuleServers - before
		t.Logf("%-8s %d clients, 2 loads each -> %d module servers built, %.1f KiB allocated per load",
			mod.Self().Name(), clients, built, float64(allocated)/float64(2*clients)/(1<<10))
		return servers, built
	}

	servers, built := load(mod)
	require.Len(t, servers, 1, "the session's clients share the module's server")
	require.Equal(t, int64(1), built)

	servers, built = load(dotenv)
	require.Len(t, servers, clients, "a module with .env defaults keeps a server per client")
	require.Equal(t, int64(clients), built)
}

// The session shares one server for a module set among its clients, built on
// a root of its own. Each client still sees its own state through it: its
// own query root in the calls it makes, and its own constructor arguments.
// A module with .env user defaults, which installing expands against the
// building client's host, keeps a server per client.
func TestSharedSchemaKeepsClientState(t *testing.T) {
	f := newModuleOwnershipTest(t)
	facade, ok := f.root.Server.(*moduleObjectTestServer)
	require.True(t, ok)
	facade.session = NewSchemaBuilderMemo()
	runtime := &clientStateRuntime{moduleOwnershipRuntime: f.runtime, roots: map[string]map[*Query]struct{}{}}

	setupCtx := f.session("shared-schema-session")
	mod := f.module(setupCtx, "example", moduleOwnershipModuleOpts{explicitConstructor: true})
	mod.Self().Source.Value.Self().SDKImpl = runtime
	stringType := newTypeDefDetachedResult(t, f.dag, "shared-greeting-string", &TypeDef{Kind: TypeDefKindString})
	ctor := mod.Self().ObjectDefs[0].Self().AsObject.Value.Self().Constructor.Value.Self()
	ctor.Args = append(ctor.Args, newTypeDefDetachedResult(t, f.dag, "shared-greeting",
		NewFunctionArg("greeting", stringType, "", nil, "", "", nil, nil)))
	// A Query field that reads client state the way core's resolvers do:
	// the client's own from its root in the context, and nothing from the
	// receiver, which on a shared server is the session's root.
	type probed struct{ self, current *Query }
	var probesMu sync.Mutex
	var probes []probed
	dagql.Fields[*Query]{
		dagql.Func("clientProbe", func(ctx context.Context, self *Query, _ struct{}) (dagql.Boolean, error) {
			current, err := CurrentQuery(ctx)
			if err != nil {
				return false, err
			}
			probesMu.Lock()
			probes = append(probes, probed{self: self, current: current})
			probesMu.Unlock()
			return dagql.Boolean(!current.hostHistories().empty()), nil
		}).DoNotCache("reads the calling client's state"),
	}.Install(f.dag)
	coreMod := &sharedTestCoreMod{base: f.dag}

	type client struct {
		ctx  context.Context
		root *Query
	}
	newClient := func(id string) client {
		root := NewRoot(facade)
		ctx := engine.ContextWithClientMetadata(t.Context(), &engine.ClientMetadata{ClientID: id, SessionID: "shared-schema-session"})
		return client{ctx: ContextWithQuery(dagql.ContextWithCache(ctx, f.cache), root), root: root}
	}
	a, b := newClient("client-a"), newClient("client-b")
	// What each client serves: its runtime's builder, on its own root, with
	// the module as its main module.
	serve := func(c client, mod dagql.ObjectResult[*Module]) *dagql.Server {
		t.Helper()
		served := NewSchemaBuilder(c.root, []Mod{coreMod}).With(NewUserMod(mod), InstallOpts{Main: true}).ClientOwned()
		srv, err := served.Schema(c.ctx)
		require.NoError(t, err)
		return srv
	}

	srvA, srvB := serve(a, mod), serve(b, mod)
	require.Same(t, srvA, srvB, "clients serving the same module set share its server")
	shared := srvA.Root().Unwrap()
	require.NotSame(t, a.root, shared, "the shared server is not bound to the client that built it")
	require.NotSame(t, b.root, shared)
	_, ok = srvA.Root().ObjectType().FieldSpec("with", "")
	require.True(t, ok, "the main module's constructor arguments are set through `with`")

	check := func(c client, srv *dagql.Server, greeting, label string) string {
		t.Helper()
		var out dagql.String
		require.NoError(t, srv.Select(c.ctx, srv.Root(), &out,
			dagql.Selector{Field: "with", Args: []dagql.NamedInput{{Name: "greeting", Value: dagql.NewString(greeting)}}},
			dagql.Selector{Field: "check", Args: []dagql.NamedInput{{Name: "label", Value: dagql.NewString(label)}}},
		))
		return out.String()
	}
	require.Equal(t, "from a:x", check(a, srvA, "from a", "x"))
	require.Equal(t, "from b:x", check(b, srvB, "from b", "x"),
		"each client's constructor arguments reach its own calls")
	require.Equal(t, "from a:y", check(a, srvA, "from a", "y"))

	// Client a approves a host history donor and opens a cache volume; b
	// does neither. Through the one server, each sees only its own.
	a.root.hostHistories().register(hostHistoryKey{sha: "a"}, hostHistoryDonor{owner: "client-a", path: "/a"})
	a.root.cacheVolumes()
	probe := func(c client) bool {
		t.Helper()
		var out dagql.Boolean
		require.NoError(t, srvA.Select(c.ctx, srvA.Root(), &out, dagql.Selector{Field: "clientProbe"}))
		return bool(out)
	}
	require.True(t, probe(a), "a sees its own approved host history")
	require.False(t, probe(b), "b does not see a's")
	sharedRoot := shared.(*Query)
	probesMu.Lock()
	require.Len(t, probes, 2)
	for i, want := range []*Query{a.root, b.root} {
		require.Same(t, sharedRoot, probes[i].self, "the field's receiver is the shared root")
		require.Same(t, want, probes[i].current, "the context holds the calling client's root")
	}
	probesMu.Unlock()
	require.True(t, sharedRoot.hostHistories().empty(), "the shared root holds no client's host histories")
	require.Nil(t, sharedRoot.cacheVolumeStore, "nor cache volumes")
	require.Nil(t, sharedRoot.ConstructorArgs, "nor constructor arguments")

	runtime.mu.Lock()
	require.Equal(t, map[*Query]struct{}{a.root: {}}, runtime.roots["client-a"],
		"client a's calls see its own query root, whose state is its own")
	require.Equal(t, map[*Query]struct{}{b.root: {}}, runtime.roots["client-b"],
		"client b's calls see its own query root, whose state is its own")
	runtime.mu.Unlock()

	// .env defaults are expanded against the building client's host when
	// installing, so a module with any keeps a server per client.
	dotenv := f.module(setupCtx, "dotenv", moduleOwnershipModuleOpts{})
	dotenv.Self().Source.Value.Self().UserDefaults = NewEnvFile(true).WithVariable("UNUSED", "1")
	envA, envB := serve(a, dotenv), serve(b, dotenv)
	require.NotSame(t, envA, envB, "a module with .env defaults is not shared")
	require.Same(t, a.root, envA.Root().Unwrap(), "it is built on its client's root")
	require.Same(t, b.root, envB.Root().Unwrap())
}
