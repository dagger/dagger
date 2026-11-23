package schema

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

// moduleIdentityTestSource reads a module source named name from an
// in-engine directory holding only its config, as a module's own files read
// from a workspace pinned to a commit are the same in every session.
func moduleIdentityTestSource(t *testing.T, e *transferTestEngine, ctx context.Context, name string) dagql.ObjectResult[*core.ModuleSource] {
	t.Helper()
	var src dagql.ObjectResult[*core.ModuleSource]
	require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &src,
		dagql.Selector{Field: "directory"},
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("dagger.json")},
			{Name: "contents", Value: dagql.NewString(`{"name":"` + name + `"}`)},
		}},
		dagql.Selector{Field: "asModuleSource"}))
	return src
}

func moduleIdentityTestLoad(t *testing.T, e *transferTestEngine, ctx context.Context, src dagql.ObjectResult[*core.ModuleSource], selectors ...dagql.Selector) dagql.ObjectResult[*core.Module] {
	t.Helper()
	var mod dagql.ObjectResult[*core.Module]
	require.NoError(t, e.dag.Select(ctx, src, &mod, append(selectors, dagql.Selector{Field: "asModule"})...))
	return mod
}

// Two sessions that read a module source from the same content load one
// module: the later session's load is the earlier's. Before, every client
// read its own source and loaded its own module, so each session loaded
// every module again, on a warm engine as on a fresh one.
func TestModuleLoadedFromEqualSourcesIsShared(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))

	first := e.session(t, "first")
	moduleIdentityTestLoad(t, e, first, moduleIdentityTestSource(t, e, first, "shared"))

	later := e.session(t, "later")
	mod := moduleIdentityTestLoad(t, e, later, moduleIdentityTestSource(t, e, later, "shared"))
	require.True(t, mod.HitCache(), "the later session's module is the earlier session's, not loaded again")
	require.Equal(t, "shared", mod.Self().Name())
}

// Sources that differ in what a module reads from them load different
// modules. withName changes only the module's name, which the
// implementation digest leaves out, so an identity built from it alone
// would hand the later session the earlier one's module.
func TestModuleLoadedFromSourcesWithDifferentNamesIsNotShared(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	named := func(name string) dagql.Selector {
		return dagql.Selector{Field: "withName", Args: []dagql.NamedInput{{Name: "name", Value: dagql.NewString(name)}}}
	}

	first := e.session(t, "first")
	require.Equal(t, "alpha", moduleIdentityTestLoad(t, e, first, moduleIdentityTestSource(t, e, first, "base"), named("alpha")).Self().Name())

	later := e.session(t, "later")
	mod := moduleIdentityTestLoad(t, e, later, moduleIdentityTestSource(t, e, later, "base"), named("beta"))
	require.False(t, mod.HitCache(), "a module named otherwise is loaded, not taken from the earlier session")
	require.Equal(t, "beta", mod.Self().Name())
}

// A load that makes a call keyed per call, session or client reads what
// another session may see otherwise, so its module stays in its session: a
// later session loading the same source loads it again. A load that reads
// nothing of the sort is shared. The module's SDK makes the read here, as
// loading resolves a default-path context named by ref from the caller.
func TestModuleLoadReadingPerCallStaysInItsSession(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, _ := prebuildTestEnv(t, "first")
	var probes atomic.Int32
	dagql.Fields[*core.Query]{
		dagql.Func("perCallProbe", func(context.Context, *core.Query, struct{}) (dagql.String, error) {
			probes.Add(1)
			return "read", nil
		}).WithInput(dagql.PerCallInput),
	}.Install(srv)
	readsPerCall := &prebuildTestSDK{runtime: func(ctx context.Context) error {
		var read dagql.String
		return srv.Select(ctx, srv.Root(), &read, dagql.Selector{Field: "perCallProbe"})
	}}

	later := withClient(ctx, "later", "later")
	for _, tc := range []struct {
		name   string
		sdk    *prebuildTestSDK
		shared bool
	}{
		{"reads-nothing", &prebuildTestSDK{}, true},
		{"reads-per-call", readsPerCall, false},
	} {
		_, err := selectAsModule(ctx, srv, prebuildTestSource(t, ctx, cache, srv, "first", tc.name, tc.sdk))
		require.NoError(t, err)
		mod, err := selectAsModule(later, srv, prebuildTestSource(t, later, cache, srv, "later", tc.name, tc.sdk))
		require.NoError(t, err)
		require.Equal(t, tc.shared, mod.HitCache(), "%s: the later session's module is the first's", tc.name)
	}
	require.EqualValues(t, 2, probes.Load(), "the module that read per call was loaded in each session")
}

// An eager client builds a module's runtime as the module loads, so a lazy
// client's load, which hasn't, isn't one it can take. Two lazy clients still
// share theirs.
func TestModuleLoadedLazilyIsNotAnEagerClients(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))

	first := e.session(t, "first")
	moduleIdentityTestLoad(t, e, first, moduleIdentityTestSource(t, e, first, "eager"))

	eager := e.session(t, "eager")
	md, err := engine.ClientMetadataFromContext(eager)
	require.NoError(t, err)
	eagerMD := *md
	eagerMD.EagerRuntime = true
	eager = engine.ContextWithClientMetadata(eager, &eagerMD)
	require.False(t, moduleIdentityTestLoad(t, e, eager, moduleIdentityTestSource(t, e, eager, "eager")).HitCache(),
		"the eager client loads the module itself")

	later := e.session(t, "later")
	require.True(t, moduleIdentityTestLoad(t, e, later, moduleIdentityTestSource(t, e, later, "eager")).HitCache(),
		"a later lazy client takes the first's")
}

// A call the module's runtime prebuild makes after the load has built the
// module, as the load stops the prebuild it no longer needs, still keeps
// the module to its session: the load reads what it made only once the
// prebuild has stopped.
func TestModuleLoadKeepsAPrebuildsLateReadInItsSession(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, _ := prebuildTestEnv(t, "first")
	var probes atomic.Int32
	dagql.Fields[*core.Query]{
		dagql.Func("perCallProbe", func(context.Context, *core.Query, struct{}) (dagql.String, error) {
			probes.Add(1)
			return "read", nil
		}).WithInput(dagql.PerCallInput),
	}.Install(srv)
	sdk := &prebuildTestSDK{prebuild: func(ctx context.Context) error {
		<-ctx.Done()
		var read dagql.String
		if err := srv.Select(context.WithoutCancel(ctx), srv.Root(), &read, dagql.Selector{Field: "perCallProbe"}); err != nil {
			return err
		}
		return ctx.Err()
	}}

	_, err := selectAsModule(ctx, srv, prebuildTestSource(t, ctx, cache, srv, "first", "late", sdk))
	require.NoError(t, err)
	require.EqualValues(t, 1, probes.Load(), "the prebuild read once the load stopped it")
	later := withClient(ctx, "later", "later")
	mod, err := selectAsModule(later, srv, prebuildTestSource(t, later, cache, srv, "later", "late", sdk))
	require.NoError(t, err)
	require.False(t, mod.HitCache(), "the later session loads the module itself")
}

// moduleIdentitySDK is a module SDK whose implementation is the module mod.
type moduleIdentitySDK struct {
	*prebuildTestSDK
	mod dagql.ObjectResult[*core.Module]
}

func (s *moduleIdentitySDK) AsModule() (dagql.ObjectResult[*core.Module], bool) {
	return s.mod, s.mod.Self() != nil
}

// A source's identity holds the SDK it resolved: a module SDK named by a
// branch resolves to another module once the branch moves, and a source
// loaded with it is another source. A source that holds a module SDK's
// config without its module, as one restored from a saved cache does, has
// no identity.
func TestModuleSourceIdentityHoldsItsResolvedSDK(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, _ := prebuildTestEnv(t, "sdk")
	source := func(sdkMod dagql.ObjectResult[*core.Module]) *core.ModuleSource {
		return &core.ModuleSource{
			Kind:               core.ModuleSourceKindDir,
			ModuleName:         "demo",
			ModuleOriginalName: "demo",
			SDK:                &core.SDKConfig{Source: "github.com/acme/sdk@main"},
			SDKImpl:            &moduleIdentitySDK{prebuildTestSDK: &prebuildTestSDK{}, mod: sdkMod},
		}
	}
	before := attachDefinitionTestResult(t, ctx, cache, srv, "sdk", "sdk-before-the-branch-moved", &core.Module{NameField: "sdk"})
	after := attachDefinitionTestResult(t, ctx, cache, srv, "sdk", "sdk-after-the-branch-moved", &core.Module{NameField: "sdk"})

	idBefore, ok, err := source(before).ContentIdentity(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	idAfter, ok, err := source(after).ContentIdentity(ctx)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEqual(t, idBefore, idAfter, "sources holding different SDK modules are different sources")

	_, ok, err = source(dagql.ObjectResult[*core.Module]{}).ContentIdentity(ctx)
	require.NoError(t, err)
	require.False(t, ok, "a source whose module SDK it doesn't hold has no identity")
}

// A source's identity holds its user defaults as a function sees them,
// expanded from the main client's environment: two clients whose .env reads
// the same, but whose environments differ, hold different defaults.
func TestModuleSourceIdentityHoldsItsExpandedUserDefaults(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	src := &core.ModuleSource{
		Kind:               core.ModuleSourceKindDir,
		ModuleName:         "demo",
		ModuleOriginalName: "demo",
		UserDefaults:       &core.EnvFile{Expand: true, Environ: []string{"greeting=${GREETING}"}},
	}
	identityFor := func(name, greeting string) string {
		t.Helper()
		md := &engine.ClientMetadata{ClientID: name + "-client", SessionID: name + "-session"}
		e.srv.attachables[md.ClientID] = newLLMAuthAttachable(t, &llmAuthSecrets{vars: map[string]string{"env://GREETING": greeting}})
		e.srv.parent = md
		id, ok, err := src.ContentIdentity(engine.ContextWithClientMetadata(e.base, md))
		require.NoError(t, err)
		require.True(t, ok)
		return id.String()
	}
	require.NotEqual(t, identityFor("first", "hello"), identityFor("later", "bonjour"),
		"defaults expanded from different environments are different defaults")
}

// Cloning an SDK config copies its maps: the experimental-feature fields
// write into the clone's, and the source it was cloned from may be held by
// other clients.
func TestSDKConfigCloneSharesNoMaps(t *testing.T) {
	t.Parallel()
	orig := &core.SDKConfig{
		Source:       "go",
		Config:       map[string]any{"goprivate": "example.com"},
		Experimental: map[string]bool{"SELF_CALLS": true},
	}
	cp := orig.Clone()
	cp.Experimental["SELF_CALLS"] = false
	cp.Config["goprivate"] = "elsewhere.com"
	require.True(t, orig.Experimental["SELF_CALLS"], "the original's features are its own")
	require.Equal(t, "example.com", orig.Config["goprivate"], "the original's config is its own")
}
