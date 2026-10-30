package core

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
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
	mainFunctions := []string{"client", "loadThingFromID", "arch", "namingProbe"}
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
			out, err := ws.With(daggerQuery(`{moduleSource(refString: %q) { naming {
  typeNames(names: %s)
  fields: fieldNames(names: %s)
  args: fieldNames(names: %s)
  functions: fieldNames(names: %s)
  enumMemberNames(names: %s)
  mainObject(candidates: %s)
} }}`, "./"+tc.module, gqlStrings(types), gqlStrings(httpClientFields), gqlStrings(args),
				gqlStrings(functions), gqlStrings(members), gqlStrings([]string{httpClient, tc.mainType}))).Stdout(ctx)
			require.NoError(t, err)
			naming := gjson.Get(out, "moduleSource.naming")
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
			constructor := namingConstructorName(tc.module)
			out, err = ws.With(daggerQuery(`{ %s { __typename } }`, constructor)).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, typeName[tc.mainType], gjson.Get(out, constructor+".__typename").String(), out)
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
