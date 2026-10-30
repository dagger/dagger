package schema

import (
	"net/http"
	"testing"

	"github.com/99designs/gqlgen/client"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// ModuleSource.naming is served on every v1 view, like formatIdentifiers: SDK
// modules that call it declare pre-cut versions (v1.0.0-beta.20).
func TestModuleSourceNamingViews(t *testing.T) {
	for _, tc := range []struct {
		view call.View
		want bool
	}{
		{"", true},
		{call.View(engine.IdentifierCasingVersion), true},
		{call.View(engine.APIViewVersion("v1.0.0-beta.20")), true},
		{call.View(engine.APIViewVersion("v1.0.0-0")), true},
		{"v0.21.0", false},
	} {
		t.Run(string(tc.view), func(t *testing.T) {
			ctx, dag := newNestingTestServer(t, tc.view)
			h := dagql.NewDefaultHandler(dag)
			gql := client.New(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				h.ServeHTTP(w, r.WithContext(ctx))
			}))
			var res struct {
				ModuleSource struct {
					Fields []struct{ Name string }
				}
				CurrentModule struct {
					Fields []struct{ Name string }
				}
				ModuleNaming *struct {
					Fields []struct{ Name string }
				}
			}
			query := `{
				moduleSource: __type(name: "ModuleSource") { fields { name } }
				currentModule: __type(name: "CurrentModule") { fields { name } }
			}`
			if tc.want {
				query = `{
					moduleSource: __type(name: "ModuleSource") { fields { name } }
					currentModule: __type(name: "CurrentModule") { fields { name } }
					moduleNaming: __type(name: "ModuleNaming") { fields { name } }
				}`
			}
			require.NoError(t, gql.Post(query, &res))
			var fields, currentFields []string
			for _, f := range res.ModuleSource.Fields {
				fields = append(fields, f.Name)
			}
			for _, f := range res.CurrentModule.Fields {
				currentFields = append(currentFields, f.Name)
			}
			if !tc.want {
				require.NotContains(t, fields, "naming")
				require.NotContains(t, currentFields, "naming")
				return
			}
			require.Contains(t, fields, "naming")
			require.Contains(t, currentFields, "naming")
			require.NotNil(t, res.ModuleNaming)
			var namingFields []string
			for _, f := range res.ModuleNaming.Fields {
				namingFields = append(namingFields, f.Name)
			}
			require.ElementsMatch(t, []string{"id", "typeNames", "fieldNames", "enumMemberNames", "mainObject"}, namingFields)
		})
	}
}

// ModuleSource.naming resolves at a pre-cut view as at the cut: it answers by
// the engine version the module declares, whatever the caller's view.
func TestModuleSourceNamingResolves(t *testing.T) {
	type answers struct {
		types, fields, members []string
		main                   string
	}
	legacy := answers{
		types:   []string{"MyModHttpClient", "MyMod"},
		fields:  []string{"getUrl", "e2ETest", "loadFooFromId"},
		members: []string{"ARM_64", "HTTP2"},
		main:    "MyMod",
	}
	latest := answers{
		types:   []string{"MyModHTTPClient", "MyMod"},
		fields:  []string{"getUrl", "e2eTest", "loadFooFromId"},
		members: []string{"ARM64", "HTTP2"},
		main:    "MyMod",
	}
	for _, view := range []call.View{
		call.View(engine.APIViewVersion("v1.0.0-beta.20")),
		call.View(engine.IdentifierCasingVersion),
	} {
		for _, tc := range []struct {
			engineVersion string
			want          answers
		}{
			{"v1.0.0-beta.20", legacy},
			{engine.IdentifierCasingVersion, latest},
		} {
			t.Run(string(view)+"/"+tc.engineVersion, func(t *testing.T) {
				ctx, dag := newNestingTestServer(t, view)
				// No entrypoint, no SDK: nothing is loaded.
				src, err := dagql.NewObjectResultForCall(&core.ModuleSource{
					ModuleName:         "my-mod",
					ModuleOriginalName: "my-mod",
					EngineVersion:      tc.engineVersion,
				}, dag, &dagql.ResultCall{
					Kind:        dagql.ResultCallKindSynthetic,
					SyntheticOp: "naming-source-" + tc.engineVersion,
					Type:        dagql.NewResultCallType((&core.ModuleSource{}).Type()),
				})
				require.NoError(t, err)

				var naming dagql.ObjectResult[*core.ModuleNaming]
				require.NoError(t, dag.Select(ctx, src, &naming, dagql.Selector{Field: "naming"}))

				strs := func(field, arg string, names ...string) []string {
					var out dagql.Array[dagql.String]
					require.NoError(t, dag.Select(ctx, naming, &out, dagql.Selector{
						Field: field,
						Args:  []dagql.NamedInput{{Name: arg, Value: dagql.ArrayInput[dagql.String](toDagqlStrings(names))}},
					}))
					res := make([]string, len(out))
					for i, s := range out {
						res[i] = string(s)
					}
					return res
				}
				require.Equal(t, tc.want.types, strs("typeNames", "names", "HttpClient", "MyMod"))
				require.Equal(t, tc.want.fields, strs("fieldNames", "names", "getURL", "e2eTest", "loadFooFromID"))
				require.Equal(t, tc.want.members, strs("enumMemberNames", "names", "Arm64", "HTTP2"))

				var main dagql.Nullable[dagql.String]
				require.NoError(t, dag.Select(ctx, naming, &main, dagql.Selector{
					Field: "mainObject",
					Args:  []dagql.NamedInput{{Name: "candidates", Value: dagql.ArrayInput[dagql.String]{"Helper", "MyMod"}}},
				}))
				require.True(t, main.Valid)
				require.Equal(t, tc.want.main, string(main.Value))

				var none dagql.Nullable[dagql.String]
				require.NoError(t, dag.Select(ctx, naming, &none, dagql.Selector{
					Field: "mainObject",
					Args:  []dagql.NamedInput{{Name: "candidates", Value: dagql.ArrayInput[dagql.String]{"Helper"}}},
				}))
				require.False(t, none.Valid)

				// A name the rules can't parse fails loudly, naming it; the
				// legacy rules name anything.
				var bad dagql.Array[dagql.String]
				err = dag.Select(ctx, naming, &bad, dagql.Selector{
					Field: "fieldNames",
					Args:  []dagql.NamedInput{{Name: "names", Value: dagql.ArrayInput[dagql.String]{"ok", "héllo"}}},
				})
				if tc.engineVersion == engine.IdentifierCasingVersion {
					require.ErrorContains(t, err, `"héllo"`)
				} else {
					require.NoError(t, err)
				}
			})
		}
	}
}

// CurrentModule.naming, for module runtimes, answers what ModuleSource.naming
// answers for the module's source: its view and its own (manifest) name, even
// when the module is loaded under another.
func TestCurrentModuleNaming(t *testing.T) {
	for _, view := range []call.View{
		call.View(engine.APIViewVersion("v1.0.0-beta.20")),
		call.View(engine.IdentifierCasingVersion),
	} {
		for _, engineVersion := range []string{"v1.0.0-beta.20", engine.IdentifierCasingVersion} {
			t.Run(string(view)+"/"+engineVersion, func(t *testing.T) {
				ctx, dag := newNestingTestServer(t, view)
				synthetic := func(op string, typ dagql.Typed) *dagql.ResultCall {
					return &dagql.ResultCall{
						Kind:        dagql.ResultCallKindSynthetic,
						SyntheticOp: op + "-" + engineVersion,
						Type:        dagql.NewResultCallType(typ.Type()),
					}
				}
				src, err := dagql.NewObjectResultForCall(&core.ModuleSource{
					ModuleName:         "my-mod",
					ModuleOriginalName: "my-mod",
					EngineVersion:      engineVersion,
				}, dag, synthetic("naming-source", &core.ModuleSource{}))
				require.NoError(t, err)
				// Loaded under another name, as a workspace may.
				mod, err := dagql.NewObjectResultForCall(&core.Module{
					NameField:    "renamed",
					OriginalName: "my-mod",
					Source:       dagql.NonNull(src),
				}, dag, synthetic("naming-module", &core.Module{}))
				require.NoError(t, err)
				cur, err := dagql.NewObjectResultForCall(&core.CurrentModule{Module: mod}, dag, synthetic("naming-current-module", &core.CurrentModule{}))
				require.NoError(t, err)

				var fromSource, fromCurrent dagql.ObjectResult[*core.ModuleNaming]
				require.NoError(t, dag.Select(ctx, src, &fromSource, dagql.Selector{Field: "naming"}))
				require.NoError(t, dag.Select(ctx, cur, &fromCurrent, dagql.Selector{Field: "naming"}))
				require.Equal(t, fromSource.Self(), fromCurrent.Self())

				names := dagql.ArrayInput[dagql.String]{"HttpClient", "MyMod"}
				var a, b dagql.Array[dagql.String]
				require.NoError(t, dag.Select(ctx, fromSource, &a, dagql.Selector{Field: "typeNames", Args: []dagql.NamedInput{{Name: "names", Value: names}}}))
				require.NoError(t, dag.Select(ctx, fromCurrent, &b, dagql.Selector{Field: "typeNames", Args: []dagql.NamedInput{{Name: "names", Value: names}}}))
				require.Equal(t, a, b)
				require.Equal(t, dagql.String("MyMod"), b[1], "the manifest name, not the name it's loaded under")
			})
		}
	}
}

func toDagqlStrings(names []string) []dagql.String {
	out := make([]dagql.String, len(names))
	for i, name := range names {
		out[i] = dagql.String(name)
	}
	return out
}
