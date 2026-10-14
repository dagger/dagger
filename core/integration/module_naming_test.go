package core

// Module type, field, function and argument names are normalized by the
// naming rules of the engine version a module declares: engine/naming from
// core.IdentifierNamingVersion (an API view, so every v1.0.0 prerelease),
// strcase before. Names the new rules changed keep their legacy spellings as
// hidden aliases. See hack/designs/identifier-casing.md.

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

// namingGoCoreTypesSource extends namingGoSource with functions that
// reference core types whose names start with an acronym, and an enum whose
// members the new rules name differently than strcase did.
const namingGoCoreTypesSource = namingGoSource + `
func (m *Test) Value() *dagger.JSONValue {
	return dag.JSON().NewString("hi")
}

func (m *Test) Echo(msg *dagger.LLMMessage) *dagger.LLMMessage {
	return msg
}

func (m *Test) Usage(usage *dagger.LLMTokenUsage) *dagger.LLMTokenUsage {
	return usage
}

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

func namingTypeRefName(ref *introspection.TypeRef) string {
	for ref.OfType != nil {
		ref = ref.OfType
	}
	return ref.Name
}

func (ModuleNamingSuite) TestGoAcronyms(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	// "latest" pins the module to this engine's version, v1.0.0 or later.
	modGen := legacySDKModule(t, c, "go", namingGoCoreTypesSource, "latest")
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

	// References to core types starting with an acronym resolve to the
	// core types, not to namespaced copies.
	require.Equal(t, "JSONValue", namingTypeRefName(namingField(t, test, "value").TypeRef))
	require.Equal(t, "LLMMessage", namingTypeRefName(namingField(t, test, "echo").TypeRef))
	require.Equal(t, "LLMTokenUsage", namingTypeRefName(namingField(t, test, "usage").TypeRef))
	for _, typ := range schema.Types {
		require.False(t, strings.HasPrefix(typ.Name, "TestLlm") || strings.HasPrefix(typ.Name, "TestLLM") ||
			strings.HasPrefix(typ.Name, "TestJson") || strings.HasPrefix(typ.Name, "TestJSON"),
			"core type namespaced into the module: %s", typ.Name)
	}

	out, err := modGen.With(daggerQuery(`{client(baseURL:"https://example.com"){getURL(path:"/x")} value{asString}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"client":{"getURL":"https://example.com/x"},"value":{"asString":"hi"}}`, out)

	out, err = modGen.With(daggerCall("client", "--base-url=https://example.com", "get-url", "--path=/y")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "https://example.com/y", strings.TrimSpace(out))

	// The legacy spellings stay accepted as hidden aliases, the type's too.
	out, err = modGen.With(daggerQuery(`{client(baseUrl:"https://example.com"){getUrl(path:"/x") ... on TestHttpclient { baseUrl }}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"client":{"getUrl":"https://example.com/x","baseUrl":"https://example.com"}}`, out)

	// Enum members too: the schema has the new names only, but the legacy
	// literals are accepted, as inputs only.
	arch := schema.Types.Get("TestArch")
	require.NotNil(t, arch)
	var archValues []string
	for _, value := range arch.EnumValues {
		archValues = append(archValues, value.Name)
	}
	require.ElementsMatch(t, []string{"ARM64", "IPV6", "GITHUB", "E2E_TEST"}, archValues)
	out, err = modGen.With(daggerQuery(`{
		new: fromArch(arch: ARM64)
		old: fromArch(arch: ARM_64)
		ipv6: fromArch(arch: I_PV_6)
		list: fromArchs(archs: [GIT_HUB, E_2_E_TEST, GITHUB])
		defaultArch
	}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"new":"arm64","old":"arm64","ipv6":"ipv6","list":"github,e2e,github,","defaultArch":"ARM64"}`, out)

	out, err = modGen.With(daggerCall("from-arch", "--arch=ARM_64")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "arm64", strings.TrimSpace(out))
	out, err = modGen.With(daggerCall("from-archs", "--archs=GIT_HUB,E_2_E_TEST")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "github,e2e,", strings.TrimSpace(out))
}

func (ModuleNamingSuite) TestGoAcronymsV1Prerelease(ctx context.Context, t *testctx.T) {
	// The naming gate is an API view, so a module that declares any v1.0.0
	// prerelease gets the new names, and keeps its legacy ones as aliases.
	for _, version := range []string{"v1.0.0-beta.16", "v1.0.0-0"} {
		t.Run(version, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := legacySDKModule(t, c, "go", namingGoSource, version)
			schema := currentSchema(ctx, t, modGen)

			client := schema.Types.Get("TestHTTPClient")
			require.NotNil(t, client)
			require.Nil(t, schema.Types.Get("TestHttpclient"), "aliases are hidden")
			require.ElementsMatch(t, []string{"baseURL", "getURL", "id"}, namingFieldNames(client))

			out, err := modGen.With(daggerQuery(`{client(baseUrl:"https://example.com"){getUrl(path:"/x")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"client":{"getUrl":"https://example.com/x"}}`, out)
		})
	}
}

func (ModuleNamingSuite) TestGoAcronymsLegacy(ctx context.Context, t *testctx.T) {
	// Modules older than core.IdentifierNamingVersion keep the strcase names
	// they always had.
	for _, version := range []string{"v0.21.9"} {
		t.Run(version, func(ctx context.Context, t *testctx.T) {
			c := connect(ctx, t)

			modGen := legacySDKModule(t, c, "go", namingGoSource, version)
			schema := currentSchema(ctx, t, modGen)

			client := schema.Types.Get("TestHttpclient")
			require.NotNil(t, client, "legacy modules keep their strcase names")
			require.Nil(t, schema.Types.Get("TestHTTPClient"))
			require.ElementsMatch(t, []string{"baseUrl", "getUrl", "id"}, namingFieldNames(client))

			test := schema.Types.Get("Test")
			require.NotNil(t, test)
			require.Equal(t, "baseUrl", namingField(t, test, "client").Args[0].Name)
			require.Equal(t, "e2ETest", namingField(t, test, "run").Args[0].Name)

			out, err := modGen.With(daggerQuery(`{client(baseUrl:"https://example.com"){getUrl(path:"/x")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"client":{"getUrl":"https://example.com/x"}}`, out)
		})
	}
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

// TestTypeScriptInterfaceAcronyms checks the calls the TypeScript runtime
// builds through a module's own interface. From v1.0.0 the engine names the
// interface and its members with its naming rules (PondHTTPFetcher,
// getURL(comURL:)), and the runtime asks it for those names.
func (ModuleNamingSuite) TestTypeScriptInterfaceAcronyms(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		With(withLegacyTypescriptModule(t, c, "pond", "pond", "latest")).
		With(sdkSourceAt("pond", "typescript", `import { func, object } from "@dagger.io/dagger"

export interface HttpFetcher {
  getUrl: (comUrl: string) => Promise<string>
  withHttpPrefix: (httpPrefix: string) => Promise<HttpFetcher>
}

@object()
export class Pond {
  @func()
  async fetch(fetcher: HttpFetcher, comUrl: string): Promise<string> {
    const prefixed = await fetcher.withHttpPrefix("got ")
    return await prefixed.getUrl(comUrl)
  }
}
`)).
		With(withLegacyTypescriptModule(t, c, "impl", "impl", "latest")).
		With(sdkSourceAt("impl", "typescript", `import { func, object } from "@dagger.io/dagger"

@object()
export class Impl {
  @func()
  prefix = ""

  @func()
  getUrl(comUrl: string): string {
    return this.prefix + comUrl
  }

  @func()
  withHttpPrefix(httpPrefix: string): Impl {
    this.prefix = httpPrefix
    return this
  }
}
`)).
		With(withLegacyGoModule(t, c, ".", "test", "latest")).
		With(fileContents("dagger.json", `{
  "name": "test",
  "sdk": "go",
  "source": ".",
  "engineVersion": "latest",
  "dependencies": [
    {"name": "impl", "source": "impl"},
    {"name": "pond", "source": "pond"}
  ]
}`)).
		With(sdkSource("go", `package main

import "context"

type Test struct{}

func (m *Test) Fetch(ctx context.Context) (string, error) {
	return dag.Pond().Fetch(ctx, dag.Impl().AsPondHTTPFetcher(), "https://example.com")
}
`))

	out, err := modGen.With(daggerQueryAt("pond", `{__type(name: "PondHTTPFetcher") { fields { name args { name } } }}`)).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, `"getURL"`)
	require.Contains(t, out, `"comURL"`)
	require.Contains(t, out, `"withHTTPPrefix"`)

	out, err = modGen.With(daggerCall("fetch")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "got https://example.com", strings.TrimSpace(out))
}
