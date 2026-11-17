package core

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/util/gitutil"
	"github.com/stretchr/testify/require"
)

func nativeInputIDs(t *testing.T, cache *dagql.Cache, values ...dagql.AnyResult) []uint64 {
	t.Helper()
	ids := make([]uint64, len(values))
	for i, value := range values {
		ids[i] = persistedRowID(t, cache, value)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

func nativeFixedGraphContains(t *testing.T, ctx context.Context, cache *dagql.Cache, session string, root, target uint64) bool {
	t.Helper()
	pending, seen := []uint64{root}, map[uint64]bool{}
	for len(pending) != 0 {
		id := pending[0]
		pending = pending[1:]
		if seen[id] {
			continue
		}
		seen[id] = true
		if id == target {
			return true
		}
		inputs, err := cache.ResultPayloadInputIDs(ctx, session, id)
		require.NoError(t, err)
		pending = append(pending, inputs...)
	}
	return false
}

func TestNativePayloadInputsLiveAndRestored(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "native-fixed-inputs")
	ctx, cache, srv := env.open(t)
	install := func() { srv.InstallObject(dagql.NewClass(srv, dagql.ClassOpts[*Expertise]{})) }
	install()
	dir := func(name string) dagql.ObjectResult[*Directory] {
		return env.directory(t, ctx, cache, srv, name, name+"-snapshot")
	}
	contextDir, originalDir, unfilteredDir, viewDir := dir("native-context"), dir("native-original"), dir("native-unfiltered"), dir("native-view")
	workspace := env.attach(t, ctx, cache, srv, "native-workspace", &Workspace{Address: "file:///native", Cwd: "/", rootfs: contextDir}).(dagql.ObjectResult[*Workspace])
	origin := env.attach(t, ctx, cache, srv, "native-origin", &GitRepository{Backend: &LocalGitRepository{Directory: originalDir}, Remote: &gitutil.Remote{}}).(dagql.ObjectResult[*GitRepository])
	blueprint := env.attach(t, ctx, cache, srv, "native-blueprint", &ModuleSource{Kind: ModuleSourceKindDir, ModuleName: "blueprint"}).(dagql.ObjectResult[*ModuleSource])
	toolchain := env.attach(t, ctx, cache, srv, "native-toolchain", &ModuleSource{Kind: ModuleSourceKindDir, ModuleName: "toolchain"}).(dagql.ObjectResult[*ModuleSource])
	gitSource := env.attach(t, ctx, cache, srv, "native-git-source", &ModuleSource{
		Kind: ModuleSourceKindGit, ContextDirectory: contextDir,
		Git: &GitModuleSource{CloneRef: "https://example.com/native.git", UnfilteredContextDir: unfilteredDir},
	}).(dagql.ObjectResult[*ModuleSource])
	source := env.attach(t, ctx, cache, srv, "native-source", &ModuleSource{
		Kind: ModuleSourceKindDir, Workspace: workspace, ContextDirectory: contextDir,
		Dependencies: dagql.ObjectResultArray[*ModuleSource]{gitSource}, Blueprint: blueprint,
		Toolchains: dagql.ObjectResultArray[*ModuleSource]{toolchain},
		DirSrc:     &DirModuleSource{OriginalContextDir: originalDir, OriginGit: origin},
	}).(dagql.ObjectResult[*ModuleSource])
	mod := func(name string, src dagql.ObjectResult[*ModuleSource]) dagql.ObjectResult[*Module] {
		return env.attach(t, ctx, cache, srv, name, &Module{NameField: name, Source: dagql.NonNull(src), Deps: NewSchemaBuilder(nil, nil)}).(dagql.ObjectResult[*Module])
	}
	dependency, entrypoint, definition := mod("native-dependency", source), mod("native-entrypoint", gitSource), mod("native-definition", blueprint)
	object := env.attach(t, ctx, cache, srv, "native-object", NewObjectTypeDef("NativeObject", "", nil)).(dagql.ObjectResult[*ObjectTypeDef])
	iface := env.attach(t, ctx, cache, srv, "native-interface", NewInterfaceTypeDef("NativeInterface", "")).(dagql.ObjectResult[*InterfaceTypeDef])
	enum := env.attach(t, ctx, cache, srv, "native-enum", NewEnumTypeDef("NativeEnum", "", dagql.ObjectResult[*SourceMap]{})).(dagql.ObjectResult[*EnumTypeDef])
	objectType := env.attach(t, ctx, cache, srv, "native-object-type", (&TypeDef{}).WithObject(object)).(dagql.ObjectResult[*TypeDef])
	interfaceType := env.attach(t, ctx, cache, srv, "native-interface-type", (&TypeDef{}).WithInterface(iface)).(dagql.ObjectResult[*TypeDef])
	enumType := env.attach(t, ctx, cache, srv, "native-enum-type", (&TypeDef{}).WithEnum(enum)).(dagql.ObjectResult[*TypeDef])
	runtime := env.attach(t, ctx, cache, srv, "native-runtime", NewContainer(Platform{OS: "linux", Architecture: "amd64"})).(dagql.ObjectResult[*Container])
	module := env.attach(t, ctx, cache, srv, "native-module", &Module{
		NameField: "native", Source: dagql.NonNull(source), ContextSource: dagql.NonNull(gitSource),
		Runtime: dagql.NonNull(runtime), Definition: dagql.NonNull(definition),
		Deps: NewSchemaBuilder(nil, []Mod{NewUserMod(dependency)}), EntrypointDeps: NewSchemaBuilder(nil, []Mod{NewUserMod(entrypoint)}),
		ObjectDefs: dagql.ObjectResultArray[*TypeDef]{objectType}, InterfaceDefs: dagql.ObjectResultArray[*TypeDef]{interfaceType}, EnumDefs: dagql.ObjectResultArray[*TypeDef]{enumType},
	})
	artifact := &Artifact{Node: &ModTreeNode{RootValue: contextDir, Module: dependency, OriginalModule: entrypoint, Type: objectType}, Workspace: workspace, ContextWorkspace: workspace, View: &ArtifactView{sources: []dagql.AnyObjectResult{viewDir}}}
	single := env.attach(t, ctx, cache, srv, "native-artifact", artifact)
	group := env.attach(t, ctx, cache, srv, "native-artifacts", &Artifacts{Entries: []*Artifact{artifact}})
	expertise := env.attach(t, ctx, cache, srv, "native-expertise", &Expertise{Artifact: artifact})
	items := ArtifactItems{Array: dagql.Array[*Artifact]{artifact}}
	itemsCall := moduleObjectTestSyntheticCall("native-artifact-items", items)
	itemResult, err := cache.GetOrInitCall(ctx, env.session, srv, &dagql.CallRequest{ResultCall: itemsCall, IsPersistable: true}, func(context.Context) (dagql.AnyResult, error) {
		return dagql.NewResultForCall(items, itemsCall)
	})
	require.NoError(t, err)
	artifactInputs := nativeInputIDs(t, cache, contextDir, dependency, entrypoint, objectType, workspace, viewDir)
	cases := []struct {
		name   string
		result dagql.AnyResult
		inputs []uint64
	}{
		{"module-source", source, nativeInputIDs(t, cache, workspace, contextDir, gitSource, blueprint, toolchain, originalDir, origin)},
		{"git-source", gitSource, nativeInputIDs(t, cache, contextDir, unfilteredDir)},
		{"module", module, nativeInputIDs(t, cache, source, gitSource, runtime, definition, dependency, entrypoint, objectType, interfaceType, enumType)},
		{"artifact", single, artifactInputs}, {"artifacts", group, artifactInputs}, {"expertise", expertise, artifactInputs}, {"artifact-items", itemResult, artifactInputs},
	}
	ids := make([]uint64, len(cases))
	for i, tc := range cases {
		ids[i] = persistedRowID(t, cache, tc.result)
	}
	targetID := persistedRowID(t, cache, contextDir)
	check := func(t *testing.T) {
		for i, tc := range cases {
			inputs, err := cache.ResultPayloadInputIDs(ctx, env.session, ids[i])
			require.NoError(t, err, tc.name)
			require.ElementsMatch(t, tc.inputs, inputs, tc.name)
			require.True(t, nativeFixedGraphContains(t, ctx, cache, env.session, ids[i], targetID), tc.name)
		}
	}
	t.Run("live", check)
	for _, tc := range cases {
		if tc.name != "artifact-items" {
			assertPersistedRefsMatchOwnership(t, ctx, cache, tc.result)
		}
		hook := tc.result.Unwrap().(dagql.HasDependencyResultsKinds)
		deps, err := hook.AttachDependencyResultsKinds(ctx, tc.result, func(dep dagql.AnyResult) (dagql.AnyResult, error) { return dep, nil })
		require.NoError(t, err)
		for _, dep := range deps {
			require.True(t, dep.Owned, tc.name)
		}
	}
	ctx, cache, srv = env.restart(t, ctx, cache)
	install()
	// Check restored declarations before decoding their payloads.
	t.Run("restored", check)
	for i, tc := range cases {
		loaded, err := cache.LoadResultByResultID(ctx, env.session, srv, ids[i])
		require.NoError(t, err, tc.name)
		require.Equal(t, ids[i], persistedRowID(t, cache, loaded), tc.name)
	}
}

func TestNativePayloadInputLifetimes(t *testing.T) {
	env := newPersistedFamiliesTestEnv(t, "native-input-lifetimes")
	ctx, cache, srv := env.open(t)
	stored := env.directory(t, ctx, cache, srv, "native-stored", "native-stored-snapshot")
	provider := env.directory(t, ctx, cache, srv, "native-provider", "native-provider-snapshot")
	sourceValue := &ModuleSource{Kind: ModuleSourceKindDir, ContextDirectory: stored, SDKImpl: &moduleSourceAttachTestSDK{dep: provider}}
	source := env.attach(t, ctx, cache, srv, "native-provider-source", sourceValue).(dagql.ObjectResult[*ModuleSource])
	sourceID, storedID, providerID := persistedRowID(t, cache, source), persistedRowID(t, cache, stored), persistedRowID(t, cache, provider)
	inputs, err := cache.ResultPayloadInputIDs(ctx, env.session, sourceID)
	require.NoError(t, err)
	require.Equal(t, []uint64{storedID}, inputs, "SDK provider ownership alone does not declare an input")
	deps, err := sourceValue.AttachDependencyResultsKinds(ctx, source, func(dep dagql.AnyResult) (dagql.AnyResult, error) { return dep, nil })
	require.NoError(t, err)
	require.Contains(t, deps, dagql.DependencyResult{Result: sourceValue.SDKImpl.(*moduleSourceAttachTestSDK).dep, Owned: true})
	explicit := sourceValue.Clone()
	explicit.ContextDirectory = provider
	explicitResult := env.attach(t, ctx, cache, srv, "native-explicit-provider", explicit)
	inputs, err = cache.ResultPayloadInputIDs(ctx, env.session, persistedRowID(t, cache, explicitResult))
	require.NoError(t, err)
	require.Equal(t, []uint64{providerID}, inputs, "an explicit stored occurrence of the provider row is an input")
	module := env.attach(t, ctx, cache, srv, "native-self-module", &Module{NameField: "native-self", Source: dagql.NonNull(source), Deps: NewSchemaBuilder(nil, nil), IncludeSelfInDeps: true})
	moduleID := persistedRowID(t, cache, module)
	inputs, err = cache.ResultPayloadInputIDs(ctx, env.session, moduleID)
	require.NoError(t, err)
	require.Equal(t, []uint64{sourceID}, inputs, "self is excluded while the stored source remains fixed")

	t.Run("busy native parent", func(t *testing.T) {
		started, release, done := make(chan struct{}), make(chan struct{}), make(chan error, 1)
		go func() {
			done <- cache.RunLazyTask(ctx, module, "lazy:native-fixed-inputs", dagql.LazyTaskSpec{Body: func(context.Context) error {
				close(started)
				<-release
				return nil
			}})
		}()
		defer func() {
			close(release)
			select {
			case err := <-done:
				require.NoError(t, err)
			case <-time.After(10 * time.Second):
				t.Error("native parent task did not stop")
			}
		}()
		select {
		case <-started:
		case <-time.After(10 * time.Second):
			t.Fatal("native parent task did not start")
		}
		_, err := cache.CapturePersistedRecord(ctx, module)
		require.ErrorIs(t, err, dagql.ErrPersistStateNotReady)
		reads := &workspaceInputReads{values: map[uint64]*dagql.ResultCallRef{moduleID: {ResultID: moduleID}}}
		collector := &workspaceInputCollector{ctx: ctx, cache: cache, server: srv, session: env.session}
		require.True(t, reads.containsInput(collector, &dagql.ResultCallRef{ResultID: storedID}), "the current fixed graph remains valid while native outputs evaluate")
		require.False(t, nativeFixedGraphContains(t, ctx, cache, env.session, moduleID, providerID), "provider-only dependencies stay outside the fixed graph")
	})
}
