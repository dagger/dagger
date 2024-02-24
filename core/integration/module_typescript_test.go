package core

import (
	_ "embed"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"

	"dagger.io/dagger"
)

func TestModuleTypescriptInit(t *testing.T) {
	t.Parallel()

	t.Run("from scratch", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("with different root", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=typescript", "child"))

		out, err := modGen.
			With(daggerQueryAt("child", `{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("camel-cases Dagger module name", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=My-Module", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{myModule{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"myModule":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("respect existing package.json", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/package.json", dagger.ContainerWithNewFileOpts{
				Contents: `{
  "name": "my-module",
  "version": "1.0.0",
  "description": "My module",
  "main": "index.js",
  "scripts": {
	"test": "echo \"Error: no test specified\" && exit 1"
  },
  "author": "John doe",
  "license": "MIT"
	}`,
			}).
			With(daggerExec("init", "--source=.", "--name=hasPkgJson", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{hasPkgJson{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasPkgJson":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("Add dagger dependencies to the existing package.json", func(t *testing.T) {
			pkgJSON, err := modGen.File("/work/package.json").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, pkgJSON, `"@dagger.io/dagger":`)
			require.Contains(t, pkgJSON, `"name": "my-module"`)
		})
	})

	t.Run("respect existing tsconfig.json", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/tsconfig.json", dagger.ContainerWithNewFileOpts{
				Contents: `{
	"compilerOptions": {
	  "target": "ES2022",
	  "moduleResolution": "Node",
	  "experimentalDecorators": true
	}
		}`,
			}).
			With(daggerExec("init", "--name=hasTsConfig", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{hasTsConfig{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasTsConfig":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("Add dagger paths to the existing tsconfig.json", func(t *testing.T) {
			tsConfig, err := modGen.File("/work/dagger/tsconfig.json").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, tsConfig, `"@dagger.io/dagger":`)
		})
	})

	t.Run("respect existing src/index.ts", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithDirectory("/work/src", c.Directory()).
			WithNewFile("/work/src/index.ts", dagger.ContainerWithNewFileOpts{
				Contents: `
				import { dag, Container, object, func } from "@dagger.io/dagger"

				@object()
				class ExistingSource {
				  @func()
				  helloWorld(stringArg: string): Container {
					return dag.container().from("alpine:latest").withExec(["echo", stringArg])
				  }
				}

				`,
			}).
			With(daggerExec("init", "--source=.", "--name=existingSource", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{existingSource{helloWorld(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"existingSource":{"helloWorld":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("with source", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=typescript", "--source=some/subdir"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		sourceSubdirEnts, err := modGen.Directory("/work/some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, sourceSubdirEnts, "src")

		sourceRootEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, sourceRootEnts, "src")
	})
}

//go:embed testdata/modules/typescript/syntax/index.ts
var tsSyntax string

func TestModuleTypescriptSyntaxSupport(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=syntax", "--sdk=typescript")).
		With(sdkSource("typescript", tsSyntax))

	t.Run("singleQuoteDefaultArgHello(msg: string = 'world'): string", func(t *testing.T) {
		defaultOut, err := modGen.With(daggerQuery(`{syntax{singleQuoteDefaultArgHello}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"singleQuoteDefaultArgHello":"hello world"}}`, defaultOut)

		out, err := modGen.With(daggerQuery(`{syntax{singleQuoteDefaultArgHello(msg: "dagger")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"singleQuoteDefaultArgHello":"hello dagger"}}`, out)
	})

	t.Run("doubleQuotesDefaultArgHello(msg: string = \"world\"): string", func(t *testing.T) {
		defaultOut, err := modGen.With(daggerQuery(`{syntax{doubleQuotesDefaultArgHello}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"doubleQuotesDefaultArgHello":"hello world"}}`, defaultOut)

		out, err := modGen.With(daggerQuery(`{syntax{doubleQuotesDefaultArgHello(msg: "dagger")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"doubleQuotesDefaultArgHello":"hello dagger"}}`, out)
	})
}

//go:embed testdata/modules/typescript/minimal/index.ts
var tsSignatures string

func TestModuleTypescriptSignatures(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=typescript")).
		With(sdkSource("typescript", tsSignatures))

	t.Run("hello(): string", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"hello":"hello"}}`, out)
	})

	t.Run("echoes(msgs: string[]): string[]", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoes(msgs: ["hello"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoes":["hello...hello...hello..."]}}`, out)
	})

	t.Run("echoOptional(msg = 'default'): string", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptional(msg: "hello")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"hello...hello...hello..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptional}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"default...default...default..."}}`, out)
	})

	t.Run("echoesVariadic(...msgs: string[]): string", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoesVariadic(msgs: ["hello"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoesVariadic":"hello...hello...hello..."}}`, out)
	})

	t.Run("echo(msg: string): string", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echo(msg: "hello")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echo":"hello...hello...hello..."}}`, out)
	})

	t.Run("echoOptionalSlice(msg = ['foobar']): string", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptionalSlice(msg: ["hello", "there"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"hello+there...hello+there...hello+there..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptionalSlice}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"foobar...foobar...foobar..."}}`, out)
	})

	t.Run("helloVoid(): void", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{helloVoid}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoid":null}}`, out)
	})

	t.Run("echoOpts(msg: string, suffix: string = '', times: number = 1): string", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi!hi!"}}`, out)

		t.Run("execute with unordered args", func(t *testing.T) {
			out, err = modGen.With(daggerQuery(`{minimal{echoOpts(times: 2, msg: "order", suffix: "?")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"echoOpts":"order?order?"}}`, out)
		})
	})

	t.Run("echoMaybe(msg: string, isQuestion = false): string", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoMaybe(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoMaybe":"hi...hi...hi..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoMaybe(msg: "hi", isQuestion: true)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoMaybe":"hi?...hi?...hi?..."}}`, out)

		t.Run("execute with unordered args", func(t *testing.T) {
			out, err = modGen.With(daggerQuery(`{minimal{echoMaybe(isQuestion: false, msg: "hi")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"echoMaybe":"hi...hi...hi..."}}`, out)
		})
	})
}

//go:embed testdata/modules/typescript/minimal/builtin.ts
var tsSignaturesBuiltin string

func TestModuleTypescriptSignaturesBuildinTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=typescript")).
		With(sdkSource("typescript", tsSignaturesBuiltin))

	out, err := modGen.With(daggerQuery(`{directory{withNewFile(path: "foo", contents: "bar"){id}}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.withNewFile.id").String()

	t.Run("async read(dir: Directory): Promise<string>", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{read(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"read":"bar"}}`, out)
	})

	t.Run("async readSlice(dir: Directory[]): Promise<string>", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readSlice(dir: ["%s"])}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readSlice":"bar"}}`, out)
	})

	t.Run("async readVariadic(...dir: Directory[]): Promise<string>", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readVariadic(dir: ["%s"])}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readVariadic":"bar"}}`, out)
	})

	t.Run("async readOptional(dir?: Directory): Promise<string>", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readOptional(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readOptional":"bar"}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{readOptional}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readOptional":""}}`, out)
	})
}

//go:embed testdata/modules/typescript/minimal/unexported.ts
var tsSignaturesUnexported string

// TODO: Fixes DEV-3343 and update this test
func TestModuleTypescriptSignatureUnexported(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=typescript")).
		With(sdkSource("typescript", tsSignaturesUnexported))

	objs := inspectModuleObjects(ctx, t, modGen)

	require.Equal(t, 2, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("1.name").String())
	require.Equal(t, "MinimalFoo", objs.Get("0.name").String())
}

func TestModuleTypescriptDocs(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=typescript")).
		With(sdkSource("typescript", tsSignatures))

	obj := inspectModuleObjects(ctx, t, modGen).Get("0")
	require.Equal(t, "Minimal", obj.Get("name").String())
	require.Equal(t, "This is the Minimal object", obj.Get("description").String())

	fooField := obj.Get(`fields.#(name="foo")`)
	require.Equal(t, "foo", fooField.Get("name").String())
	require.Equal(t, "This is a field", fooField.Get("description").String())

	hello := obj.Get(`functions.#(name="hello")`)
	require.Equal(t, "hello", hello.Get("name").String())
	require.Empty(t, hello.Get("description").String())
	require.Empty(t, hello.Get("args").Array())

	echoOpts := obj.Get(`functions.#(name="echoOpts")`)
	require.Equal(t, "echoOpts", echoOpts.Get("name").String())
	require.Equal(t, "EchoOpts does some opts things", echoOpts.Get("description").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "the message to echo", echoOpts.Get("args.0.description").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "number of times to repeat the message", echoOpts.Get("args.2.description").String())

	echoMaybe := obj.Get(`functions.#(name="echoMaybe")`)
	require.Equal(t, "echoMaybe", echoMaybe.Get("name").String())
	require.Empty(t, echoMaybe.Get("description").String())
	require.Len(t, echoMaybe.Get("args").Array(), 2)
	require.Equal(t, "msg", echoMaybe.Get("args.0.name").String())
	require.Equal(t, "the message to echo", echoMaybe.Get("args.0.description").String())
	require.Equal(t, "isQuestion", echoMaybe.Get("args.1.name").String())
	require.Equal(t, "set to true to add a question mark.", echoMaybe.Get("args.1.description").String())
}

//go:embed testdata/modules/typescript/optional/index.ts
var tsOptional string

func TestModuleTypescriptOptional(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=typescript")).
		With(sdkSource("typescript", tsOptional))

	out, err := modGen.With(daggerQuery(`{minimal{foo}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal": {"foo": ""}}`, out)

	out, err = modGen.With(daggerQuery(`{minimal{isEmpty}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal": {"isEmpty": true}}`, out)
}

//go:embed testdata/modules/typescript/runtime-detection/index.ts
var runtimeDetection string

func TestModuleTypescriptRuntimeDetection(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript")).
		With(sdkSource("typescript", runtimeDetection))

	t.Run("should default to node", func(t *testing.T) {
		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"node"}}`, out)
	})

	t.Run("should use package.json configuration node", func(t *testing.T) {
		modGen := modGen.WithNewFile("/work/dagger/package.json", dagger.ContainerWithNewFileOpts{
			Contents: `{
				"name": "my-module",
				"version": "1.0.0",
				"description": "My module",
				"main": "index.js",
				"scripts": {
					"test": "echo \"Error: no test specified\" && exit 1"
				},
				"author": "John doe",
				"license": "MIT",
				"dagger": {
					"runtime": "node"
				}
			}`,
		})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"node"}}`, out)
	})

	t.Run("should use package.json configuration bun", func(t *testing.T) {
		modGen := modGen.WithNewFile("/work/dagger/package.json", dagger.ContainerWithNewFileOpts{
			Contents: `{
				"name": "my-module",
				"version": "1.0.0",
				"description": "My module",
				"main": "index.js",
				"scripts": {
					"test": "echo \"Error: no test specified\" && exit 1"
				},
				"author": "John doe",
				"license": "MIT",
				"dagger": {
					"runtime": "bun"
				}
			}`,
		})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"bun"}}`, out)
	})

	t.Run("should detect package-lock.json", func(t *testing.T) {
		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript")).
			With(sdkSource("typescript", runtimeDetection)).
			WithExec([]string{"npm", "install", "--package-lock-only", "-C", "./dagger"})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"node"}}`, out)
	})

	t.Run("should detect bun.lockb", func(t *testing.T) {
		modGen := c.Container().From("oven/bun:1.0.27-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript")).
			With(sdkSource("typescript", runtimeDetection)).
			WithExec([]string{"bun", "install", "--cwd", "./dagger"})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"bun"}}`, out)
	})

	t.Run("should prioritize package.json config over file detection", func(t *testing.T) {
		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript")).
			With(sdkSource("typescript", runtimeDetection)).
			WithNewFile("/work/dagger/package.json", dagger.ContainerWithNewFileOpts{
				Contents: `{
					"name": "my-module",
					"version": "1.0.0",
					"description": "My module",
					"main": "index.js",
					"scripts": {
						"test": "echo \"Error: no test specified\" && exit 1"
					},
					"author": "John doe",
					"license": "MIT",
					"dagger": {
						"runtime": "bun"
					}
				}`,
			}).
			WithExec([]string{"npm", "install", "--package-lock-only", "-C", "./dagger"})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"bun"}}`, out)
	})
}

func TestModuleTypescriptWithOtherModuleTypes(t *testing.T) {
	t.Parallel()

	c, ctx := connect(t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=typescript")).
		With(sdkSource("typescript", `
	import {  object, func, field } from "@dagger.io/dagger"

@object()
class Dep {
  @func()
  fn(): Obj {
    return new Obj("foo")
  }
}

@object()
class Obj {
  @field()
  foo: string = ""

  constructor(foo: string) {
    this.foo = foo
  }
}

@object()
class Foo {}
`)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=typescript", "test")).
		With(daggerExec("install", "-m=test", "./dep")).
		WithWorkdir("/work/test")

	t.Run("return as other module object", func(t *testing.T) {
		t.Run("direct", func(t *testing.T) {
			_, err := ctr.With(sdkSource("typescript", `
			import { object, func, DepObj } from "@dagger.io/dagger"

			@object()
			class Test {
			  @func()
			  fn(): DepObj {
				 return new DepObj()
			  }
			}
			`)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+cannot\s+return\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "dep",
			), err.Error())
		})

		t.Run("list", func(t *testing.T) {
			_, err := ctr.With(sdkSource("typescript", `
			import { object, func, DepObj } from "@dagger.io/dagger"

			@object()
			class Test {
			  @func()
			  fn(): DepObj[] {
				 return [new DepObj()]
			  }
			}
			`)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+cannot\s+return\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "dep",
			), err.Error())
		})
	})

	t.Run("arg as other module object", func(t *testing.T) {
		t.Run("direct", func(t *testing.T) {
			_, err := ctr.With(sdkSource("typescript", `
import { object, func, DepObj } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  fn(obj: DepObj): void {}
}
			`)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+arg\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "obj", "dep",
			), err.Error())
		})

		t.Run("list", func(t *testing.T) {
			_, err := ctr.
				With(sdkSource("typescript", `
import { object, func, DepObj } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  fn(obj: DepObj[]): void {}
}
			`)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+arg\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "obj", "dep",
			), err.Error())
		})
	})

	t.Run("field as other module object", func(t *testing.T) {
		t.Run("direct", func(t *testing.T) {
			_, err := ctr.
				With(sdkSource("typescript", `
import { object, func, DepObj } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  fn(): Obj {
    return new Obj()
  }
}

@object()
class Obj {
  @field()
  foo: DepObj
}
			`)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+field\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Obj", "foo", "dep",
			), err.Error())
		})

		t.Run("list", func(t *testing.T) {
			_, err := ctr.
				With(sdkSource("typescript", `
import { object, func, DepObj } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  fn(): Obj {
    return new Obj()
  }
}

@object()
class Obj {
  @field()
  foo: DepObj[]
}
			`)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			require.Regexp(t, fmt.Sprintf(
				`object\s+%q\s+field\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Obj", "foo", "dep",
			), err.Error())
		})
	})
}

func TestModuleTypescriptAliases(t *testing.T) {
	t.Parallel()

	t.Run("alias in function", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=alias", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { object, func } from "@dagger.io/dagger"

@object()
class Alias {
  @func("bar")
  foo(): string {
	return "hello world"
  }
}
`))

		out, err := modGen.With(daggerQuery(`{alias{bar}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"alias": {"bar": "hello world"}}`, out)
	})

	t.Run("nested alias in function", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=alias", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { object, func } from "@dagger.io/dagger"

@object()
class SubSub {
	@func("zoo")
	subSubHello(): string {
		return "hello world"
	}
}

@object()
class Sub {
	@func("hello")
	subHello(): SubSub {
		return new SubSub()
	}
}

@object()
class Alias {
  @func("bar")
  foo(): Sub {
	return new Sub()
  }
}
`))

		out, err := modGen.With(daggerQuery(`{alias{bar{hello{zoo}}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"alias": {"bar": {"hello": {"zoo": "hello world"}}}}`, out)
	})

	t.Run("nested alias in field", func(t *testing.T) {
		t.Parallel()

		c, ctx := connect(t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=alias", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { object, func, field } from "@dagger.io/dagger"

@object()
class SuperSubSub {
	@field("farFarNested")
	far = true
}

@object()
class SubSub {
	@field("zoo")
	a = 4

	@field("hey")
	b = [true, false, true]

	@field("far")
	subsubsub = new SuperSubSub()
}

@object()
class Sub {
	@field("hello")
	hey = "a"

	@field("foo")
	sub = new SubSub()
}

@object()
class Alias {
  @func("bar")
  foo(): Sub {
	return new Sub()
  }
}
`))

		out, err := modGen.With(daggerQuery(`{alias{bar{hello,foo{zoo,hey,far{farFarNested}}}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"alias": {"bar": {"hello": "a", "foo": {"zoo": 4, "hey": [true, false, true], "far": {"farFarNested": true} }}}}`, out)
	})
}
