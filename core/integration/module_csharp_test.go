package core

import (
	"context"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// Group all tests that are specific to C# only.
type CsharpSuite struct{}

func TestCsharp(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(CsharpSuite{})
}

func (CsharpSuite) TestInit(ctx context.Context, t *testctx.T) {
	t.Run("from upstream", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("uses expected field casing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Hello(string name) => $"Hello, {name}!";
}
`)

		out, err := modGen.
			With(daggerQuery(`{test{hello(name: "World")}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"hello":"Hello, World!"}}`, out)
	})
}

func (CsharpSuite) TestReturnTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
		out, err := modGen.With(daggerQuery(`{test{returnString}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"returnString":"hello"}}`, out)
	})

	t.Run("int", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{returnInt}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"returnInt":42}}`, out)
	})

	t.Run("bool", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{returnBool}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"returnBool":true}}`, out)
	})

	t.Run("float", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{returnFloat}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"returnFloat":3.14}}`, out)
	})

	t.Run("double", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{returnDouble}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"returnDouble":2.718281828}}`, out)
	})

	t.Run("decimal", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{returnDecimal}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"returnDecimal":1.23456789}}`, out)
	})

	t.Run("container", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("return-container")).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `Container@xxh3:[a-f0-9]{16}`, out)
	})

	t.Run("directory", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("return-directory", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "foo.txt")
	})

	t.Run("file", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("return-file", "contents")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "content", out)
	})
}

func (CsharpSuite) TestOptionalValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{greet(name: "World", greeting: "Hi")}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"greet":"Hi, World!"}}`, out)
	})

	t.Run("without optional value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{test{greet(name: "World")}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"greet":"Hello, World!"}}`, out)
	})
}

func (CsharpSuite) TestDefaultValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
		out, err := modGen.With(daggerQuery(`{test{echo}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"echo":"default"}}`, out)
	})

	t.Run("int default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{add(a: 5)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"add":15}}`, out)
	})

	t.Run("override default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{add(a: 5, b: 3)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"add":8}}`, out)
	})
}

func (CsharpSuite) TestConstructor(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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

	out, err := modGen.
		With(daggerQuery(`{test(name: "configured"){getName}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"getName":"configured"}}`, out)
}

func (CsharpSuite) TestEnum(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
		With(daggerQuery(`{test{getStatus(status: ACTIVE)}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"getStatus":"Active"}}`, out)
}

func (CsharpSuite) TestIgnore(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{publicFunction}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"publicFunction":"visible"}}`, out)
	})

	t.Run("private function not exposed", func(ctx context.Context, t *testctx.T) {
		_, err := modGen.
			With(daggerQuery(`{test{privateFunction}}`)).
			Stdout(ctx)
		require.Error(t, err)
		requireErrOut(t, err, "privateFunction")
	})
}

func (CsharpSuite) TestDefaultPath(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
		With(daggerCall("process-directory", "entries")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Contains(t, out, "Main.cs")
}

func (CsharpSuite) TestSignatures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{method1(arg1: "test", arg2: 123)}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"method1":"test:123"}}`, out)
	})

	t.Run("async method", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{test{asyncMethod(input: "hello")}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"asyncMethod":"HELLO"}}`, out)
	})
}

func (CsharpSuite) TestDocs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
using Dagger;

/// <summary>
/// Test module for documentation
/// </summary>
[Object]
public class Test
{
    /// <summary>
    /// Says hello to the given name
    /// </summary>
    /// <param name="name">The name to greet</param>
    [Function]
    public string Hello(string name) => $"Hello, {name}!";
}
`)

	out, err := modGen.
		With(daggerQuery(`{test{hello(name: "Docs")}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"hello":"Hello, Docs!"}}`, out)
}

func (CsharpSuite) TestNameCasing(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Field]
    public string MyFieldValue { get; set; } = "field";

    public Test(string myFieldValue = "field")
    {
        MyFieldValue = myFieldValue;
    }

    [Function]
    public string MyMethodName(string myParamName) => $"{myParamName}:{MyFieldValue}";
}
`)

	// C# uses PascalCase, GraphQL should use camelCase
	out, err := modGen.
		With(daggerQuery(`{test(myFieldValue: "test"){myMethodName(myParamName: "value")}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"myMethodName":"value:test"}}`, out)
}

func (CsharpSuite) TestReturnSelf(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Field]
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
		With(daggerQuery(`{test{withValue(value: "fluent"){getValue}}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"withValue":{"getValue":"fluent"}}}`, out)
}

func (CsharpSuite) TestListTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{returnStringList}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"returnStringList":["one","two","three"]}}`, out)
	})

	t.Run("return int list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{test{returnIntList}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"returnIntList":[1,2,3]}}`, out)
	})

	t.Run("accept string list", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{test{joinStrings(items: ["a","b","c"])}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"joinStrings":"a,b,c"}}`, out)
	})
}

func (CsharpSuite) TestCustomObjects(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class CustomObject(string name = "")
{
    [Field]
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
		With(daggerQuery(`{test{createCustom(name: "custom"){getName}}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test":{"createCustom":{"getName":"custom"}}}`, out)
}

func (CsharpSuite) TestErrors(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
		With(daggerQuery(`{test{throwError}}`)).
		Stdout(ctx)

	require.Error(t, err)
	requireErrOut(t, err, "intentional error")
}

func (CsharpSuite) TestFloatingPointTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
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
		out, err := modGen.With(daggerQuery(`{test{addFloat(a:1.5,b:2.5)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"addFloat":4.0}}`, out)
	})

	t.Run("add double", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{addDouble(a:10.5,b:20.75)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"addDouble":31.25}}`, out)
	})

	t.Run("add decimal", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{addDecimal(a:100.123,b:200.456)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"addDecimal":300.579}}`, out)
	})

	t.Run("multiply float with default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{multiplyFloat(a:5.5)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"multiplyFloat":11.0}}`, out)
	})

	t.Run("multiply float with explicit value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{multiplyFloat(a:3.0,b:4.0)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"multiplyFloat":12.0}}`, out)
	})

	t.Run("multiply double with default", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{multiplyDouble(a:7.5)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"multiplyDouble":22.5}}`, out)
	})

	t.Run("multiply double with explicit value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{multiplyDouble(a:2.5,b:4.0)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"multiplyDouble":10.0}}`, out)
	})
}

func (CsharpSuite) TestNullableParameters(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/Main.cs", `
#nullable enable
using Dagger;
using Dagger.Runtime;

[Object]
public class Test
{
    static Test()
    {
        // Enable debug logging to see nullable parameter detection
        ModuleRuntime.ConfigureLogging(true);
    }

    [Function]
    public string SayHello(string? name)
    {
        if (string.IsNullOrWhiteSpace(name))
        {
            return "Hello!";
        }
        return $"Hello, {name}!";
    }

    [Function]
    public string CountTo(int? number)
    {
        if (number == null)
        {
            return "No number provided";
        }
        return $"Counting to {number}";
    }

    [Function]
    public string IsEnabled(bool? enabled)
    {
        if (enabled == null)
        {
            return "Unknown state";
        }
        return enabled.Value ? "Enabled" : "Disabled";
    }
}
`)

	t.Run("nullable string without value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{sayHello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"sayHello":"Hello!"}}`, out)
	})

	t.Run("nullable string with value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{sayHello(name: "World")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"sayHello":"Hello, World!"}}`, out)
	})

	t.Run("nullable int without value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{countTo}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"countTo":"No number provided"}}`, out)
	})

	t.Run("nullable int with value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{countTo(number: 42)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"countTo":"Counting to 42"}}`, out)
	})

	t.Run("nullable bool without value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{isEnabled}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"isEnabled":"Unknown state"}}`, out)
	})

	t.Run("nullable bool with true", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{isEnabled(enabled: true)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"isEnabled":"Enabled"}}`, out)
	})

	t.Run("nullable bool with false", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{test{isEnabled(enabled: false)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"isEnabled":"Disabled"}}`, out)
	})
}

func (CsharpSuite) TestInterface(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/IFooer.cs", `
using Dagger;
using System.Threading.Tasks;

[Interface]
public interface IFooer
{
    [Function]
    Task<string> Foo(int bar);
}
`).
		WithNewFile("/work/Example.cs", `	
using Dagger;

[Object]
public class Example
{
    [Function]
    public string Foo(int bar) => $"number is: {bar}";
}
`).
		WithNewFile("/work/Main.cs", `
using Dagger;
using System.Threading.Tasks;

[Object]
public class Test
{
    [Function]
    public async Task<string> UseFooer(IFooer fooer, int value)
    {
        return await fooer.Foo(value);
    }
}
`)

	t.Run("interface is registered", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{__type(name: "IFooer"){name, kind}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"__type":{"name":"IFooer","kind":"INTERFACE"}}`, out)
	})

	t.Run("interface has functions", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{__type(name: "IFooer"){fields{name}}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.Contains(t, out, `"foo"`)
	})

	t.Run("object implements interface", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCall("use-fooer", "--fooer", "example", "--value", "42")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "number is: 42\n", out)
	})
}

func (CsharpSuite) TestInterfaceMultipleImplementations(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=github.com/pjmagee/dagger/sdk/csharp@dotnet-module-support")).
		WithNewFile("/work/IGreeter.cs", `
using Dagger;
using System.Threading.Tasks;

[Interface]
public interface IGreeter
{
    [Function]
    Task<string> Greet(string name);
}
`).
		WithNewFile("/work/FormalGreeter.cs", `
using Dagger;

[Object]
public class FormalGreeter
{
    [Function]
    public string Greet(string name) => $"Good day, {name}.";
}
`).
		WithNewFile("/work/CasualGreeter.cs", `
using Dagger;

[Object]
public class CasualGreeter
{
    [Function]
    public string Greet(string name) => $"Hey {name}!";
}
`).
		WithNewFile("/work/Main.cs", `
using Dagger;
using System.Threading.Tasks;

[Object]
public class Test
{
    [Function]
    public async Task<string> UseGreeter(IGreeter greeter, string name)
    {
        return await greeter.Greet(name);
    }
}
`)

	t.Run("interface registration with multiple implementations", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerQuery(`{__type(name: "IGreeter"){name, kind}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"__type":{"name":"IGreeter","kind":"INTERFACE"}}`, out)
	})

	t.Run("formal greeter", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCall("use-greeter", "--greeter", "formal-greeter", "--name", "Alice")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "Good day, Alice.\n", out)
	})

	t.Run("casual greeter", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.
			With(daggerCall("use-greeter", "--greeter", "casual-greeter", "--name", "Bob")).
			Stdout(ctx)

		require.NoError(t, err)
		require.Equal(t, "Hey Bob!\n", out)
	})
}
