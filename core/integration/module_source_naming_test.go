package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
	"golang.org/x/mod/semver"

	"github.com/dagger/dagger/engine"
)

// namingEquivalenceDang is a Dang module whose declarations the two naming
// rules spell differently: a type, an interface and an enum, fields,
// functions and arguments with acronyms, and a field, a function and an
// argument all named loadThingFromID, which are named alike. Its main type is
// mainType.
//
// namingProbe asks ModuleSource.naming about another module source, which
// declares cutVersion and has no SDK, from inside this module: at a pre-cut
// module's view, as the SDK modules that generate code call it.
func namingEquivalenceDang(mainType, cutVersion string) string {
	return `enum CpuArch {
  ARM64
  HTTP2
  P_256
}

interface JsonReader {
  pub readURL(baseURL: String!): String!
}

type HttpClient implements JsonReader {
  pub baseURL: String!
  pub loadThingFromID: String!

  new(baseURL: String!) {
    self.baseURL = baseURL
    self.loadThingFromID = baseURL
    self
  }

  pub readURL(baseURL: String!): String! {
    baseURL
  }

  pub getURL(path: String!): String! {
    self.baseURL + path
  }
}

type ` + mainType + ` {
  pub client(baseURL: String!): HttpClient! {
    HttpClient(baseURL: baseURL)
  }

  pub loadThingFromID(loadThingFromID: String!): String! {
    loadThingFromID
  }

  pub arch(cpuArch: CpuArch!): CpuArch! {
    cpuArch
  }

  pub namingProbe: [String!]! {
    directory.
      withNewFile("dagger.json", "{\"name\":\"my-mod\",\"engineVersion\":\"` + cutVersion + `\"}").
      asModuleSource.
      naming.
      typeNames(names: ["HttpClient", "MyMod"])
  }

  pub currentTypeNames: [String!]! {
    currentModule.naming.typeNames(names: ["HttpClient", "JsonReader", "CpuArch", "` + mainType + `"])
  }

  pub currentFieldNames: [String!]! {
    currentModule.naming.fieldNames(names: ["baseURL", "loadThingFromID", "readURL", "e2eTest"])
  }
}
`
}

// gqlStrings renders names as a GraphQL list literal.
func gqlStrings(names []string) string {
	out, err := json.Marshal(names)
	if err != nil {
		panic(err)
	}
	return string(out)
}

// gjsonStrings reads a JSON array of strings.
func gjsonStrings(res gjson.Result) []string {
	var out []string
	for _, v := range res.Array() {
		out = append(out, v.String())
	}
	return out
}

// requireQueryNamingMatches checks that Query.naming, at the version naming
// reports, answers for the module naming reports, by its name (and its
// original name, when it differs), exactly what naming answers: naming is a
// ModuleNaming with selection selected, which must include version, name and
// originalName.
func requireQueryNamingMatches(ctx context.Context, t *testctx.T, ctr *core.Container, naming gjson.Result, selection string) {
	t.Helper()
	version := naming.Get("version").String()
	name := naming.Get("name").String()
	originalName := naming.Get("originalName").String()
	require.NotEmpty(t, version, naming.Raw)
	moduleArgs := fmt.Sprintf("name: %q", name)
	if originalName != name {
		moduleArgs += fmt.Sprintf(", originalName: %q", originalName)
	}
	out, err := ctr.With(daggerQuery(`{naming(version: %q) { version module(%s) { %s } }}`, version, moduleArgs, selection)).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, version, gjson.Get(out, "naming.version").String(), out)
	require.JSONEq(t, naming.Raw, gjson.Get(out, "naming.module").Raw)
}

// TestNamingBeforeEntrypoint uses a real source with no SDK or entrypoint,
// not an in-memory ModuleSource. Naming must work without loading it, from
// either caller view, and Query.naming must reproduce all its answers.
func (ModuleNamingSuite) TestNamingBeforeEntrypoint(ctx context.Context, t *testctx.T) {
	for _, callerVersion := range []string{"v1.0.0-beta.20", namingCut} {
		t.Run(callerVersion, func(ctx context.Context, t *testctx.T) {
			q := core.NewQuery(connect(ctx, t, dagger.WithVersionOverride(callerVersion)))
			for _, declared := range []string{"v1.0.0-beta.20", namingCut, "", "v1.999.0"} {
				t.Run(declared, func(ctx context.Context, t *testctx.T) {
					src := q.Directory().WithNewFile("dagger.json", fmt.Sprintf(`{"name":"my-mod","engineVersion":%q}`, declared)).AsModuleSource()
					n := src.Naming()
					version, err := n.Version(ctx)
					if declared == "v1.999.0" {
						// Query.naming can answer for a future version, but a real
						// source must first pass module version compatibility.
						require.ErrorContains(t, err, "module requires dagger v1.999.0")
						return
					}
					require.NoError(t, err)
					declaredVersion, err := src.EngineVersion(ctx)
					require.NoError(t, err)
					require.Equal(t, engine.ModuleAPIViewVersion(declaredVersion), version)
					name, err := n.Name(ctx)
					require.NoError(t, err)
					require.Equal(t, "my-mod", name)
					original, err := n.OriginalName(ctx)
					require.NoError(t, err)
					require.Equal(t, name, original)
					primitive := q.Naming(core.NamingOpts{Version: version}).Module(name, core.NamingModuleOpts{OriginalName: original})
					// Omission of originalName means name, not an empty prefix.
					defaultOriginal, err := q.Naming(core.NamingOpts{Version: version}).Module(name).OriginalName(ctx)
					require.NoError(t, err)
					require.Equal(t, original, defaultOriginal)
					for _, field := range []struct {
						name  string
						call  func(*core.ModuleNaming, []string) ([]string, error)
						names []string
					}{
						{"types", func(n *core.ModuleNaming, names []string) ([]string, error) { return n.TypeNames(ctx, names) }, []string{"HttpClient", "MyMod", "MyModThing", "HttpClient"}},
						{"fields", func(n *core.ModuleNaming, names []string) ([]string, error) { return n.FieldNames(ctx, names) }, []string{"getURL", "e2eTest", "loadFooFromID"}},
						{"members", func(n *core.ModuleNaming, names []string) ([]string, error) { return n.EnumMemberNames(ctx, names) }, []string{"Arm64", "HTTP2", "P_256"}},
					} {
						got, err := field.call(n, field.names)
						require.NoError(t, err, field.name)
						want, err := field.call(primitive, field.names)
						require.NoError(t, err, field.name)
						require.Equal(t, want, got, field.name)
						empty, err := field.call(n, []string{})
						require.NoError(t, err, field.name)
						require.Empty(t, empty, field.name)
					}
					for _, candidates := range [][]string{{"Helper", "MyMod"}, {"Helper"}, {}} {
						got, err := n.MainObject(ctx, candidates)
						require.NoError(t, err)
						want, err := primitive.MainObject(ctx, candidates)
						require.NoError(t, err)
						require.Equal(t, want, got)
					}
					// Unlike primitive identifier formatting, ModuleNaming retains
					// legacy strcase's permissiveness before the cut.
					_, err = n.FieldNames(ctx, []string{"ok", "héllo"})
					if semver.Compare(version, namingCut) >= 0 {
						require.ErrorContains(t, err, `"héllo"`)
					} else {
						require.NoError(t, err)
					}
				})
			}
		})
	}
}

// TestModuleSourceNamingMatchesSchema checks ModuleSource.naming against the
// names the engine actually registers once the module is loaded, for a module
// pinned before the identifier casing cut (strcase) and one at the cut: its
// types, fields, functions, arguments and enum members, and its main object.
// naming is asked first, on a module source that hasn't been loaded.
func (ModuleNamingSuite) TestModuleSourceNamingMatchesSchema(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	module := func(name, version string) string {
		return "name = \"" + name + "\"\nengineVersion = \"" + version + "\"\nsource = \".\"\n\n[runtime]\nsource = \"dang\"\n"
	}
	ws := goGitBase(t, c).
		WithNewFile("dagger.toml", "[modules.api-before]\nsource = \"./api-before\"\n[modules.api-after]\nsource = \"./api-after\"\n").
		WithNewFile("api-before/dagger-module.toml", module("api-before", "v1.0.0-beta.20")).
		WithNewFile("api-before/main.dang", namingEquivalenceDang("ApiBefore", namingCut)).
		WithNewFile("api-after/dagger-module.toml", module("api-after", namingCut)).
		WithNewFile("api-after/main.dang", namingEquivalenceDang("ApiAfter", namingCut))

	// The declarations, as the source spells them.
	const (
		httpClient = "HttpClient"
		jsonReader = "JsonReader"
		cpuArch    = "CpuArch"
	)
	httpClientFields := []string{"baseURL", "loadThingFromID"}
	httpClientFunctions := []string{"readURL", "getURL"}
	mainFunctions := []string{"client", "loadThingFromID", "arch", "namingProbe", "currentTypeNames", "currentFieldNames"}
	args := []string{"baseURL", "path", "loadThingFromID", "cpuArch"}
	// Dang only lets enum members be written in GraphQL's convention, which
	// both rules keep; the unit tests cover the others.
	members := []string{"ARM64", "HTTP2", "P_256"}

	for _, tc := range []struct {
		module, mainType string
		legacy           bool
	}{
		{"api-before", "ApiBefore", true},
		{"api-after", "ApiAfter", false},
	} {
		t.Run(tc.module, func(ctx context.Context, t *testctx.T) {
			types := []string{httpClient, jsonReader, cpuArch, tc.mainType}
			functions := append(append([]string{}, httpClientFunctions...), mainFunctions...)
			selection := fmt.Sprintf(`
  version
  name
  originalName
  typeNames(names: %s)
  fields: fieldNames(names: %s)
  args: fieldNames(names: %s)
  functions: fieldNames(names: %s)
  enumMemberNames(names: %s)
  mainObject(candidates: %s)
  currentFields: fieldNames(names: ["baseURL", "loadThingFromID", "readURL", "e2eTest"])
`, gqlStrings(types), gqlStrings(httpClientFields), gqlStrings(args),
				gqlStrings(functions), gqlStrings(members), gqlStrings([]string{httpClient, tc.mainType}))
			out, err := ws.With(daggerQuery(`{moduleSource(refString: %q) { naming { %s } }}`, "./"+tc.module, selection)).Stdout(ctx)
			require.NoError(t, err)
			naming := gjson.Get(out, "moduleSource.naming")
			require.Equal(t, tc.module, naming.Get("name").String())
			require.Equal(t, tc.module, naming.Get("originalName").String())
			requireQueryNamingMatches(ctx, t, ws, naming, selection)
			answer := func(field string, names []string) map[string]string {
				got := gjsonStrings(naming.Get(field))
				require.Len(t, got, len(names), field)
				m := map[string]string{}
				for i, name := range names {
					m[name] = got[i]
				}
				return m
			}
			typeName := answer("typeNames", types)
			fieldName := answer("fields", httpClientFields)
			argName := answer("args", args)
			functionName := answer("functions", functions)
			memberName := answer("enumMemberNames", members)
			require.Equal(t, tc.mainType, naming.Get("mainObject").String())

			// The two rules differ here, so the test tells them apart.
			if tc.legacy {
				require.Equal(t, "ApiBeforeHttpClient", typeName[httpClient])
			} else {
				require.Equal(t, "APIAfterHTTPClient", typeName[httpClient])
			}
			// Functions, fields and arguments are named alike, load…FromID
			// included.
			require.Equal(t, "loadThingFromId", functionName["loadThingFromID"])
			require.Equal(t, "loadThingFromId", fieldName["loadThingFromID"])
			require.Equal(t, "loadThingFromId", argName["loadThingFromID"])

			// What the engine registers once it loads the module.
			var q strings.Builder
			q.WriteString("{\n")
			for i, name := range types {
				fmt.Fprintf(&q, "  t%d: __type(name: %q) { name kind fields { name args { name } } enumValues { name } }\n", i, typeName[name])
			}
			q.WriteString("}")
			out, err = ws.With(daggerQueryAt(tc.module, "%s", q.String())).Stdout(ctx)
			require.NoError(t, err)
			registered := map[string]gjson.Result{}
			for i, name := range types {
				typ := gjson.Get(out, fmt.Sprintf("t%d", i))
				require.True(t, typ.Exists() && typ.Type != gjson.Null, "%s is registered as %s: %s", name, typeName[name], out)
				require.Equal(t, typeName[name], typ.Get("name").String())
				registered[name] = typ
			}
			require.Equal(t, "OBJECT", registered[httpClient].Get("kind").String())
			require.Equal(t, "INTERFACE", registered[jsonReader].Get("kind").String())
			require.Equal(t, "ENUM", registered[cpuArch].Get("kind").String())

			// fieldArgs maps each field of typ to its arguments.
			fieldArgs := func(typ gjson.Result) map[string][]string {
				m := map[string][]string{}
				for _, f := range typ.Get("fields").Array() {
					m[f.Get("name").String()] = gjsonStrings(f.Get("args.#.name"))
				}
				return m
			}
			names := func(m map[string]string, keys ...string) []string {
				var out []string
				for _, k := range keys {
					out = append(out, m[k])
				}
				return out
			}

			client := fieldArgs(registered[httpClient])
			require.ElementsMatch(t,
				append(append([]string{"id"}, names(fieldName, httpClientFields...)...), names(functionName, httpClientFunctions...)...),
				namingKeys(client))
			require.Equal(t, []string{argName["baseURL"]}, client[functionName["readURL"]])
			require.Equal(t, []string{argName["path"]}, client[functionName["getURL"]])

			reader := fieldArgs(registered[jsonReader])
			require.ElementsMatch(t, []string{"id", functionName["readURL"]}, namingKeys(reader))
			require.Equal(t, []string{argName["baseURL"]}, reader[functionName["readURL"]])

			main := fieldArgs(registered[tc.mainType])
			require.ElementsMatch(t, append([]string{"id"}, names(functionName, mainFunctions...)...), namingKeys(main))
			require.Equal(t, []string{argName["baseURL"]}, main[functionName["client"]])
			require.Equal(t, []string{argName["loadThingFromID"]}, main[functionName["loadThingFromID"]])
			require.Equal(t, []string{argName["cpuArch"]}, main[functionName["arch"]])

			require.ElementsMatch(t, names(memberName, members...), gjsonStrings(registered[cpuArch].Get("enumValues.#.name")))

			// The main object naming picked is the one the module is
			// constructed as. A module asks naming about another source, at
			// its own view (pre-cut, for api-before), and gets that source's
			// names.
			out, err = ws.With(daggerQueryAt(tc.module, `{ __typename probe: namingProbe }`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"__typename": "Query", "probe": ["MyModHTTPClient", "MyMod"]}`, out)

			// The module's runtime has no ModuleSource: CurrentModule.naming
			// answers the same as its source's.
			out, err = ws.With(daggerQueryAt(tc.module, `{ %s %s }`, functionName["currentTypeNames"], functionName["currentFieldNames"])).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, names(typeName, types...), gjsonStrings(gjson.Get(out, functionName["currentTypeNames"])), out)
			require.Equal(t, gjsonStrings(naming.Get("currentFields")), gjsonStrings(gjson.Get(out, functionName["currentFieldNames"])), out)

			constructor := namingConstructorName(tc.module)
			out, err = ws.With(daggerQuery(`{ %s { __typename } }`, constructor)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, typeName[tc.mainType], gjson.Get(out, constructor+".__typename").String(), out)
		})
	}
}

// TestCurrentModuleNamingAliases checks CurrentModule.naming inside one module
// loaded twice in a session, as two dependencies under different names: each
// load gets the names of its own (aliased) source. The two loads share an
// implementation-scoped CurrentModule, whose source carries only one of the
// names. Query.naming answers the same for the version, name and original
// name each reports.
func (ModuleNamingSuite) TestCurrentModuleNamingAliases(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	module := func(name, deps string) string {
		return "name = \"" + name + "\"\nengineVersion = \"" + namingCut + "\"\nsource = \".\"\n\n[runtime]\nsource = \"dang\"\n" + deps
	}
	dep := func(name string) string {
		return "\n[[dependencies]]\nname = \"" + name + "\"\nsource = \"../shared\"\n"
	}
	ws := goGitBase(t, c).
		WithNewFile("dagger.toml", "[modules.pa]\nsource = \"./pa\"\n[modules.pb]\nsource = \"./pb\"\n").
		WithNewFile("shared/dagger-module.toml", module("shared", "")).
		WithNewFile("shared/main.dang", `type Thing {
  pub name: String! {
    "thing"
  }
}

type Shared {
  pub names(tag: String!): [String!]! {
    currentModule.naming.typeNames(names: ["Thing"])
  }

  pub identity(tag: String!): [String!]! {
    let n = currentModule.naming
    [n.version, n.name, n.originalName]
  }

  pub prefixed(tag: String!): [String!]! {
    currentModule.naming.typeNames(names: ["SharedThing", "Shared"])
  }
}
`).
		WithNewFile("pa/dagger-module.toml", module("pa", dep("alias-one"))).
		WithNewFile("pa/main.dang", `type Pa {
  pub names: [String!]! {
    aliasOne.names(tag: "a")
  }

  pub identity: [String!]! {
    aliasOne.identity(tag: "a")
  }

  pub prefixed: [String!]! {
    aliasOne.prefixed(tag: "a")
  }
}
`).
		WithNewFile("pb/dagger-module.toml", module("pb", dep("alias-two"))).
		WithNewFile("pb/main.dang", `type Pb {
  pub names: [String!]! {
    aliasTwo.names(tag: "b")
  }

  pub identity: [String!]! {
    aliasTwo.identity(tag: "b")
  }

  pub prefixed: [String!]! {
    aliasTwo.prefixed(tag: "b")
  }
}
`)
	out, err := ws.With(daggerQuery(`{ pa { names identity prefixed } pb { names identity prefixed } }`)).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, []string{"AliasOneThing"}, gjsonStrings(gjson.Get(out, "pa.names")), out)
	require.Equal(t, []string{"AliasTwoThing"}, gjsonStrings(gjson.Get(out, "pb.names")), out)
	// Types are namespaced under the alias, after the original name.
	require.Equal(t, []string{"AliasOneThing", "AliasOne"}, gjsonStrings(gjson.Get(out, "pa.prefixed")), out)
	require.Equal(t, []string{"AliasTwoThing", "AliasTwo"}, gjsonStrings(gjson.Get(out, "pb.prefixed")), out)

	// Query.naming answers the same for each load's version, name and
	// original name.
	for mod, alias := range map[string]string{"pa": "alias-one", "pb": "alias-two"} {
		identity := gjsonStrings(gjson.Get(out, mod+".identity"))
		// Without the original name, already-prefixed declarations cannot
		// have the intrinsic prefix removed. This must differ from the
		// caller-bound naming, rather than just comparing two API paths.
		withoutOriginal, err := core.NewQuery(c).Naming(core.NamingOpts{Version: namingCut}).Module(alias).
			TypeNames(ctx, []string{"SharedThing", "Shared"})
		require.NoError(t, err)
		require.NotEqual(t, gjsonStrings(gjson.Get(out, mod+".prefixed")), withoutOriginal)
		require.Len(t, identity, 3, out)
		require.Equal(t, engine.ModuleAPIViewVersion(namingCut), identity[0], out)
		require.Equal(t, alias, identity[1], out)
		require.Equal(t, "shared", identity[2], out)
		naming, err := json.Marshal(map[string]any{
			"version":      identity[0],
			"name":         identity[1],
			"originalName": identity[2],
			"names":        gjsonStrings(gjson.Get(out, mod+".names")),
			"prefixed":     gjsonStrings(gjson.Get(out, mod+".prefixed")),
		})
		require.NoError(t, err)
		requireQueryNamingMatches(ctx, t, ws, gjson.ParseBytes(naming), `version name originalName
  names: typeNames(names: ["Thing"])
  prefixed: typeNames(names: ["SharedThing", "Shared"])`)
	}
}

// TestNamingDangToml checks Query.naming against ModuleSource.naming for a
// module whose dang.toml declares its engine version: the version naming
// reports is the dang.toml's, not its dagger.json's.
func (ModuleNamingSuite) TestNamingDangToml(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := dangTomlModule(t, c, "v1.0.0-beta.20", `type Tiny {
  pub client: HttpClient! { HttpClient }
}

type HttpClient {
  pub ping: String! { "pong" }
}
`, map[string]string{
		"/dagger.toml": "[modules.tiny]\nsource = \"tiny\"\n",
		"dang.toml":    "[imports.Dagger]\ndagger = \"" + namingCut + "\"\n",
	})
	const selection = `version name originalName
  typeNames(names: ["HttpClient", "Tiny"])
  fieldNames(names: ["getURL", "loadFooFromID"])
  enumMemberNames(names: ["Arm64"])
  mainObject(candidates: ["HttpClient", "Tiny"])`
	out, err := ws.With(daggerQuery(`{moduleSource(refString: "./tiny") { naming { %s } }}`, selection)).Stdout(ctx)
	require.NoError(t, err)
	naming := gjson.Get(out, "moduleSource.naming")
	require.Equal(t, engine.ModuleAPIViewVersion(namingCut), naming.Get("version").String(), out)
	require.Equal(t, []string{"TinyHTTPClient", "Tiny"}, gjsonStrings(naming.Get("typeNames")), out)
	requireQueryNamingMatches(ctx, t, ws, naming, selection)
}

// TestMainTypeSchemaName checks modules whose main type is spelled as the
// first answer of ModuleSource.naming's typeNames for the module's own name,
// as SDKs and templates name it, on either side of the cut. strcase isn't
// idempotent on these names (k8s-tools is K8STools, which it spells K8Stools),
// so the legacy main object lookup must take the schema name as it is. The
// module loads, is constructed as that type, and mainObject agrees.
func (ModuleNamingSuite) TestMainTypeSchemaName(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		version string
		want    map[string]string
	}{
		{"v1.0.0-beta.20", map[string]string{"k8s-tools": "K8STools", "e2e-test": "E2ETest", "http-server": "HttpServer"}},
		{namingCut, map[string]string{"k8s-tools": "K8sTools", "e2e-test": "E2ETest", "http-server": "HTTPServer"}},
	} {
		t.Run(tc.version, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			names := []string{"k8s-tools", "e2e-test", "http-server"}
			var toml strings.Builder
			for _, name := range names {
				fmt.Fprintf(&toml, "[modules.%s]\nsource = \"./%s\"\n", name, name)
			}
			// No workspace modules yet: nothing loads them while there's no
			// code to load.
			ws := goGitBase(t, c)
			for _, name := range names {
				ws = ws.WithNewFile(name+"/dagger-module.toml", "name = \""+name+"\"\nengineVersion = \""+tc.version+"\"\nsource = \".\"\n\n[runtime]\nsource = \"dang\"\n")
			}

			// The names, asked before there's any code to load.
			schemaNames := map[string]string{}
			constructors := map[string]string{}
			for _, name := range names {
				out, err := ws.With(daggerQuery(`{moduleSource(refString: %q) { naming { typeNames(names: [%q]) fieldNames(names: [%q]) } }}`, "./"+name, name, name)).Stdout(ctx)
				require.NoError(t, err)
				schemaNames[name] = gjson.Get(out, "moduleSource.naming.typeNames.0").String()
				constructors[name] = gjson.Get(out, "moduleSource.naming.fieldNames.0").String()
				require.NotEmpty(t, schemaNames[name], out)
				if want, ok := tc.want[name]; ok {
					require.Equal(t, want, schemaNames[name])
				}
				ws = ws.WithNewFile(name+"/main.dang", `type `+schemaNames[name]+` {
  pub greet: String! {
    "hello"
  }
}
`)
			}
			ws = ws.WithNewFile("dagger.toml", toml.String())

			var query strings.Builder
			query.WriteString("{")
			for _, name := range names {
				out, err := ws.With(daggerQuery(`{moduleSource(refString: %q) { naming { mainObject(candidates: [%q]) } }}`, "./"+name, schemaNames[name])).Stdout(ctx)
				require.NoError(t, err)
				require.Equal(t, schemaNames[name], gjson.Get(out, "moduleSource.naming.mainObject").String(), name)
				fmt.Fprintf(&query, " %s { __typename greet }", constructors[name])
			}
			query.WriteString(" }")

			// Each loads, constructed as its schema name.
			out, err := ws.With(daggerQuery("%s", query.String())).Stdout(ctx)
			require.NoError(t, err)
			for _, name := range names {
				require.Equal(t, schemaNames[name], gjson.Get(out, constructors[name]+".__typename").String(), out)
				require.Equal(t, "hello", gjson.Get(out, constructors[name]+".greet").String(), out)
			}
		})
	}
}

// namingConstructorName is the Query field of a module named
// api-<suffix>: camelCase, the same by either rules.
func namingConstructorName(module string) string {
	head, tail, _ := strings.Cut(module, "-")
	return head + strings.ToUpper(tail[:1]) + tail[1:]
}

func namingKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
