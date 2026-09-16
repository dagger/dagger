package core

import (
	"context"
	"testing"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine/snapshots/config"
	"github.com/opencontainers/go-digest"
	"github.com/stretchr/testify/require"
)

type installedPreferenceServer struct {
	*persistedFamiliesTestQueryServer
	served *SchemaBuilder
}

func (s *installedPreferenceServer) CurrentServedDeps(context.Context) (*SchemaBuilder, error) {
	return s.served, nil
}
func installPreferenceScope(t *testing.T, srv *dagql.Server) {
	t.Helper()
	dagql.Fields[*Module]{dagql.NodeFunc("_implementationScoped", func(ctx context.Context, self dagql.ObjectResult[*Module], _ struct{}) (dagql.ObjectResult[*Module], error) {
		value, err := dagql.NewObjectResultForCurrentCall(ctx, srv, self.Self().Clone())
		if err != nil {
			return value, err
		}
		return value.WithContentDigest(ctx, digest.FromString("implementation:"+self.Self().Name()), call.ExtraDigestLabelRemoteCache)
	})}.Install(srv)
}
func preferenceModule(t *testing.T, env *persistedFamiliesTestEnv, ctx context.Context, cache *dagql.Cache, srv *dagql.Server, field, name string, dependencies ...dagql.ObjectResult[*Module]) dagql.ObjectResult[*Module] {
	t.Helper()
	query, err := CurrentQuery(ctx)
	require.NoError(t, err)
	source := env.attach(t, ctx, cache, srv, field+"-source", &ModuleSource{Kind: ModuleSourceKindLocal, ModuleName: name, ModuleOriginalName: name, SourceRootSubpath: ".", Local: &LocalModuleSource{ContextDirectoryPath: "/" + field}}).(dagql.ObjectResult[*ModuleSource])
	mods := make([]Mod, len(dependencies))
	for i, dep := range dependencies {
		mods[i] = NewUserMod(dep)
	}
	module := &Module{NameField: name, OriginalName: name, Source: dagql.NonNull(source), Deps: NewSchemaBuilder(query, mods)}
	return env.attach(t, ctx, cache, srv, field, module).(dagql.ObjectResult[*Module])
}

func TestModDepsForCallInstalledPreference(t *testing.T) {
	for _, before := range []bool{true, false} {
		name := "import after installation"
		if before {
			name = "import before installation"
		}
		t.Run(name, func(t *testing.T) {
			a := newPersistedFamiliesTestEnv(t, "a")
			actx, ac, asrv := a.open(t)
			installPreferenceScope(t, asrv)
			amod := preferenceModule(t, a, actx, ac, asrv, "producer", "probe")
			scoped, err := ImplementationScopedModule(actx, amod)
			require.NoError(t, err)
			var bundle dagql.ValueBundle
			require.NoError(t, ac.WithExportedValues(actx, dagql.ValueSelection{Roots: []dagql.AnyResult{scoped}}, config.RefConfig{}, func(_ context.Context, v *dagql.ExportedValues) error { bundle = v.Bundle; return nil }))
			b := newPersistedFamiliesTestEnv(t, "b")
			ctx, cache, srv := b.open(t)
			installPreferenceScope(t, srv)
			query, err := CurrentQuery(ctx)
			require.NoError(t, err)
			facade := &installedPreferenceServer{persistedFamiliesTestQueryServer: query.Server.(*persistedFamiliesTestQueryServer)}
			query.Server = facade
			var mapping []dagql.ImportedValue
			if before {
				mapping, err = cache.ImportValues(ctx, bundle)
				require.NoError(t, err)
			}
			dep := preferenceModule(t, b, ctx, cache, srv, "consumer-dep", "dependency")
			installed := preferenceModule(t, b, ctx, cache, srv, "consumer", "probe", dep)
			facade.served = NewSchemaBuilder(query, []Mod{NewUserMod(installed), NewUserMod(dep)})
			if !before {
				mapping, err = cache.ImportValues(ctx, bundle)
				require.NoError(t, err)
			}
			recorded := mapping[0].ResultID
			candidates, err := query.installedSchemaModuleCandidates(ctx, cache)
			require.NoError(t, err)
			require.Len(t, candidates, 2, "direct modules and their transitive duplicates appear only once")
			installedID, err := cache.PersistedResultID(installed)
			require.NoError(t, err)
			require.Equal(t, installedID, candidates[0].ModuleResultID)
			loaded, err := cache.LoadResultByResultIDForSchema(ctx, b.session, srv, recorded, candidates)
			require.NoError(t, err)
			loadedID, err := cache.PersistedResultID(loaded)
			require.NoError(t, err)
			require.Equal(t, installedID, loadedID, "comparison must retain the operational Module")
			for _, position := range []string{"module", "receiver", "argument", "implicit", "ancestor"} {
				t.Run(position, func(t *testing.T) {
					ref := &dagql.ResultCallRef{ResultID: recorded}
					frame := &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "report", Type: dagql.NewResultCallType(dagql.String("").Type())}
					arg := &dagql.ResultCallArg{Name: "module", Value: &dagql.ResultCallLiteral{Kind: dagql.ResultCallLiteralKindResultRef, ResultRef: ref}}
					switch position {
					case "module":
						frame.Module = &dagql.ResultCallModule{Name: "probe", ResultRef: ref}
					case "receiver":
						frame.Receiver = ref
					case "argument":
						frame.Args = []*dagql.ResultCallArg{arg}
					case "implicit":
						frame.ImplicitInputs = []*dagql.ResultCallArg{arg}
					case "ancestor":
						frame.Receiver = &dagql.ResultCallRef{Call: &dagql.ResultCall{Kind: dagql.ResultCallKindField, Field: "ancestor", Type: frame.Type, Args: []*dagql.ResultCallArg{arg}}}
					}
					recovered, err := query.ModDepsForCall(ctx, frame)
					require.NoError(t, err)
					found := false
					for _, mod := range recovered.Mods() {
						if mod.ModuleResult().Self() != nil {
							id, err := cache.PersistedResultID(mod.ModuleResult())
							require.NoError(t, err)
							if id == installedID {
								found = true
							}
						}
					}
					require.True(t, found)
					require.Equal(t, recorded, ref.ResultID, "schema recovery leaves the recorded frame exact")
				})
			}
		})
	}
}
