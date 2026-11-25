package schema

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/dagger/dagger/internal/buildkit/util/compression"
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

// A module loaded from a source with a content identity outlives the session
// that loaded it, as its definitions do: once that session ends, as a
// check's does, the next session on the engine loads nothing again, and an
// engine sent the module, as the Cloud sends it, loads nothing either.
// Before, a loaded module was kept only while something kept depended on it.
func TestModuleLoadedOutlivesItsSession(t *testing.T) {
	t.Parallel()
	salt := transferTestSalt(t)
	a := newTransferTestEngine(t, salt)

	first := a.session(t, "first")
	moduleIdentityTestLoad(t, a, first, moduleIdentityTestSource(t, a, first, "kept"))
	require.NoError(t, a.cache.ReleaseSession(a.base, "first-session"))
	require.NoError(t, a.cache.WaitSessionRelease(a.base, "first-session"))

	later := a.session(t, "later")
	kept := moduleIdentityTestLoad(t, a, later, moduleIdentityTestSource(t, a, later, "kept"))
	require.True(t, kept.HitCache(), "the module outlives the session that loaded it")

	b := newTransferTestEngine(t, salt)
	selection := dagql.ValueSelection{Roots: []dagql.AnyResult{kept}}
	require.NoError(t, a.cache.WithExportedValues(later, selection, config.RefConfig{Compression: compression.New(compression.Uncompressed)}, func(_ context.Context, exported *dagql.ExportedValues) error {
		b1 := b.session(t, "b1")
		_, err := b.cache.MergeValues(b1, dagql.CloudCacheID, exported.Bundle)
		require.NoError(t, err)
		require.True(t, moduleIdentityTestLoad(t, b, b1, moduleIdentityTestSource(t, b, b1, "kept")).HitCache(),
			"another engine sent the module loads nothing again")
		return nil
	}))
}

// Two sessions that read a module source from the same directory read it
// once: the later session's read is the earlier's. Before, every client read
// every source again, its .env and its dependencies included.
func TestModuleSourceReadFromEqualContentIsShared(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	first := e.session(t, "first")
	moduleIdentityTestSource(t, e, first, "read")
	later := e.session(t, "later")
	require.True(t, moduleIdentityTestSource(t, e, later, "read").HitCache(), "the later session's source is the earlier's, not read again")
}

// A module source read from a workspace with a content identity is read once
// for every session reading the same workspace, as the workspace's files are.
func TestWorkspaceModuleSourceReadIsShared(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	read := func(t *testing.T, ctx context.Context) dagql.ObjectResult[*core.ModuleSource] {
		t.Helper()
		var src dagql.ObjectResult[*core.ModuleSource]
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &src,
			dagql.Selector{Field: "directory"},
			dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("mod/dagger.json")},
				{Name: "contents", Value: dagql.NewString(`{"name":"workspaced"}`)},
			}},
			dagql.Selector{Field: "asWorkspace"},
			dagql.Selector{Field: "moduleSource", Args: []dagql.NamedInput{{Name: "path", Value: dagql.NewString("/mod")}}}))
		return src
	}
	read(t, e.session(t, "first"))
	require.True(t, read(t, e.session(t, "later")).HitCache(), "the later session's source is the earlier's")
}

// A caller that reads its own host gives a module source its .env and may
// name paths on its machine, so its reads stay its own; module code and
// host-less clients read none of that, and their reads are keyed by what
// they read and kept past the session.
func TestModuleSourceReadByAHostReadingCallerIsItsOwn(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	key := func(ctx context.Context) *dagql.CallRequest {
		t.Helper()
		req := &dagql.CallRequest{ResultCall: &dagql.ResultCall{Field: "asModuleSource"}}
		require.NoError(t, moduleSourceReadCacheKey(ctx, req, true, false))
		return req
	}
	perClient := func(req *dagql.CallRequest) bool {
		for _, input := range req.ImplicitInputs {
			if input != nil && input.Name == dagql.PerClientInput.Name {
				return true
			}
		}
		return false
	}

	module := key(e.session(t, "module"))
	require.False(t, perClient(module), "a module's own read is keyed by what it reads")
	require.True(t, module.IsPersistable)

	host := e.session(t, "host")
	md, err := engine.ClientMetadataFromContext(host)
	require.NoError(t, err)
	e.srv.parent = md
	hostRead := key(host)
	require.True(t, perClient(hostRead), "a caller that reads its own host keeps its read")
	require.False(t, hostRead.IsPersistable)
}

// A caller that busts its cache deliberately (dagql.WithPerClientCacheScope),
// as updating a module's dependencies does to resolve them live, reads a
// shared module source again rather than taking the read made before it.
func TestModuleSourceReadUnderAFreshCacheScopeIsReadAgain(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	ctx := e.session(t, "reader")
	moduleIdentityTestSource(t, e, ctx, "scoped")
	require.True(t, moduleIdentityTestSource(t, e, ctx, "scoped").HitCache(), "the same read again is the earlier's")
	require.False(t, moduleIdentityTestSource(t, e, dagql.WithPerClientCacheScope(ctx), "scoped").HitCache(), "a read under a fresh cache scope is made again")
}

// A shared module-source read holds what its engine decides as it reads: the
// version "latest" names, and the built-in SDKs its image holds. An engine of
// another build reads the source again rather than taking another's read.
// Not parallel: it replaces the engine's build for the package.
func TestModuleSourceReadByAnotherEngineBuildIsReadAgain(t *testing.T) {
	e := newTransferTestEngine(t, transferTestSalt(t))
	moduleIdentityTestSource(t, e, e.session(t, "first"), "built")
	require.True(t, moduleIdentityTestSource(t, e, e.session(t, "same-build"), "built").HitCache(), "an engine of the same build takes the read")

	build := moduleSourceEngineBuild
	t.Cleanup(func() { moduleSourceEngineBuild = build })
	moduleSourceEngineBuild = func() string { return "v0.0.0-another+build" }
	require.False(t, moduleIdentityTestSource(t, e, e.session(t, "another-build"), "built").HitCache(), "an engine of another build reads the source again")
}

// A shared module-source read is keyed by its engine's default platform, as
// a module's function calls are: an entrypoint's sources can read the
// platform and give the module other files on an engine of another one.
func TestModuleSourceReadIsKeyedByItsEnginePlatform(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	ctx := e.session(t, "module")
	req := &dagql.CallRequest{ResultCall: &dagql.ResultCall{Field: "asModuleSource"}}
	require.NoError(t, moduleSourceReadCacheKey(ctx, req, true, false))
	platform, err := core.CurrentEngineDefaultPlatform(ctx)
	require.NoError(t, err)
	var keyed string
	for _, input := range req.ImplicitInputs {
		if input != nil && input.Name == core.EngineDefaultPlatformInput && input.Value != nil {
			keyed = input.Value.StringValue
		}
	}
	require.Equal(t, platform.Format(), keyed, "the read is keyed by its engine's default platform")
}

// A call keyed by whose credentials its caller's Git reads may borrow
// (core.GitBorrowingInput), as a planning expansion is, is made again for a
// caller with another permission: module runtime code, which may borrow
// nothing; trusted resolution of a module's dependencies; and a client that
// is its own non-module parent, such as the CLI. A caller with the same
// permission takes the call made before.
// Not parallel: it makes one of its sessions the engine's non-module client.
func TestCallKeyedByGitBorrowingIsMadeAgainForAnotherPermission(t *testing.T) {
	e := newTransferTestEngine(t, transferTestSalt(t))
	var calls atomic.Int32
	dagql.Fields[*core.Query]{
		dagql.Func("borrowingExpansion", func(context.Context, *core.Query, struct{}) (dagql.String, error) {
			calls.Add(1)
			return dagql.NewString("expanded"), nil
		}).WithInput(core.GitBorrowingInput),
	}.Install(e.dag)
	expand := func(ctx context.Context) {
		t.Helper()
		var out dagql.String
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &out, dagql.Selector{Field: "borrowingExpansion"}))
	}

	module := e.session(t, "module")
	expand(module)
	expand(module)
	require.Equal(t, int32(1), calls.Load(), "a caller with the same permission takes the call made before")

	expand(core.WithModuleDependencyResolution(module))
	require.Equal(t, int32(2), calls.Load(), "trusted resolution makes the call again")

	host := e.session(t, "host")
	md, err := engine.ClientMetadataFromContext(host)
	require.NoError(t, err)
	e.srv.parent = md
	expand(host)
	require.Equal(t, int32(3), calls.Load(), "a client that is its own non-module parent makes the call again")
}

// A read of a module source by a ref is shared only when the ref names what
// is read without its caller. A ref that could be a local directory is its
// caller's to decide, and so is a vanity ref, which resolves through the
// session's workspace lock or the network: their reads keep their per-client
// key. A ref to a known code host, such as github.com, read as Git, as a
// module's dependency is read, names its repository outright, and module
// code's read of it is shared.
func TestModuleSourceReadByRefIsSharedOnlyWhenTheRefDecidesIt(t *testing.T) {
	t.Parallel()
	e := newTransferTestEngine(t, transferTestSalt(t))
	ctx := e.session(t, "module")
	perClient := func(ref string, git bool) bool {
		t.Helper()
		args := moduleSourceArgs{RefString: ref}
		if git {
			args.RequireKind = dagql.Opt(core.ModuleSourceKindGit)
		}
		req := &dagql.CallRequest{ResultCall: &dagql.ResultCall{Field: "moduleSource"}}
		require.NoError(t, (&moduleSourceSchema{}).moduleSourceCacheKey(ctx, dagql.ObjectResult[*core.Query]{}, args, req))
		for _, input := range req.ImplicitInputs {
			if input != nil && input.Name == dagql.PerClientInput.Name {
				return true
			}
		}
		return false
	}
	const known = "github.com/dagger/go@79a92beb4df0898400ef7f7a632009035290383b"
	require.True(t, perClient("./mod", false), "a local path is its caller's")
	require.True(t, perClient("mod", false), "a ref that could be a local directory is its caller's")
	require.True(t, perClient(known, false), "a ref without a scheme could be a local directory")
	require.True(t, perClient("example.com/go@v1.2.3", true), "a vanity ref is its caller's")
	require.False(t, perClient(known, true), "a known code host's ref read as Git is shared")
}

// withSdk loads the SDK it names, and is shared by what decides it as a read
// of a module source is. What loading the SDK takes from its session, such
// as an SDK's vanity ref resolved through the session's workspace lock,
// keeps the result to its session, as it does for the read: a later session
// loads the SDK again, and the same session takes the result it made.
// Not parallel: it replaces the package's SDK loader.
func TestModuleSourceWithSDKKeepsWhatItReadToItsSession(t *testing.T) {
	loadSDK := loadModuleSourceSDK
	var loads atomic.Int32
	loadModuleSourceSDK = func(ctx context.Context, _ *core.Query, _ *core.SDKConfig, _ *core.ModuleSource) (core.SDK, error) {
		loads.Add(1)
		dagql.RecordSessionRead(ctx, "the SDK's vanity ref")
		return &prebuildTestSDK{}, nil
	}
	t.Cleanup(func() { loadModuleSourceSDK = loadSDK })
	e := newTransferTestEngine(t, transferTestSalt(t))
	withSDK := func(ctx context.Context) {
		t.Helper()
		var src dagql.ObjectResult[*core.ModuleSource]
		require.NoError(t, e.dag.Select(ctx, e.dag.Root(), &src,
			dagql.Selector{Field: "directory"},
			dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString("dagger.json")},
				{Name: "contents", Value: dagql.NewString(`{"name":"sdkread"}`)},
			}},
			dagql.Selector{Field: "asModuleSource"},
			dagql.Selector{Field: "withSdk", Args: []dagql.NamedInput{
				{Name: "source", Value: dagql.NewString("example.com/sdk@v1.0.0")},
			}}))
	}
	first := e.session(t, "first")
	withSDK(first)
	withSDK(e.session(t, "later"))
	require.Equal(t, int32(2), loads.Load(), "a later session loads the SDK again")
	withSDK(first)
	require.Equal(t, int32(2), loads.Load(), "the same session takes the result it made")
}
