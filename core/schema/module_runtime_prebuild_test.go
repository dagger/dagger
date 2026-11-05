package schema

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

// prebuildTestSDK is an SDK whose runtime is a container. runtime, when set,
// runs before Runtime returns, and prebuild, when set, makes the SDK a
// core.RuntimePrebuilder.
type prebuildTestSDK struct {
	container     dagql.ObjectResult[*core.Container]
	runtime       func(context.Context) error
	prebuild      func(context.Context) error
	runtimeCalls  atomic.Int32
	prebuildCalls atomic.Int32
}

type prebuildingTestSDK struct{ *prebuildTestSDK }

func (s *prebuildingTestSDK) PrebuildRuntime(ctx context.Context, _ dagql.ObjectResult[*core.ModuleSource]) error {
	s.prebuildCalls.Add(1)
	return s.prebuild(ctx)
}

func (s *prebuildTestSDK) sdk() core.SDK {
	if s.prebuild != nil {
		return &prebuildingTestSDK{s}
	}
	return s
}

func (s *prebuildTestSDK) Runtime(ctx context.Context, _ *core.SchemaBuilder, _ dagql.ObjectResult[*core.ModuleSource]) (core.ModuleRuntime, error) {
	s.runtimeCalls.Add(1)
	if s.runtime != nil {
		if err := s.runtime(ctx); err != nil {
			return nil, err
		}
	}
	return &core.ContainerRuntime{Container: s.container}, nil
}

func (s *prebuildingTestSDK) AsRuntime() (core.Runtime, bool)                  { return s, true }
func (s *prebuildTestSDK) AsRuntime() (core.Runtime, bool)                     { return s, true }
func (s *prebuildTestSDK) AsModuleTypes() (core.ModuleTypes, bool)             { return nil, false }
func (s *prebuildTestSDK) AsCodeGenerator() (core.CodeGenerator, bool)         { return nil, false }
func (s *prebuildTestSDK) AsClientGenerator() (core.ClientGenerator, bool)     { return nil, false }
func (s *prebuildTestSDK) AsModuleInitializer() (core.ModuleInitializer, bool) { return nil, false }
func (s *prebuildTestSDK) AsClientInitializer() (core.ClientInitializer, bool) { return nil, false }
func (s *prebuildTestSDK) AsRuntimeTarget() (core.RuntimeTarget, bool)         { return nil, false }
func (s *prebuildTestSDK) AsModule() (dagql.ObjectResult[*core.Module], bool) {
	return dagql.ObjectResult[*core.Module]{}, false
}
func (s *prebuildTestSDK) CloneForModuleSource(*core.ModuleSource) core.SDK { return s.sdk() }
func (s *prebuildTestSDK) AttachDependencyResults(context.Context, func(dagql.AnyResult) (dagql.AnyResult, error)) ([]dagql.AnyResult, error) {
	return nil, nil
}

// prebuildTestEnv is the module definition test env with the real asModule
// and digest installed, and the core schema as every module's default dependencies.
func prebuildTestEnv(t *testing.T, session string) (context.Context, *dagql.Cache, *dagql.Server, *moduleDefinitionTestResolver) {
	t.Helper()
	stub := &moduleDefinitionTestResolver{}
	ctx, cache, srv, server := moduleDefinitionTestEnv(t, "", session, stub)
	base, err := NewCoreSchemaBase(ctx, server)
	require.NoError(t, err)
	server.defaultDeps = core.NewSchemaBuilder(core.NewRoot(server), []core.Mod{base.CoreMod("")})
	dagql.Fields[*core.ModuleSource]{
		dagql.NodeFunc("asModule", (&moduleSourceSchema{}).moduleSourceAsModule),
		dagql.Func("digest", (&moduleSourceSchema{}).moduleSourceDigest),
	}.Install(srv)
	return ctx, cache, srv, stub
}

// prebuildTestSource attaches a module source named name whose SDK is sdk
// and whose dependencies are deps.
func prebuildTestSource(t *testing.T, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, session, name string, sdk *prebuildTestSDK, deps ...dagql.ObjectResult[*core.ModuleSource]) dagql.ObjectResult[*core.ModuleSource] {
	t.Helper()
	in := definitionTestInputsFor(t, ctx, cache, srv, session, name+"-source", digest.FromString(name), name+"-runtime", name+"-schema")
	sdk.container = in.runtime
	source := &core.ModuleSource{
		Kind:               core.ModuleSourceKindDir,
		ModuleName:         name,
		ModuleOriginalName: name,
		EngineVersion:      engine.Version,
		ContextDirectory:   in.source.Self().ContextDirectory,
		SDK:                &core.SDKConfig{Source: "scripted"},
		SDKImpl:            sdk.sdk(),
		Dependencies:       deps,
	}
	frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: name + "-module-source", Type: dagql.NewResultCallType(source.Type())}
	src, err := cache.GetOrInitCall(ctx, session, srv, &dagql.CallRequest{ResultCall: frame}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewObjectResultForCall(source, srv, frame)
	})
	require.NoError(t, err)
	return src.(dagql.ObjectResult[*core.ModuleSource])
}

func selectAsModule(ctx context.Context, srv *dagql.Server, src dagql.ObjectResult[*core.ModuleSource]) (dagql.ObjectResult[*core.Module], error) {
	var mod dagql.ObjectResult[*core.Module]
	err := srv.Select(ctx, src, &mod, dagql.Selector{Field: "asModule"})
	return mod, err
}

// A module whose SDK can build its runtime without the module's dependencies
// builds it while they load: here the dependency's load can't finish until
// the module's build has started.
func TestModuleRuntimeBuildsWhileDependenciesLoad(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, stub := prebuildTestEnv(t, "prebuild")

	buildStarted := make(chan struct{})
	depSDK := &prebuildTestSDK{runtime: func(ctx context.Context) error {
		select {
		case <-buildStarted:
			return nil
		case <-time.After(30 * time.Second):
			return errors.New("the dependency finished loading before the module's runtime build started")
		case <-ctx.Done():
			return ctx.Err()
		}
	}}
	dep := prebuildTestSource(t, ctx, cache, srv, "prebuild", "dep", depSDK)
	modSDK := &prebuildTestSDK{prebuild: func(context.Context) error {
		close(buildStarted)
		return nil
	}}
	src := prebuildTestSource(t, ctx, cache, srv, "prebuild", "mod", modSDK, dep)

	mod, err := selectAsModule(ctx, srv, src)
	require.NoError(t, err)
	require.EqualValues(t, 1, modSDK.prebuildCalls.Load(), "the module's runtime was prebuilt")
	require.EqualValues(t, 1, modSDK.runtimeCalls.Load(), "then selected as its runtime")
	require.EqualValues(t, 2, stub.runs.Load(), "both modules' definitions ran")
	require.True(t, mod.Self().Runtime.Valid)
	require.Equal(t, persistedID(t, cache, modSDK.container), persistedID(t, cache, mod.Self().Runtime.Value), "the module runs the runtime it built")
}

// A load that doesn't need the build, as when its definition is cached,
// returns without waiting for it, and stops it.
func TestModuleRuntimePrebuildNotAwaited(t *testing.T) {
	t.Parallel()
	ctx, cache, srv, _ := prebuildTestEnv(t, "not-awaited")
	dep := prebuildTestSource(t, ctx, cache, srv, "not-awaited", "dep", &prebuildTestSDK{})
	var buildStopped atomic.Bool
	modSDK := &prebuildTestSDK{prebuild: func(ctx context.Context) error {
		<-ctx.Done()
		buildStopped.Store(true)
		return ctx.Err()
	}}
	src := prebuildTestSource(t, ctx, cache, srv, "not-awaited", "mod", modSDK, dep)

	loaded := make(chan error, 1)
	go func() {
		_, err := selectAsModule(ctx, srv, src)
		loaded <- err
	}()
	select {
	case err := <-loaded:
		require.NoError(t, err)
	case <-time.After(30 * time.Second):
		t.Fatal("the load waited for a build it didn't need")
	}
	require.True(t, buildStopped.Load(), "the build was stopped")
}

// The engine prebuilds at most maxRuntimePrebuilds runtimes at once: here
// more modules than that wait on a dependency that loads only once the test
// lets it, so every one of them wants to prebuild at the same time. Not
// parallel: the bound is engine-wide, and the other prebuild tests would take
// its slots.
func TestModuleRuntimePrebuildsBounded(t *testing.T) {
	ctx, cache, srv, _ := prebuildTestEnv(t, "bounded")
	depLoads := make(chan struct{})
	dep := prebuildTestSource(t, ctx, cache, srv, "bounded", "dep", &prebuildTestSDK{runtime: func(ctx context.Context) error {
		select {
		case <-depLoads:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}})

	var inFlight, most atomic.Int32
	entered := make(chan struct{}, 2*maxRuntimePrebuilds)
	modules := 2 * maxRuntimePrebuilds
	srcs := make([]dagql.ObjectResult[*core.ModuleSource], modules)
	for i := range srcs {
		srcs[i] = prebuildTestSource(t, ctx, cache, srv, "bounded", fmt.Sprintf("mod%d", i), &prebuildTestSDK{prebuild: func(ctx context.Context) error {
			n := inFlight.Add(1)
			defer inFlight.Add(-1)
			for {
				m := most.Load()
				if n <= m || most.CompareAndSwap(m, n) {
					break
				}
			}
			entered <- struct{}{}
			<-ctx.Done()
			return ctx.Err()
		}}, dep)
	}

	loaded := make(chan error, modules)
	for _, src := range srcs {
		go func() {
			_, err := selectAsModule(ctx, srv, src)
			loaded <- err
		}()
	}
	for range maxRuntimePrebuilds {
		select {
		case <-entered:
		case <-time.After(30 * time.Second):
			t.Fatalf("only %d runtimes started prebuilding", inFlight.Load())
		}
	}
	// Give any prebuild past the bound time to start.
	select {
	case <-entered:
		t.Fatalf("%d runtimes prebuilt at once, more than %d", most.Load(), maxRuntimePrebuilds)
	case <-time.After(2 * time.Second):
	}
	close(depLoads)
	for range modules {
		require.NoError(t, <-loaded)
	}
	require.EqualValues(t, maxRuntimePrebuilds, most.Load())
}

// A dependency that fails to load fails the module with the dependency's
// error, as before, and stops the module's runtime build. A build that fails
// while the dependencies load fails the module with the build's error.
func TestModuleRuntimePrebuildFailures(t *testing.T) {
	t.Parallel()

	t.Run("dependency failure", func(t *testing.T) {
		t.Parallel()
		ctx, cache, srv, _ := prebuildTestEnv(t, "dep-fails")
		buildStarted := make(chan struct{})
		var buildCanceled atomic.Bool
		depSDK := &prebuildTestSDK{runtime: func(context.Context) error {
			<-buildStarted
			return errors.New("dependency is broken")
		}}
		dep := prebuildTestSource(t, ctx, cache, srv, "dep-fails", "dep", depSDK)
		modSDK := &prebuildTestSDK{prebuild: func(ctx context.Context) error {
			close(buildStarted)
			<-ctx.Done()
			buildCanceled.Store(true)
			return ctx.Err()
		}}
		src := prebuildTestSource(t, ctx, cache, srv, "dep-fails", "mod", modSDK, dep)

		_, err := selectAsModule(ctx, srv, src)
		require.ErrorContains(t, err, "dependency is broken")
		require.NotErrorIs(t, err, context.Canceled)
		require.True(t, buildCanceled.Load(), "the build was stopped")
		require.Zero(t, modSDK.runtimeCalls.Load())
	})

	t.Run("build failure", func(t *testing.T) {
		t.Parallel()
		ctx, cache, srv, stub := prebuildTestEnv(t, "build-fails")
		dep := prebuildTestSource(t, ctx, cache, srv, "build-fails", "dep", &prebuildTestSDK{})
		// The runtime is the build that failed, whether the load reports
		// the failure itself or reaches the runtime first.
		buildErr := fmt.Errorf("compile error")
		modSDK := &prebuildTestSDK{
			prebuild: func(context.Context) error { return buildErr },
			runtime:  func(context.Context) error { return buildErr },
		}
		src := prebuildTestSource(t, ctx, cache, srv, "build-fails", "mod", modSDK, dep)

		_, err := selectAsModule(ctx, srv, src)
		require.ErrorContains(t, err, "compile error")
		require.EqualValues(t, 1, stub.runs.Load(), "only the dependency's definition ran")
	})
}
