package core

import (
	"context"
	"fmt"
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
	t.Run("from scratch", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=csharp"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("with different root", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=csharp", "child"))

		out, err := modGen.
			With(daggerQueryAt("child", `{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("camel-cases Dagger module name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=My-Module", "--sdk=csharp"))

		out, err := modGen.
			With(daggerQuery(`{myModule{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"myModule":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("with source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=csharp", "--source=some/subdir"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		sourceSubdirEnts, err := modGen.Directory("/work/some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, sourceSubdirEnts, "Main.cs")

		sourceRootEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, sourceRootEnts, "Main.cs")
	})

	t.Run("uses expected field casing", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
		WithNewFile("/work/Main.cs", `
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
		WithNewFile("/work/Main.cs", `
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
		WithNewFile("/work/Main.cs", `
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
		WithNewFile("/work/Main.cs", `
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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
		With(daggerExec("init", "--name=test", "--sdk=csharp")).
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

func (CsharpSuite) TestWithOtherModuleTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=csharp")).
		WithNewFile("/work/dep/Main.cs", `
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
`).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=csharp", "test")).
		With(daggerExec("install", "-m=test", "./dep")).
		WithWorkdir("/work/test")

	t.Run("return as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("/work/test/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public DepObj Fn() => null!;
}
`).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q function %q cannot return external type from dependency module %q`,
				"Test", "fn", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("/work/test/Main.cs", `
using Dagger;
using System.Collections.Generic;

[Object]
public class Test
{
    [Function]
    public List<DepObj> Fn() => null!;
}
`).
				With(daggerFunctions()).
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
				WithNewFile("/work/test/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Fn(DepObj obj) => "";
}
`).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q function %q arg %q cannot reference external type from dependency module %q`,
				"Test", "fn", "obj", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("/work/test/Main.cs", `
using Dagger;
using System.Collections.Generic;

[Object]
public class Test
{
    [Function]
    public string Fn(List<DepObj> objs) => "";
}
`).
				With(daggerFunctions()).
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
				WithNewFile("/work/test/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public DepObj? MyObj { get; set; }
}
`).
				With(daggerFunctions()).
				Sync(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				`object %q field %q cannot reference external type from dependency module %q`,
				"Test", "myObj", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("/work/test/Main.cs", `
using Dagger;
using System.Collections.Generic;

[Object]
public class Test
{
    [Function]
    public List<DepObj> MyObjs { get; set; } = new List<DepObj>();
}
`).
				With(daggerFunctions()).
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

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/IDuck.cs", `
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
`).
			WithNewFile("/work/Main.cs", `
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

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp", "--source=mymodule")).
			WithNewFile("/work/mymodule/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Hello() => "hello from subpath";
}
`)

		out, err := modGen.
			With(daggerQuery(`{test{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"hello":"hello from subpath"}}`, out)
	})
}

func (CsharpSuite) TestVariadicParameters(ctx context.Context, t *testctx.T) {
	t.Run("params string array", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{join(messages:["hello","world","from","csharp"])}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"join":"hello, world, from, csharp"}}`, out)
	})

	t.Run("params int array", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{sum(numbers:[1,2,3,4,5])}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"sum":15}}`, out)
	})

	t.Run("empty params array", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{count(items:[])}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"count":0}}`, out)
	})
}

func (CsharpSuite) TestNameAttribute(ctx context.Context, t *testctx.T) {
	t.Run("function parameter name override", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{echo(msg:"hello")}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"echo":"hello"}}`, out)
	})

	t.Run("constructor parameter name override", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    private readonly string source;

    public Test([Name("src")] string source = "default")
    {
        this.source = source;
    }

    [Function]
    public string GetSource()
    {
        return source;
    }
}
`)

		out, err := modGen.
			With(daggerQuery(`{test(src:"custom"){getSource}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"getSource":"custom"}}`, out)
	})

	t.Run("field name override", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Name("val")]
    public string Value { get; set; } = "field value";

    [Function]
    public string GetValue() => Value;
}
`)

		out, err := modGen.
			With(daggerQuery(`{test{val}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"val":"field value"}}`, out)
	})

	t.Run("function name override", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    [Name("greet")]
    public string SayHello(string name)
    {
        return $"Hello, {name}!";
    }
}
`)

		out, err := modGen.
			With(daggerQuery(`{test{greet(name:"World")}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"greet":"Hello, World!"}}`, out)
	})
}

func (CsharpSuite) TestReservedKeywords(ctx context.Context, t *testctx.T) {
	t.Run("parameter with reserved keyword names", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{echo(class:"MyClass",event:"Click",namespace:"MyApp")}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"echo":"MyClass,Click,MyApp"}}`, out)
	})

	t.Run("constructor with reserved keywords", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    private readonly string type;

    public Test(string @type = "default")
    {
        this.type = @type;
    }

    [Function]
    public string GetType()
    {
        return type;
    }
}
`)

		out, err := modGen.
			With(daggerQuery(`{test(type:"custom"){getType}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"getType":"custom"}}`, out)
	})
}

func (CsharpSuite) TestEnumCollections(ctx context.Context, t *testctx.T) {
	t.Run("return List of enums", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{getStatuses}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"getStatuses":["PENDING","ACTIVE","COMPLETED"]}}`, out)
	})

	t.Run("return array of enums", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{getPriorities}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"getPriorities":["LOW","MEDIUM","HIGH"]}}`, out)
	})

	t.Run("accept List of enums", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{joinColors(colors:[RED,BLUE])}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"joinColors":"Red,Blue"}}`, out)
	})

	t.Run("accept array of enums", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{test{countLevels(levels:[DEBUG,INFO,WARNING,ERROR])}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"countLevels":4}}`, out)
	})
}

func (CsharpSuite) TestCheckFunctions(ctx context.Context, t *testctx.T) {
	t.Run("void check passes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Check]
    public void PassingCheck()
    {
        // Check passes by not throwing
    }
}
`)

		out, err := modGen.
			With(daggerCallAt("test", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "✔")
	})

	t.Run("void check fails", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using System;
using Dagger;

[Object]
public class Test
{
    [Check]
    public void FailingCheck()
    {
        throw new Exception("validation failed");
    }
}
`)

		_, err := modGen.
			With(daggerCallAt("test", "check")).
			Stdout(ctx)
		require.Error(t, err)
		require.Contains(t, err.Error(), "validation failed")
	})

	t.Run("Task check passes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using System.Threading.Tasks;
using Dagger;

[Object]
public class Test
{
    [Check]
    public async Task AsyncPassingCheck()
    {
        await Task.Delay(10);
        // Check passes by not throwing
    }
}
`)

		out, err := modGen.
			With(daggerCallAt("test", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "✔")
	})

	t.Run("Container check passes on exit code 0", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Check]
    public Container ValidateWithContainer()
    {
        return Dag.Container()
            .From("`+alpineImage+`")
            .WithExec(new[] { "sh", "-c", "exit 0" });
    }
}
`)

		out, err := modGen.
			With(daggerCallAt("test", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "✔")
	})

	t.Run("Container check fails on non-zero exit code", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Check]
    public Container FailingContainerCheck()
    {
        return Dag.Container()
            .From("`+alpineImage+`")
            .WithExec(new[] { "sh", "-c", "exit 1" });
    }
}
`)

		_, err := modGen.
			With(daggerCallAt("test", "check")).
			Stdout(ctx)
		require.Error(t, err)
	})

	t.Run("Task<Container> check passes", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using System.Threading.Tasks;
using Dagger;

[Object]
public class Test
{
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
			With(daggerCallAt("test", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "✔")
	})

	t.Run("check with optional parameters", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
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
			With(daggerCallAt("test", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "✔")
	})

	t.Run("check with DefaultPath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
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
			With(daggerCallAt("test", "check")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "✔")
	})
}

func (CsharpSuite) TestFieldDocumentation(ctx context.Context, t *testctx.T) {
	t.Run("field with XML documentation", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{__type(name:"Test"){fields{name description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "The name of the test")
	})

	t.Run("private field without documentation", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{__type(name:"Test"){fields{name description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "This is public and should appear")
		require.NotContains(t, out, "privateField")
	})
}

func (CsharpSuite) TestDeprecatedAttribute(ctx context.Context, t *testctx.T) {
	t.Run("deprecated function", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using System;
using Dagger;

[Object]
public class Test
{
    [Obsolete("Use NewMethod instead")]
    [Function]
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

		out, err := modGen.
			With(daggerQuery(`{__type(name:"Test"){fields{name isDeprecated deprecationReason}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Use NewMethod instead")
		require.Contains(t, out, "isDeprecated")
	})

	t.Run("deprecated parameter", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using System;
using Dagger;

[Object]
public class Test
{
    [Function]
    public string Echo([Obsolete("Use message instead")] string msg = "")
    {
        return msg;
    }
}
`)

		out, err := modGen.
			With(daggerQuery(`{__type(name:"Test"){fields{name args{name isDeprecated deprecationReason}}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Use message instead")
	})
}

func (CsharpSuite) TestIgnoreAttribute(ctx context.Context, t *testctx.T) {
	t.Run("ignore field", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    public string PublicField { get; set; } = "public";

    [Ignore]
    public string IgnoredField { get; set; } = "ignored";

    [Function]
    public string GetPublic() => PublicField;
}
`)

		out, err := modGen.
			With(daggerQuery(`{__type(name:"Test"){fields{name}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "publicField")
		require.NotContains(t, out, "ignoredField")
	})

	t.Run("ignore function", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    [Function]
    public string PublicMethod() => "public";

    [Ignore]
    [Function]
    public string IgnoredMethod() => "ignored";
}
`)

		out, err := modGen.
			With(daggerQuery(`{__type(name:"Test"){fields{name}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "publicMethod")
		require.NotContains(t, out, "ignoredMethod")
	})
}

func (CsharpSuite) TestAlternativeConstructors(ctx context.Context, t *testctx.T) {
	t.Run("static factory method", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
using Dagger;

[Object]
public class Test
{
    private readonly string value;

    private Test(string value)
    {
        this.value = value;
    }

    [Constructor]
    public static Test Create(string initialValue)
    {
        return new Test(initialValue);
    }

    [Function]
    public string GetValue() => value;
}
`)

		out, err := modGen.
			With(daggerQuery(`{test{create(initialValue:"factory"){getValue}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"create":{"getValue":"factory"}}}`, out)
	})

	t.Run("multiple factory methods", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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

		out, err := modGen.
			With(daggerQuery(`{test{fromString(value:"hello"){getSource}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"fromString":{"getSource":"string:hello"}}}`, out)

		out, err = modGen.
			With(daggerQuery(`{test{fromInt(number:42){getSource}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test":{"fromInt":{"getSource":"int:42"}}}`, out)
	})
}

func (CsharpSuite) TestInheritedDocs(ctx context.Context, t *testctx.T) {
	t.Run("inherit from base class", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{__type(name:"Test"){fields{name description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Gets the greeting message")
	})

	t.Run("inherit from interface", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=csharp")).
			WithNewFile("/work/Main.cs", `
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
			With(daggerQuery(`{__type(name:"Test"){fields{name description}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.Contains(t, out, "Returns a greeting message")
	})
}
