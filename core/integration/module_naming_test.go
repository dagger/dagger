package core

// Module type, field, function and argument names are normalized by the
// naming rules of the engine version a module declares: engine/naming from
// core.IdentifierNamingVersion (v1.0.0-beta.21) on, strcase before. Every
// earlier v1.0.0 prerelease, v1.0.0-0 included, keeps strcase. There are no
// aliases: a module's schema has the names of its own rules only. See
// hack/designs/identifier-casing.md.

import (
	"context"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/cmd/codegen/introspection"
)

type ModuleNamingSuite struct{}

func TestModuleNaming(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(ModuleNamingSuite{})
}

const namingGoSource = `package main

import (
	"dagger/test/internal/dagger"
)

type Test struct{}

var _ *dagger.Directory

// An HTTP client.
type HTTPClient struct {
	BaseURL string
}

func (m *Test) Client(baseURL string) *HTTPClient {
	return &HTTPClient{BaseURL: baseURL}
}

func (c *HTTPClient) GetURL(path string) string {
	return c.BaseURL + path
}

func (m *Test) Run(e2eTest string) string {
	return "ran " + e2eTest
}
`

// namingGoEnumSource extends namingGoSource with an enum whose members the
// new rules name differently than strcase did.
//
// It doesn't reference core types whose names start with an acronym
// (JSONValue, LLMMessage): the in-repo Go SDK's codegen still names a
// module's self-binding schema by strcase, so such a module at the new rules
// doesn't build. That fix belongs to the Go SDK (github.com/dagger/go-sdk);
// TestSameTypeDefsAcrossVersions covers the engine side with Dang.
const namingGoEnumSource = namingGoSource + `
type Arch string

const (
	Arm64   Arch = "arm64"
	IPv6    Arch = "ipv6"
	GitHub  Arch = "github"
	E2ETest Arch = "e2e"
)

func (m *Test) FromArch(arch Arch) string {
	return string(arch)
}

func (m *Test) FromArchs(archs []Arch) string {
	var out string
	for _, arch := range archs {
		out += string(arch) + ","
	}
	return out
}

func (m *Test) DefaultArch() Arch {
	return Arm64
}
`

func namingFieldNames(typ *introspection.Type) []string {
	var names []string
	for _, field := range typ.Fields {
		names = append(names, field.Name)
	}
	return names
}

func namingField(t testing.TB, typ *introspection.Type, name string) *introspection.Field {
	t.Helper()
	for _, field := range typ.Fields {
		if field.Name == name {
			return field
		}
	}
	require.Failf(t, "missing field", "%s has no field %q; has %v", typ.Name, name, namingFieldNames(typ))
	return nil
}

func (ModuleNamingSuite) TestGoAcronyms(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// "latest" pins the module to this engine's version, which is past
	// core.IdentifierNamingVersion.
	modGen := legacySDKModule(t, c, "go", namingGoEnumSource, "latest")
	schema := currentSchema(ctx, t, modGen)

	client := schema.Types.Get("TestHTTPClient")
	require.NotNil(t, client, "module types keep their acronyms")
	require.Nil(t, schema.Types.Get("TestHttpclient"))
	require.ElementsMatch(t, []string{"baseURL", "getURL", "id"}, namingFieldNames(client))
	getURL := namingField(t, client, "getURL")
	require.Equal(t, "path", getURL.Args[0].Name)

	test := schema.Types.Get("Test")
	require.NotNil(t, test)
	require.Equal(t, "baseURL", namingField(t, test, "client").Args[0].Name)
	require.Equal(t, "e2eTest", namingField(t, test, "run").Args[0].Name)

	out, err := modGen.With(daggerQuery(`{client(baseURL:"https://example.com"){getURL(path:"/x")}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"client":{"getURL":"https://example.com/x"}}`, out)

	out, err = modGen.With(daggerCall("client", "--base-url=https://example.com", "get-url", "--path=/y")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/y", strings.TrimSpace(out))

	// The legacy spellings are not part of the module's schema.
	_, err = modGen.With(daggerQuery(`{client(baseUrl:"https://example.com"){getUrl(path:"/x")}}`)).Stdout(ctx)
	requireErrOut(t, err, "baseUrl")

	// Enum members are named by the new rules too.
	arch := schema.Types.Get("TestArch")
	require.NotNil(t, arch)
	var archValues []string
	for _, value := range arch.EnumValues {
		archValues = append(archValues, value.Name)
	}
	require.ElementsMatch(t, []string{"ARM64", "IPV6", "GITHUB", "E2E_TEST"}, archValues)
	out, err = modGen.With(daggerQuery(`{
		arm: fromArch(arch: ARM64)
		ipv6: fromArch(arch: IPV6)
		list: fromArchs(archs: [GITHUB, E2E_TEST])
		defaultArch
	}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"arm":"arm64","ipv6":"ipv6","list":"github,e2e,","defaultArch":"ARM64"}`, out)

	// The CLI still resolves the spellings the legacy rules gave the members,
	// and sends the canonical names.
	out, err = modGen.With(daggerCall("from-arch", "--arch=ARM_64")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "arm64", strings.TrimSpace(out))
	out, err = modGen.With(daggerCall("from-archs", "--archs=GIT_HUB,E_2_E_TEST")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "github,e2e,", strings.TrimSpace(out))
}

// TestGoAcronymsVersions checks the cut: a module that declares
// v1.0.0-beta.21 or later gets the new names, and one that declares any
// earlier version, v1.0.0 prereleases included, keeps the strcase names it
// always had, byte for byte.
func (ModuleNamingSuite) TestGoAcronymsVersions(ctx context.Context, t *testctx.T) {
	for _, tc := range []struct {
		version string
		legacy  bool
	}{
		{"v1.0.0-beta.21", false},
		{"v1.0.0-beta.20", true},
		{"v1.0.0-beta.19", true},
		{"v1.0.0-0", true},
		{"v0.21.9", true},
	} {
		t.Run(tc.version, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := legacySDKModule(t, c, "go", namingGoSource, tc.version)
			schema := currentSchema(ctx, t, modGen)
			test := schema.Types.Get("Test")
			require.NotNil(t, test)

			if !tc.legacy {
				client := schema.Types.Get("TestHTTPClient")
				require.NotNil(t, client)
				require.Nil(t, schema.Types.Get("TestHttpclient"))
				require.ElementsMatch(t, []string{"baseURL", "getURL", "id"}, namingFieldNames(client))
				require.Equal(t, "baseURL", namingField(t, test, "client").Args[0].Name)
				require.Equal(t, "e2eTest", namingField(t, test, "run").Args[0].Name)

				out, err := modGen.With(daggerQuery(`{client(baseURL:"https://example.com"){getURL(path:"/x")}}`)).Stdout(ctx)
				require.NoError(t, err)
				require.JSONEq(t, `{"client":{"getURL":"https://example.com/x"}}`, out)
				return
			}

			client := schema.Types.Get("TestHttpclient")
			require.NotNil(t, client, "legacy modules keep their strcase names")
			require.Nil(t, schema.Types.Get("TestHTTPClient"))
			require.ElementsMatch(t, []string{"baseUrl", "getUrl", "id"}, namingFieldNames(client))
			require.Equal(t, "baseUrl", namingField(t, test, "client").Args[0].Name)
			require.Equal(t, "e2ETest", namingField(t, test, "run").Args[0].Name)

			out, err := modGen.With(daggerQuery(`{client(baseUrl:"https://example.com"){getUrl(path:"/x")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"client":{"getUrl":"https://example.com/x"}}`, out)
		})
	}
}

// namingDangSource is a Dang module whose types, fields and arguments the
// two naming rules spell differently; mainType is the module's main object,
// and extra is added to it.
func namingDangSource(mainType, extra string) string {
	return `type HttpClient {
  pub baseUrl: String!

  new(baseUrl: String!) {
    self.baseUrl = baseUrl
    self
  }

  pub getUrl(path: String!): String! {
    self.baseUrl + path
  }
}

type ` + mainType + ` {
  pub client(baseUrl: String!): HttpClient! {
    HttpClient(baseUrl: baseUrl)
  }
` + extra + `}
`
}

// namingDangCoreTypeRef is a function returning a core type whose name
// starts with an acronym. Only modules at the new rules get it: the legacy
// strcase rules spell JSONValue "Jsonvalue", which has never resolved to the
// core type.
const namingDangCoreTypeRef = `
  pub greeting: JSONValue! {
    json.newString("hi")
  }
`

// TestSameTypeDefsAcrossVersions checks that the naming rules are kept apart
// by version, with no cache cross-talk: two modules served side by side, one
// declaring v1.0.0-beta.20 and one v1.0.0-beta.21, declare identically named
// typedefs. Their runtimes make the same typedef constructor calls
// (withObject("HttpClient"), withField("baseUrl"), ...), differing only in
// the view the calls record, and each module must get its own rules' names.
func (ModuleNamingSuite) TestSameTypeDefsAcrossVersions(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	module := func(name, version string) string {
		return "name = \"" + name + "\"\nengineVersion = \"" + version + "\"\nsource = \".\"\n\n[runtime]\nsource = \"dang\"\n"
	}
	ws := goGitBase(t, c).
		WithNewFile("dagger.toml", "[modules.before]\nsource = \"./before\"\n[modules.after]\nsource = \"./after\"\n").
		WithNewFile("before/dagger-module.toml", module("before", "v1.0.0-beta.20")).
		WithNewFile("before/main.dang", namingDangSource("Before", "")).
		WithNewFile("after/dagger-module.toml", module("after", "v1.0.0-beta.21")).
		WithNewFile("after/main.dang", namingDangSource("After", namingDangCoreTypeRef))

	// The new rules' reference to a core type whose name starts with an
	// acronym resolves to the core type, not to a namespaced copy.
	const query = `{
  before { client(baseUrl: "https://a.example") { __typename baseUrl getUrl(path: "/x") } }
  after { client(baseURL: "https://b.example") { __typename baseURL getURL(path: "/y") } greeting { asString } }
}`
	const want = `{
  "before": {"client": {"__typename": "BeforeHttpClient", "baseUrl": "https://a.example", "getUrl": "https://a.example/x"}},
  "after": {"client": {"__typename": "AfterHTTPClient", "baseURL": "https://b.example", "getURL": "https://b.example/y"}, "greeting": {"asString": "hi"}}
}`
	out, err := ws.With(daggerQuery(query)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, want, out)

	// Each module alone, in either order, gets the same names: nothing one
	// module's load cached leaks into the other's.
	for _, mod := range []struct{ path, query, want string }{
		{"after", `{client(baseURL: "u") { __typename baseURL }}`, `{"client": {"__typename": "AfterHTTPClient", "baseURL": "u"}}`},
		{"before", `{client(baseUrl: "u") { __typename baseUrl }}`, `{"client": {"__typename": "BeforeHttpClient", "baseUrl": "u"}}`},
	} {
		out, err := ws.With(daggerQueryAt(mod.path, mod.query)).Stdout(ctx)
		require.NoError(t, err, mod.path)
		require.JSONEq(t, mod.want, out, mod.path)
	}
}

// TestMainTypeSpelling checks a module whose main type is spelled otherwise
// than its name around digits and acronyms: K8STools in module k8s-tools,
// which engine/naming cuts into different words (K8STools vs K8sTools). It's
// still the main object, with a constructor, and a type prefixed with it is
// namespaced under the module once.
func (ModuleNamingSuite) TestMainTypeSpelling(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)
	ws := goGitBase(t, c).
		WithNewFile("dagger.toml", "[modules.k8s-tools]\nsource = \"./k8s-tools\"\n").
		WithNewFile("k8s-tools/dagger-module.toml", "name = \"k8s-tools\"\nengineVersion = \"v1.0.0-beta.21\"\nsource = \".\"\n\n[runtime]\nsource = \"dang\"\n").
		WithNewFile("k8s-tools/main.dang", `type K8SToolsClient {
  pub name: String!

  new(name: String!) {
    self.name = name
    self
  }
}

type K8STools {
  pub greet: String! {
    "hello"
  }

  pub client(name: String!): K8SToolsClient! {
    K8SToolsClient(name: name)
  }
}
`)

	out, err := ws.With(daggerCallAt("k8s-tools", "greet")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello", strings.TrimSpace(out))

	out, err = ws.With(daggerQuery(`{k8sTools { __typename greet client(name: "x") { __typename name } }}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"k8sTools": {"__typename": "K8sTools", "greet": "hello", "client": {"__typename": "K8sToolsClient", "name": "x"}}}`, out)
}

// TestCLIFlags checks the CLI's spelling of an E2E-style argument. The CLI
// names flags by the rules of its own engine version (--e2e-test), whichever
// rules the module's schema names follow, and still accepts the spelling
// older CLIs gave (--e-2-e-test).
func (ModuleNamingSuite) TestCLIFlags(ctx context.Context, t *testctx.T) {
	for _, version := range []string{"latest", "v0.21.9"} {
		t.Run(version, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)
			modGen := legacySDKModule(t, c, "go", namingGoSource, version)

			help, err := modGen.With(daggerCall("run", "--help")).Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, help, "--e2e-test")
			require.NotContains(t, help, "--e-2-e-test")

			for _, flag := range []string{"--e2e-test", "--e-2-e-test"} {
				out, err := modGen.With(daggerCall("run", flag+"=x")).Stdout(ctx)
				require.NoError(t, err, flag)
				require.Equal(t, "ran x", strings.TrimSpace(out), flag)
			}
		})
	}
}

func (ModuleNamingSuite) TestPythonAcronyms(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := pythonModInit(t, c, `
import dagger


@dagger.object_type
class HTTPClient:
    base_url: str = dagger.field()

    @dagger.function
    def get_url(self, path: str) -> str:
        return self.base_url + path


@dagger.object_type
class Test:
    @dagger.function
    def client(self, base_url: str) -> HTTPClient:
        return HTTPClient(base_url=base_url)

    @dagger.function
    def run(self, e2e_test: str) -> str:
        return "ran " + e2e_test
`)
	schema := currentSchema(ctx, t, modGen)

	// The same names as the Go module: the schema looks the same whichever
	// language a module is written in.
	client := schema.Types.Get("TestHTTPClient")
	require.NotNil(t, client)
	require.ElementsMatch(t, []string{"baseURL", "getURL", "id"}, namingFieldNames(client))
	test := schema.Types.Get("Test")
	require.NotNil(t, test)
	require.Equal(t, "baseURL", namingField(t, test, "client").Args[0].Name)
	require.Equal(t, "e2eTest", namingField(t, test, "run").Args[0].Name)

	out, err := modGen.With(daggerCall("client", "--base-url=https://example.com", "get-url", "--path=/y")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/y", strings.TrimSpace(out))
}
