package core

// These tests cover modules authored with the C# SDK. They verify generated
// C# bindings and executing C# module functions.
//
// Modules are laid out like the Java suite (see javaModule): the module
// project lives under /work/modules/<name> and the local sdk/csharp checkout
// is mounted at /work/sdk/csharp, referenced as ../../sdk/csharp. This keeps
// tests running against the working tree and keeps the SDK sources outside
// the module project so MSBuild's **/*.cs compile glob only sees the module.

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"dagger.io/dagger"
	"dagger.io/dagger/core"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/core/modules"
)

// Group all tests that are specific to C# only.
type CsharpSuite struct{}

func TestCsharp(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CsharpSuite{})
}

const csharpProgramCs = `// Module entrypoint, called by the dagger engine.
return await Dagger.ModuleRuntime.Entrypoint.RunAsync(args);
`

const csharpGlobalUsings = `// Global using directives for Dagger modules (mirrors the SDK template):
// bring the static Dag client into scope and alias types that clash with System.IO.
global using static Dagger.Client;
global using Directory = Dagger.Directory;
global using File = Dagger.File;
`

const csharpProj = `<Project Sdk="Microsoft.NET.Sdk">
  <Import Project="sdk/Dagger.SDK.props" Condition="Exists('sdk/Dagger.SDK.props')" />
  <PropertyGroup>
    <OutputType>Exe</OutputType>
    <TargetFramework>net10.0</TargetFramework>
    <RootNamespace>DaggerModule</RootNamespace>
    <EnableDaggerAnalyzers>true</EnableDaggerAnalyzers>
  </PropertyGroup>
  <Import Project="sdk/Dagger.SDK.targets" Condition="Exists('sdk/Dagger.SDK.targets')" />
</Project>
`

// csharpProjectName mirrors the SDK runtime's module-name -> C# identifier
// conversion used for the project file (e.g. "my-module" -> "MyModule").
func csharpProjectName(moduleName string) string {
	var b strings.Builder
	for _, part := range strings.FieldsFunc(moduleName, func(r rune) bool {
		return r == '-' || r == '_' || r == ' '
	}) {
		b.WriteString(strings.ToUpper(part[:1]))
		b.WriteString(part[1:])
	}
	return b.String()
}

// csharpBase returns a git-initialized /work container with the local
// sdk/csharp checkout mounted at /work/sdk/csharp.
func csharpBase(t *testctx.T, c *dagger.Client) *core.Container {
	t.Helper()
	sdkSrc, err := filepath.Abs("../../sdk/csharp")
	require.NoError(t, err)
	return goGitBase(t, c).
		WithDirectory("/work/sdk/csharp", core.NewQuery(c).Host().Directory(sdkSrc))
}

// withCsharpModule writes a C# module (config, project scaffolding and
// Main.cs) into dir (relative to /work, e.g. "modules/test").
func withCsharpModule(t *testctx.T, dir, moduleName, mainCs string) core.WithContainerFunc {
	t.Helper()
	return func(ctr *core.Container) *core.Container {
		return ctr.
			With(configFile(dir, &modules.ModuleConfig{
				Name:          moduleName,
				EngineVersion: modules.EngineVersionLatest,
				SDK:           &modules.SDK{Source: "../../sdk/csharp"},
			})).
			With(fileContents(dir+"/Program.cs", csharpProgramCs)).
			With(fileContents(dir+"/GlobalUsings.cs", csharpGlobalUsings)).
			With(fileContents(dir+"/"+csharpProjectName(moduleName)+".csproj", csharpProj)).
			With(fileContents(dir+"/Main.cs", mainCs))
	}
}

// csharpModInit returns a container with a C# module named moduleName at
// /work/modules/<moduleName> (the working directory), initialized from the
// given Main.cs source and wired to the local sdk/csharp checkout.
func csharpModInit(t *testctx.T, c *dagger.Client, moduleName, mainCs string) *core.Container {
	t.Helper()
	dir := "modules/" + moduleName
	return csharpBase(t, c).
		With(withCsharpModule(t, dir, moduleName, mainCs)).
		WithWorkdir("/work/" + dir)
}

// csharpModInitGit is csharpModInit for tests that load the module through
// host.directory().asModule() (e.g. inspectModule): that path resolves the
// module from its own directory only, so a relative ../../sdk/csharp ref
// cannot escape it. Vendor the local sdk/csharp checkout under ./sdk/csharp
// so the module is self-contained without the missing upstream builtin ref.
func csharpModInitGit(t *testctx.T, c *dagger.Client, moduleName, mainCs string) *core.Container {
	t.Helper()
	sdkSrc, err := filepath.Abs("../../sdk/csharp")
	require.NoError(t, err)
	return goGitBase(t, c).
		WithDirectory("sdk/csharp", core.NewQuery(c).Host().Directory(sdkSrc)).
		With(configFile(".", &modules.ModuleConfig{
			Name:          moduleName,
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "./sdk/csharp"},
		})).
		With(fileContents("Program.cs", csharpProgramCs)).
		With(fileContents("GlobalUsings.cs", csharpGlobalUsings)).
		With(fileContents(csharpProjectName(moduleName)+".csproj", csharpProj)).
		With(fileContents("Main.cs", mainCs))
}

// csharpModInitLegacyConfig is csharpModInit but writes the module config as
// legacy dagger.json: workspace inference (used by workspace commands like
// `dagger check` when no workspace config exists) only looks for dagger.json.
func csharpModInitLegacyConfig(t *testctx.T, c *dagger.Client, moduleName, mainCs string) *core.Container {
	t.Helper()
	dir := "modules/" + moduleName
	cfg := fmt.Sprintf(`{
  "name": %q,
  "engineVersion": "latest",
  "sdk": { "source": "../../sdk/csharp" }
}
`, moduleName)
	return csharpBase(t, c).
		With(fileContents(dir+"/dagger.json", cfg)).
		With(fileContents(dir+"/Program.cs", csharpProgramCs)).
		With(fileContents(dir+"/GlobalUsings.cs", csharpGlobalUsings)).
		With(fileContents(dir+"/"+csharpProjectName(moduleName)+".csproj", csharpProj)).
		With(fileContents(dir+"/Main.cs", mainCs)).
		WithWorkdir("/work/" + dir)
}

func (CsharpSuite) TestScaffold(ctx context.Context, t *testctx.T) {
	t.Run("template scaffold", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// A module with only a config file: the SDK runtime copies its
		// template (Program.cs, Main.cs with ContainerEcho/GrepDir, csproj).
		modGen := csharpBase(t, c).
			With(configFile("modules/bare", &modules.ModuleConfig{
				Name:          "bare",
				EngineVersion: modules.EngineVersionLatest,
				SDK:           &modules.SDK{Source: "../../sdk/csharp"},
			})).
			WithWorkdir("/work/modules/bare")

		out, err := modGen.
			With(daggerQueryAt(".", `{containerEcho(stringArg:"hello"){stdout}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"containerEcho":{"stdout":"hello\n"}}`, out)
	})

	t.Run("hyphenated module name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Hyphenated names map to PascalCase C# identifiers (my-module ->
		// MyModule) for the project file and main object class.
		modGen := csharpModInit(t, c, "my-module", `
using Dagger;

[Object]
public class MyModule
{
    [Function]
    public string Hello(string name) => $"Hello, {name}!";
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{hello(name: "World")}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"hello":"Hello, World!"}`, out)
	})

	t.Run("uses expected field casing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Hello(string name) => $"Hello, {name}!";
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{hello(name: "World")}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"hello":"Hello, World!"}`, out)
	})
}

func (CsharpSuite) TestReturnTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string ReturnString() => "hello";

    [Function]
    public int ReturnInt() => 42;

    [Function]
    public bool ReturnBool() => true;

    [Function]
    public float ReturnFloat() => 3.14F;

    [Function]
    public double ReturnDouble() => 2.718281828;

    [Function]
    public decimal ReturnDecimal() => 1.23456789M;

    [Function]
    public Container ReturnContainer() => Dag.Container().From("alpine:latest");

    [Function]
    public Directory ReturnDirectory() => Dag.Directory().WithNewFile("foo.txt", "bar");

    [Function]
    public File ReturnFile() => Dag.Directory().WithNewFile("test.txt", "content").File("test.txt");
}
`)

	t.Run("string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{returnString}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"returnString":"hello"}`, out)
	})

	t.Run("int", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{returnInt}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"returnInt":42}`, out)
	})

	t.Run("bool", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{returnBool}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"returnBool":true}`, out)
	})

	t.Run("float", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{returnFloat}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"returnFloat":3.14}`, out)
	})

	t.Run("double", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{returnDouble}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"returnDouble":2.718281828}`, out)
	})

	t.Run("decimal", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{returnDecimal}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"returnDecimal":1.23456789}`, out)
	})

	t.Run("container", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "return-container")).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `Container@xxh3:[a-f0-9]{16}`, out)
	})

	t.Run("directory", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "return-directory", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "foo.txt")
	})

	t.Run("file", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCallAt(".", "return-file", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "content", out)
	})
}

func (CsharpSuite) TestOptionalValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Greet(string name, string? greeting = null)
    {
        var greetingStr = greeting ?? "Hello";
        return $"{greetingStr}, {name}!";
    }
}
`)

	t.Run("with optional value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{greet(name: "World", greeting: "Hi")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"greet":"Hi, World!"}`, out)
	})

	t.Run("without optional value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{greet(name: "World")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"greet":"Hello, World!"}`, out)
	})
}

func (CsharpSuite) TestDefaultValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Echo(string message = "default") => message;

    [Function]
    public int Add(int a, int b = 10) => a + b;
}
`)

	t.Run("string default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{echo}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echo":"default"}`, out)
	})

	t.Run("int default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{add(a: 5)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"add":15}`, out)
	})

	t.Run("override default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{add(a: 5, b: 3)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"add":8}`, out)
	})
}

func (CsharpSuite) TestConstructor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    public string Name { get; set; } = "default";

    public Test(string name = "default")
    {
        Name = name;
    }

    [Function]
    public string GetName() => Name;
}
`)

	// Constructor arguments are passed as top-level flags with `dagger call`.
	out, err := modGen.
		With(daggerCallAt(".", "--name=configured", "get-name")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "configured", out)
}

func (CsharpSuite) TestEnum(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Enum]
public enum Status
{
    Active,
    Inactive,
    Pending
}

[Object]
public class Test
{
    [Function]
    public string GetStatus(Status status) => status.ToString();
}
`)

	out, err := modGen.
		With(daggerQueryAt(".", `{getStatus(status: ACTIVE)}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"getStatus":"Active"}`, out)
}

// An enum-typed field is read from the object's state, so the value the SDK
// serializes there must name a registered member.
func (CsharpSuite) TestEnumField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Enum]
public enum Status
{
    Active,
    Inactive,
    Pending
}

[Object]
public class Test
{
    public Status Status { get; set; } = Status.Active;

    public Test(Status status = Status.Active)
    {
        Status = status;
    }

    [Function]
    public Test WithStatus(Status status) => new(status);
}
`)

	out, err := modGen.
		With(daggerQueryAt(".", `{status}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"status":"ACTIVE"}`, out)

	out, err = modGen.
		With(daggerQueryAt(".", `{withStatus(status: PENDING) {status}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"withStatus":{"status":"PENDING"}}`, out)
}

func (CsharpSuite) TestIgnore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    private string internalValue = "secret";

    [Function]
    public string PublicFunction() => "visible";

    // Private methods are not exposed
    private string PrivateFunction() => internalValue;
}
`)

	t.Run("public function is exposed", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{publicFunction}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"publicFunction":"visible"}`, out)
	})

	t.Run("private function not exposed", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			With(daggerQueryAt(".", `{privateFunction}`)).
			Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "privateFunction")
	})

	t.Run("public method without [Function] is not exposed", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// Methods require [Function] to become API surface; public
		// properties are auto-exposed as fields (Go/Java parity).
		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    public string PlainProperty { get; set; } = "state";

    [Function]
    public string Visible() => "visible";

    public string Plain() => "not-exposed";
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields{name}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "visible")
		require.Contains(t, out, "plainProperty")
		require.NotContains(t, out, `"plain"`)
	})
}
func (CsharpSuite) TestDefaultPath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public Directory ProcessDirectory(
        [DefaultPath(".")] Directory source,
        string pattern = "*.txt")
    {
        return source;
    }
}
`)

	out, err := modGen.
		With(daggerCallAt(".", "process-directory", "entries")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, out, "Main.cs")
}

func (CsharpSuite) TestDefaultAddress(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public Task<string> Release([DefaultAddress("alpine:3.20")] Container ctr)
    {
        return ctr.WithExec(["cat", "/etc/alpine-release"]).Stdout();
    }
}
`)

	out, err := modGen.
		With(daggerCallAt(".", "release")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, out, "3.20")
}

func (CsharpSuite) TestSignatures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Method1(string arg1, int arg2) => $"{arg1}:{arg2}";

    [Function]
    public async Task<string> AsyncMethod(string input)
    {
        await Task.Delay(10);
        return input.ToUpper();
    }
}
`)

	t.Run("sync method", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{method1(arg1: "test", arg2: 123)}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"method1":"test:123"}`, out)
	})

	t.Run("async method", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{asyncMethod(input: "hello")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"asyncMethod":"HELLO"}`, out)
	})
}

func (CsharpSuite) TestDocs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    /// <param name="name">The name to greet</param>
    [Function]
    public string Hello(string name) => $"Hello, {name}!";
}
`)

	out, err := modGen.
		With(daggerQueryAt(".", `{hello(name: "Docs")}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"hello":"Hello, Docs!"}`, out)
}

func (CsharpSuite) TestNameCasing(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string MyFieldValue { get; set; } = "field";

    public Test(string myFieldValue = "field")
    {
        MyFieldValue = myFieldValue;
    }

    [Function]
    public string MyMethodName(string myParamName) => $"{myParamName}:{MyFieldValue}";
}
`)

	// C# uses PascalCase; the CLI exposes kebab-case flags/functions derived
	// from the camelCase API names.
	out, err := modGen.
		With(daggerCallAt(".", "--my-field-value=test", "my-method-name", "--my-param-name=value")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "value:test", out)
}

func (CsharpSuite) TestReturnSelf(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Value { get; set; } = "";

    public Test(string value = "")
    {
        Value = value;
    }

    [Function]
    public Test WithValue(string value)
    {
        Value = value;
        return this;
    }

    [Function]
    public string GetValue() => Value;
}
`)

	out, err := modGen.
		With(daggerQueryAt(".", `{withValue(value: "fluent"){getValue}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"withValue":{"getValue":"fluent"}}`, out)
}

func (CsharpSuite) TestListTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string[] ReturnStringList()
    {
        return new[] { "one", "two", "three" };
    }

    [Function]
    public int[] ReturnIntList()
    {
        return new[] { 1, 2, 3 };
    }

    [Function]
    public string JoinStrings(string[] items)
    {
        return string.Join(",", items);
    }
}
`)

	t.Run("return string list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{returnStringList}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"returnStringList":["one","two","three"]}`, out)
	})

	t.Run("return int list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{returnIntList}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"returnIntList":[1,2,3]}`, out)
	})

	t.Run("accept string list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQueryAt(".", `{joinStrings(items: ["a","b","c"])}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"joinStrings":"a,b,c"}`, out)
	})
}

func (CsharpSuite) TestCustomObjects(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class CustomObject(string name = "")
{
    [Function]
    public string Name { get; set; } = name;

    [Function]
    public string GetName() => Name;
}

[Object]
public class Test
{
    [Function]
    public CustomObject CreateCustom(string name) => new CustomObject(name);
}
`)

	out, err := modGen.
		With(daggerQueryAt(".", `{createCustom(name: "custom"){getName}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"createCustom":{"getName":"custom"}}`, out)
}

func (CsharpSuite) TestErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string ThrowError()
    {
        throw new System.Exception("intentional error");
    }
}
`)

	_, err := modGen.
		With(daggerQueryAt(".", `{throwError}`)).
		Stdout(ctx)

	require.Error(t, err)
	requireErrOut(t, err, "intentional error")
}

func (CsharpSuite) TestFloatingPointTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public float AddFloat(float a, float b) => a + b;

    [Function]
    public double AddDouble(double a, double b) => a + b;

    [Function]
    public decimal AddDecimal(decimal a, decimal b) => a + b;

    [Function]
    public float MultiplyFloat(float a, float b = 2.0F) => a * b;

    [Function]
    public double MultiplyDouble(double a, double b = 3.0) => a * b;
}
`)

	t.Run("add float", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{addFloat(a:1.5,b:2.5)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"addFloat":4.0}`, out)
	})

	t.Run("add double", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{addDouble(a:10.5,b:20.75)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"addDouble":31.25}`, out)
	})

	t.Run("add decimal", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{addDecimal(a:100.123,b:200.456)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"addDecimal":300.579}`, out)
	})

	t.Run("multiply float with default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{multiplyFloat(a:5.5)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"multiplyFloat":11.0}`, out)
	})

	t.Run("multiply float with explicit value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{multiplyFloat(a:3.0,b:4.0)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"multiplyFloat":12.0}`, out)
	})

	t.Run("multiply double with default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{multiplyDouble(a:7.5)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"multiplyDouble":22.5}`, out)
	})

	t.Run("multiply double with explicit value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQueryAt(".", `{multiplyDouble(a:2.5,b:4.0)}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"multiplyDouble":10.0}`, out)
	})
}

func (CsharpSuite) TestWithOtherModuleTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := csharpBase(t, c).
		With(withCsharpModule(t, "modules/dep", "dep", `
using Dagger;

[Object]
public class Dep
{
    [Function]
    public Obj Fn() => new Obj("foo");
}

[Object]
public class Obj
{
    [Function]
    public string Foo { get; set; }

    public Obj(string foo = "")
    {
        Foo = foo;
    }
}
`)).
		With(configFile("modules/test", &modules.ModuleConfig{
			Name:          "test",
			EngineVersion: modules.EngineVersionLatest,
			SDK:           &modules.SDK{Source: "../../sdk/csharp"},
			Dependencies: []*modules.ModuleConfigDependency{
				{Name: "dep", Source: "../dep"},
			},
		})).
		With(fileContents("modules/test/Program.cs", csharpProgramCs)).
		With(fileContents("modules/test/GlobalUsings.cs", csharpGlobalUsings)).
		With(fileContents("modules/test/Test.csproj", csharpProj)).
		WithWorkdir("/work/modules/test")

	t.Run("return as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public DepObj Fn() => null!;
}
`).
				With(daggerFunctions("-m", ".")).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q function %q cannot return external type from dependency module %q`,
				"Test", "fn", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("Main.cs", `
using Dagger;
using System.Collections.Generic;

[Object]
public class Test
{
    [Function]
    public List<DepObj> Fn() => null!;
}
`).
				With(daggerFunctions("-m", ".")).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q function %q cannot return external type from dependency module %q`,
				"Test", "fn", "dep",
			))
		})
	})

	t.Run("arg as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Fn(DepObj obj) => "";
}
`).
				With(daggerFunctions("-m", ".")).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q function %q arg %q cannot reference external type from dependency module %q`,
				"Test", "fn", "obj", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("Main.cs", `
using Dagger;
using System.Collections.Generic;

[Object]
public class Test
{
    [Function]
    public string Fn(List<DepObj> objs) => "";
}
`).
				With(daggerFunctions("-m", ".")).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q function %q arg %q cannot reference external type from dependency module %q`,
				"Test", "fn", "objs", "dep",
			))
		})
	})

	t.Run("field as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public DepObj? MyObj { get; set; }
}
`).
				With(daggerFunctions("-m", ".")).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q field %q cannot reference external type from dependency module %q`,
				"Test", "myObj", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("Main.cs", `
using Dagger;
using System.Collections.Generic;

[Object]
public class Test
{
    [Function]
    public List<DepObj> MyObjs { get; set; } = new List<DepObj>();
}
`).
				With(daggerFunctions("-m", ".")).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q field %q cannot reference external type from dependency module %q`,
				"Test", "myObjs", "dep",
			))
		})
	})
}

func (CsharpSuite) TestInterface(ctx context.Context, t *testctx.T) {
	t.Run("doc", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitGit(t, c, "test", `
using Dagger;
using System.Threading.Tasks;

[Object]
public class Test
{
    [Function]
    public async Task<string> DuckQuack(IDuck duck)
    {
        return await duck.Quack();
    }

    [Function]
    public async Task<string> DuckSuperQuack(IDuck duck)
    {
        return await duck.SuperQuack();
    }
}
`).
			WithNewFile("IDuck.cs", `
using Dagger;
using System.Threading.Tasks;

/// <summary>
/// A simple Duck interface
/// </summary>
[Interface(Name = "Duck")]
public interface IDuck
{
    /// <summary>
    /// A small quack sound
    /// </summary>
    [Function]
    Task<string> Quack();

    /// <summary>
    /// A super quack sound
    /// </summary>
    [Function]
    Task<string> SuperQuack();
}
`)

		schema := inspectModule(ctx, t, modGen)

		require.Equal(t, "A simple Duck interface", schema.Get("interfaces.#.asInterface|#(name=TestDuck).description").String())
		require.Equal(t, "A small quack sound", schema.Get("interfaces.#.asInterface|#(name=TestDuck).functions.#(name=quack).description").String())
		require.Equal(t, "A super quack sound", schema.Get("interfaces.#.asInterface|#(name=TestDuck).functions.#(name=superQuack).description").String())
	})
}

func (CsharpSuite) TestModuleSubPathLoading(ctx context.Context, t *testctx.T) {
	t.Run("load from subpath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpBase(t, c).
			With(configFile("modules/test", &modules.ModuleConfig{
				Name:          "test",
				EngineVersion: modules.EngineVersionLatest,
				SDK:           &modules.SDK{Source: "../../sdk/csharp"},
				Source:        "mymodule",
			})).
			With(fileContents("modules/test/mymodule/Program.cs", csharpProgramCs)).
			With(fileContents("modules/test/mymodule/GlobalUsings.cs", csharpGlobalUsings)).
			With(fileContents("modules/test/mymodule/Test.csproj", csharpProj)).
			With(fileContents("modules/test/mymodule/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Hello() => "hello from subpath";
}
`)).
			WithWorkdir("/work/modules/test")

		out, err := modGen.
			With(daggerQueryAt(".", `{hello}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hello":"hello from subpath"}`, out)
	})
}

func (CsharpSuite) TestVariadicParameters(ctx context.Context, t *testctx.T) {
	t.Run("params string array", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Join(params string[] messages)
    {
        return string.Join(", ", messages);
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{join(messages:["hello","world","from","csharp"])}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"join":"hello, world, from, csharp"}`, out)
	})

	t.Run("params int array", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public int Sum(params int[] numbers)
    {
        return numbers.Sum();
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{sum(numbers:[1,2,3,4,5])}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"sum":15}`, out)
	})

	t.Run("empty params array", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public int Count(params string[] items)
    {
        return items.Length;
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{count(items:[])}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"count":0}`, out)
	})
}

func (CsharpSuite) TestNameAttribute(ctx context.Context, t *testctx.T) {
	t.Run("function parameter name override", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Echo([Name("msg")] string message)
    {
        return message;
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{echo(msg:"hello")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echo":"hello"}`, out)
	})

	t.Run("constructor parameter name override", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    public string Source { get; set; } = "default";

    public Test([Name("src")] string source = "default")
    {
        Source = source;
    }

    [Function]
    public string GetSource()
    {
        return Source;
    }
}
`)

		out, err := modGen.
			With(daggerCallAt(".", "--src=custom", "get-source")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "custom", out)
	})

	t.Run("field name override", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function(Name = "val")]
    public string Value { get; set; } = "field value";

    [Function]
    public string GetValue() => Value;
}
`)

		// Function path first (independent of field-state resolution).
		out, err := modGen.
			With(daggerCallAt(".", "get-value")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "field value", out)

		// Field path: served from state under the registered name.
		out, err = modGen.
			With(daggerQueryAt(".", `{val}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"val":"field value"}`, out)
	})

	t.Run("function name override", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function(Name = "greet")]
    public string SayHello(string name)
    {
        return $"Hello, {name}!";
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{greet(name:"World")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"greet":"Hello, World!"}`, out)
	})
}

func (CsharpSuite) TestReservedKeywords(ctx context.Context, t *testctx.T) {
	t.Run("parameter with reserved keyword names", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Echo(string @class, string @event, string @namespace)
    {
        return $"{@class},{@event},{@namespace}";
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{echo(class:"MyClass",event:"Click",namespace:"MyApp")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echo":"MyClass,Click,MyApp"}`, out)
	})

	t.Run("constructor with reserved keywords", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    public string Type { get; set; } = "default";

    public Test(string @type = "default")
    {
        Type = @type;
    }

    [Function]
    public new string GetType()
    {
        return Type;
    }
}
`)

		out, err := modGen.
			With(daggerCallAt(".", "--type=custom", "get-type")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "custom", out)
	})
}

func (CsharpSuite) TestEnumCollections(ctx context.Context, t *testctx.T) {
	t.Run("return List of enums", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using System.Collections.Generic;
using Dagger;

public enum Status
{
    Pending,
    Active,
    Completed
}

[Object]
public class Test
{
    [Function]
    public List<Status> GetStatuses()
    {
        return new List<Status> { Status.Pending, Status.Active, Status.Completed };
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{getStatuses}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getStatuses":["PENDING","ACTIVE","COMPLETED"]}`, out)
	})

	t.Run("return array of enums", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

public enum Priority
{
    Low,
    Medium,
    High
}

[Object]
public class Test
{
    [Function]
    public Priority[] GetPriorities()
    {
        return new[] { Priority.Low, Priority.Medium, Priority.High };
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{getPriorities}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"getPriorities":["LOW","MEDIUM","HIGH"]}`, out)
	})

	t.Run("accept List of enums", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using System.Collections.Generic;
using System.Linq;
using Dagger;

public enum Color
{
    Red,
    Green,
    Blue
}

[Object]
public class Test
{
    [Function]
    public string JoinColors(List<Color> colors)
    {
        return string.Join(",", colors.Select(c => c.ToString()));
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{joinColors(colors:[RED,BLUE])}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"joinColors":"Red,Blue"}`, out)
	})

	t.Run("accept array of enums", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using System.Linq;
using Dagger;

public enum Level
{
    Debug,
    Info,
    Warning,
    Error
}

[Object]
public class Test
{
    [Function]
    public int CountLevels(Level[] levels)
    {
        return levels.Length;
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{countLevels(levels:[DEBUG,INFO,WARNING,ERROR])}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"countLevels":4}`, out)
	})
}

func (CsharpSuite) TestCheckFunctions(ctx context.Context, t *testctx.T) {
	// Checks are ordinary module functions carrying the check marker
	// ([Function] + [Check], like Java's @Function @Check); they are
	// discovered and run by the top-level `dagger check` command, whose
	// OK/ERROR verdicts render on progress output (hence --progress=report
	// with CombinedOutput).
	t.Run("void check passes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitLegacyConfig(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    [Check]
    public void PassingCheck()
    {
        // Check passes by not throwing
    }
}
`)

		out, err := modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `passing-check.*OK`, out)
	})

	t.Run("void check fails", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitLegacyConfig(t, c, "test", `
using System;
using Dagger;

[Object]
public class Test
{
    [Function]
    [Check]
    public void FailingCheck()
    {
        throw new Exception("validation failed");
    }
}
`)

		out, err := modGen.
			With(daggerExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `failing-check.*ERROR`, out)

		// The exception message surfaces on progress output when the check
		// is invoked as a plain function.
		out, err = modGen.
			With(daggerExecFail("--progress=report", "call", "failing-check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "validation failed")
	})

	t.Run("Task check passes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitLegacyConfig(t, c, "test", `
using System.Threading.Tasks;
using Dagger;

[Object]
public class Test
{
    [Function]
    [Check]
    public async Task AsyncPassingCheck()
    {
        await Task.Delay(10);
        // Check passes by not throwing
    }
}
`)

		out, err := modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `async-passing-check.*OK`, out)
	})

	// Like every SDK, a check returns void or Task and fails by throwing; a
	// container runs as a check by awaiting Sync(), which throws on a non-zero
	// exit.
	t.Run("container check passes on exit code 0", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitLegacyConfig(t, c, "test", `
using System.Threading.Tasks;
using Dagger;

[Object]
public class Test
{
    [Function]
    [Check]
    public async Task ValidateWithContainer()
    {
        await Dag.Container()
            .From("`+alpineImage+`")
            .WithExec(new[] { "sh", "-c", "exit 0" })
            .Sync();
    }
}
`)

		out, err := modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `validate-with-container.*OK`, out)
	})

	t.Run("container check fails on non-zero exit code", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitLegacyConfig(t, c, "test", `
using System.Threading.Tasks;
using Dagger;

[Object]
public class Test
{
    [Function]
    [Check]
    public async Task FailingContainerCheck()
    {
        await Dag.Container()
            .From("`+alpineImage+`")
            .WithExec(new[] { "sh", "-c", "exit 1" })
            .Sync();
    }
}
`)

		out, err := modGen.
			With(daggerExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `failing-container-check.*ERROR`, out)
	})

	// The engine rejects checks that return anything but Void; DAGGER017
	// reports it at compile time.
	t.Run("Container-returning check is rejected", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitLegacyConfig(t, c, "test", `
using System.Threading.Tasks;
using Dagger;

[Object]
public class Test
{
    [Function]
    [Check]
    public Container ContainerCheck()
    {
        return Dag.Container()
            .From("`+alpineImage+`")
            .WithExec(new[] { "sh", "-c", "exit 0" });
    }

    [Function]
    [Check]
    public async Task<Container> AsyncContainerCheck()
    {
        await Task.Delay(10);
        return Dag.Container()
            .From("`+alpineImage+`")
            .WithExec(new[] { "sh", "-c", "exit 0" });
    }
}
`)

		out, err := modGen.
			With(daggerExecFail("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "DAGGER017")
		require.Contains(t, out, "'ContainerCheck' marked with [Check] returns 'Dagger.Container'")
		require.Contains(t, out, "'AsyncContainerCheck' marked with [Check] returns 'System.Threading.Tasks.Task<Dagger.Container>'")
		require.Contains(t, out, "Check functions must return void or Task")
	})

	t.Run("check with optional parameters", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitLegacyConfig(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    [Check]
    public void ConfigurableCheck(string level = "info")
    {
        if (level != "info")
        {
            throw new System.Exception($"unexpected level: {level}");
        }
    }
}
`)

		out, err := modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `configurable-check.*OK`, out)
	})

	t.Run("check with DefaultPath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInitLegacyConfig(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    [Check]
    public void ValidateFiles([DefaultPath(".")] Directory source)
    {
        // Validate source directory exists
        if (source == null)
        {
            throw new System.Exception("source is null");
        }
    }
}
`)

		out, err := modGen.
			With(daggerExec("--progress=report", "check")).
			CombinedOutput(ctx)
		require.NoError(t, err)
		require.Regexp(t, `validate-files.*OK`, out)
	})
}
func (CsharpSuite) TestFieldDocumentation(ctx context.Context, t *testctx.T) {
	t.Run("field with XML documentation", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    /// <summary>
    /// The name of the test
    /// </summary>
    public string Name { get; set; } = "default";

    [Function]
    public string GetName() => Name;
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields{name description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "The name of the test")
	})

	t.Run("private field without documentation", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    /// <summary>
    /// This is public and should appear
    /// </summary>
    public string PublicField { get; set; } = "public";

    private string privateField = "private";

    [Function]
    public string GetPublic() => PublicField;
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields{name description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "This is public and should appear")
		require.NotContains(t, out, "privateField")
	})
}

func (CsharpSuite) TestDeprecatedAttribute(ctx context.Context, t *testctx.T) {
	t.Run("deprecated function", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function(Deprecated = "Use NewMethod instead")]
    public string OldMethod()
    {
        return "old";
    }

    [Function]
    public string NewMethod()
    {
        return "new";
    }
}
`)

		// Deprecated functions are hidden from introspection by default.
		out, err := modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields{name}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "newMethod")
		require.NotContains(t, out, "oldMethod")

		// includeDeprecated surfaces the function with its reason.
		out, err = modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields(includeDeprecated:true){name isDeprecated deprecationReason}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "oldMethod")
		require.Contains(t, out, "Use NewMethod instead")

		// A deprecated function can still be called.
		out, err = modGen.
			With(daggerQueryAt(".", `{oldMethod}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"oldMethod":"old"}`, out)
	})

	t.Run("deprecated parameter", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// [Deprecated] is the SDK's parameter deprecation attribute
		// (System.Obsolete is not valid on parameters). The parameter is
		// optional: deprecating a required arg makes no sense for callers.
		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Echo([Deprecated("Use message instead")] string msg = "hello")
    {
        return msg;
    }
}
`)

		// Deprecated args are hidden from introspection by default.
		out, err := modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields{name args{name}}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "echo")
		require.NotContains(t, out, "msg")

		// includeDeprecated surfaces the arg with its reason.
		out, err = modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields{name args(includeDeprecated:true){name isDeprecated deprecationReason}}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "msg")
		require.Contains(t, out, "Use message instead")

		// A deprecated arg can still be passed.
		out, err = modGen.
			With(daggerQueryAt(".", `{echo(msg:"hi")}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"echo":"hi"}`, out)
	})
}
func (CsharpSuite) TestIgnoreAttribute(ctx context.Context, t *testctx.T) {
	// [Ignore] carries directory ignore patterns for Directory parameters
	// (like +ignore in the Go SDK); it is parameter-only and not a
	// visibility modifier. Hiding a method = omitting [Function].
	c := connect(ctx, t)

	modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public async Task<string> FilesNoIgnore([DefaultPath(".")] Directory dir)
    {
        return string.Join(" ", await dir.Entries());
    }

    [Function]
    public async Task<string> FilesIgnore(
        [DefaultPath(".")] [Ignore("*.csproj")] Directory dir)
    {
        return string.Join(" ", await dir.Entries());
    }

    [Function]
    public async Task<string> FilesNegIgnore(
        [DefaultPath(".")] [Ignore("**", "!*.cs")] Directory dir)
    {
        return string.Join(" ", await dir.Entries());
    }
}
`)

	t.Run("without ignore", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCallAt(".", "files-no-ignore")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Main.cs")
		require.Contains(t, out, "Test.csproj")
	})

	t.Run("with ignore", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCallAt(".", "files-ignore")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Main.cs")
		require.NotContains(t, out, "Test.csproj")
	})

	t.Run("with negated ignore", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCallAt(".", "files-neg-ignore")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Main.cs")
		require.NotContains(t, out, "Test.csproj")
		require.NotContains(t, out, "dagger-module.toml")
	})
}
func (CsharpSuite) TestAlternativeConstructors(ctx context.Context, t *testctx.T) {
	t.Run("static factory method", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// [Constructor] designates a static factory as THE module
		// constructor (the engine has a single constructor slot). State
		// must live in a public property whose camelCase name matches the
		// factory parameter: constructor results serialize public
		// properties, and later calls reconstruct by re-running the
		// factory from parent state.
		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    public string Value { get; set; } = "";

    [Constructor]
    public static Test Create(string value)
    {
        return new Test { Value = value };
    }

    [Function]
    public string GetValue() => Value;
}
`)

		out, err := modGen.
			With(daggerCallAt(".", "--value=factory", "get-value")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "factory", out)

		// Constructor args are also exposed through the with(...) field.
		out, err = modGen.
			With(daggerQueryAt(".", `{with(value:"factory"){getValue}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"with":{"getValue":"factory"}}`, out)
	})

	t.Run("multiple factory methods rejected", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// The engine supports exactly one constructor per module; multiple
		// [Constructor] factories are a registration error.
		modGen := csharpModInit(t, c, "test", `
using Dagger;

[Object]
public class Test
{
    private readonly string source;

    private Test(string source)
    {
        this.source = source;
    }

    [Constructor]
    public static Test FromString(string value)
    {
        return new Test($"string:{value}");
    }

    [Constructor]
    public static Test FromInt(int number)
    {
        return new Test($"int:{number}");
    }

    [Function]
    public string GetSource() => source;
}
`)

		_, err := modGen.With(daggerFunctions("-m", ".")).Sync(ctx)
		require.Error(t, err)
		// The Roslyn analyzer rejects this at module compile time.
		requireErrOut(t, err, "DAGGER021")
	})
}
func (CsharpSuite) TestInheritedDocs(ctx context.Context, t *testctx.T) {
	t.Run("inherit from base class", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

public abstract class BaseClass
{
    /// <summary>
    /// Gets the greeting message
    /// </summary>
    public abstract string Greet();
}

[Object]
public class Test : BaseClass
{
    /// <inheritdoc/>
    [Function]
    public override string Greet()
    {
        return "Hello from Test";
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields{name description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Gets the greeting message")
	})

	t.Run("inherit from interface", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := csharpModInit(t, c, "test", `
using Dagger;

public interface IGreeter
{
    /// <summary>
    /// Returns a greeting message
    /// </summary>
    string SayHello();
}

[Object]
public class Test : IGreeter
{
    /// <inheritdoc/>
    [Function]
    public string SayHello()
    {
        return "Hello!";
    }
}
`)

		out, err := modGen.
			With(daggerQueryAt(".", `{__type(name:"Test"){fields{name description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Returns a greeting message")
	})
}

// TestRecord verifies that `record` (record class) types are supported as
// Dagger module objects: a record's public primary constructor maps to
// constructor args, and its public init properties are auto-exposed as fields,
// matching plain-class behaviour (issue #10).
func (CsharpSuite) TestRecord(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	t.Run("returned record exposes positional properties as fields", func(ctx context.Context, t *testctx.T) {
		modGen := csharpModInit(t, c, "test", `
using Dagger;

/// <summary>Test module.</summary>
[Object]
public class Test
{
    /// <summary>Build a point.</summary>
    [Function]
    public Point MakePoint(int x, int y) => new Point(x, y);
}

/// <summary>A 2D point.</summary>
[Object]
public record Point(int X, int Y);
`)

		out, err := modGen.
			With(daggerCallAt(".", "make-point", "--x=3", "--y=4", "x")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "3", out)

		out, err = modGen.
			With(daggerCallAt(".", "make-point", "--x=3", "--y=4", "y")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "4", out)
	})

	t.Run("record as the main module object", func(ctx context.Context, t *testctx.T) {
		modGen := csharpModInit(t, c, "test", `
using Dagger;

/// <summary>A record module with a constructor arg and a function.</summary>
[Object]
public record Test(string Greeting = "hello")
{
    /// <summary>Greet a name.</summary>
    [Function]
    public string Greet(string name) => $"{Greeting}, {name}";
}
`)

		out, err := modGen.
			With(daggerCallAt(".", "--greeting=hi", "greet", "--name=world")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hi, world", out)
	})
}
