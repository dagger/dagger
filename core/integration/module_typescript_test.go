package core

import (
	"context"
	_ "embed"
	"fmt"
	"strings"
	"testing"

	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

// Group all tests that are specific to TypeScript only.
type TypescriptSuite struct{}

func TestTypescript(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(TypescriptSuite{})
}

func (TypescriptSuite) TestInit(ctx context.Context, t *testctx.T) {
	t.Run("from scratch", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

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

	t.Run("with different root", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

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

	t.Run("camel-cases Dagger module name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

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

	t.Run("respect existing package.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/package.json", `{
  "name": "my-module",
  "version": "1.0.0",
  "description": "My module",
  "main": "index.js",
  "scripts": {
  "test": "echo \"Error: no test specified\" && exit 1"
  },
  "author": "John doe",
  "license": "MIT",
  "type": "module"
  }`,
			).
			With(daggerExec("init", "--source=.", "--name=hasPkgJson", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{hasPkgJson{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasPkgJson":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("Add dagger dependencies to the existing package.json", func(ctx context.Context, t *testctx.T) {
			pkgJSON, err := modGen.File("/work/package.json").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, pkgJSON, `"typescript":`)
			require.NotContains(t, pkgJSON, `"@dagger.io/dagger":`)
			require.Contains(t, pkgJSON, `"name": "my-module"`)
		})
	})

	t.Run("respect existing tsconfig.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/tsconfig.json", `{
  "compilerOptions": {
    "target": "ES2022",
    "moduleResolution": "Node",
    "experimentalDecorators": true
  }
    }`,
			).
			With(daggerExec("init", "--source=.", "--name=hasTsConfig", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{hasTsConfig{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasTsConfig":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("Add dagger paths to the existing tsconfig.json", func(ctx context.Context, t *testctx.T) {
			tsConfig, err := modGen.File("/work/tsconfig.json").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, tsConfig, `"@dagger.io/dagger":`)
		})
	})

	t.Run("respect existing src/index.ts", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithDirectory("/work/src", c.Directory()).
			WithNewFile("/work/src/index.ts", `
        import { dag, Container, object, func } from "@dagger.io/dagger"

        @object()
        export class ExistingSource {
          @func()
          helloWorld(stringArg: string): Container {
          return dag.container().from("`+alpineImage+`").withExec(["echo", stringArg])
          }
        }

        `,
			).
			With(daggerExec("init", "--source=.", "--name=existingSource", "--sdk=typescript"))

		out, err := modGen.
			With(daggerQuery(`{existingSource{helloWorld(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"existingSource":{"helloWorld":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("with source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

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
		require.Contains(t, sourceSubdirEnts, "src/")

		sourceRootEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, sourceRootEnts, "src/")
	})

	t.Run("ignore parent directory package manager", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"npm", "init", "-y"}).
			WithExec([]string{"corepack", "enable"}).
			WithExec([]string{"corepack", "use", "pnpm@9.6.0"}).
			With(daggerExec("init", "--sdk=typescript", "--source=dagger"))

		out, err := modGen.With(daggerCall("container-echo", "--string-arg", "hello world", "stdout")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello world\n", out)

		parentPackageJSON, err := modGen.File("./package.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, parentPackageJSON, `"packageManager": "pnpm@`) // We don't check the exact version because it's a SHA

		sourcePackageJSON, err := modGen.File("./dagger/package.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, sourcePackageJSON, `"packageManager": "yarn@`) // We don't check the exact version because it's a SHA
	})

	t.Run("init module in .dagger if files present in current dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/src/index.ts", `
        import { dag, Container, object, func } from "@dagger.io/dagger"

        @object()
        class ExistingSource {
          @func()
          helloWorld(stringArg: string): Container {
          return dag.container().from("`+alpineImage+`").withExec(["echo", stringArg])
          }
        }

        `,
			).
			With(daggerExec("init", "--name=bare", "--sdk=typescript"))

		daggerDirEnts, err := modGen.Directory("/work/.dagger").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerDirEnts, "package.json", "sdk", "src", "tsconfig.json", "yarn.lock")

		out, err := modGen.
			WithWorkdir("/work").
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("init module when current dir only has hidden dirs", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"mkdir", "-p", ".git"}).
			With(daggerExec("init", "--name=bare", "--sdk=typescript"))

		daggerDirEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerDirEnts, "dagger.json", "package.json", "sdk", "src", "tsconfig.json", "yarn.lock")

		out, err := modGen.
			WithWorkdir("/work").
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})
}

//go:embed testdata/modules/typescript/syntax/index.ts
var tsSyntax string

func (TypescriptSuite) TestSyntaxSupport(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=syntax", "--sdk=typescript")).
		With(sdkSource("typescript", tsSyntax))

	t.Run("singleQuoteDefaultArgHello(msg: string = 'world'): string", func(ctx context.Context, t *testctx.T) {
		defaultOut, err := modGen.With(daggerQuery(`{syntax{singleQuoteDefaultArgHello}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"singleQuoteDefaultArgHello":"hello world"}}`, defaultOut)

		out, err := modGen.With(daggerQuery(`{syntax{singleQuoteDefaultArgHello(msg: "dagger")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"syntax":{"singleQuoteDefaultArgHello":"hello dagger"}}`, out)
	})

	t.Run("doubleQuotesDefaultArgHello(msg: string = \"world\"): string", func(ctx context.Context, t *testctx.T) {
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

func (TypescriptSuite) TestSignatures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=typescript")).
		With(sdkSource("typescript", tsSignatures))

	t.Run("hello(): string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"hello":"hello"}}`, out)
	})

	t.Run("echoes(msgs: string[]): string[]", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoes(msgs: ["hello"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoes":["hello...hello...hello..."]}}`, out)
	})

	t.Run("echoOptional(msg = 'default'): string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptional(msg: "hello")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"hello...hello...hello..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptional}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"default...default...default..."}}`, out)
	})

	t.Run("echoesVariadic(...msgs: string[]): string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoesVariadic(msgs: ["hello"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoesVariadic":"hello...hello...hello..."}}`, out)
	})

	t.Run("echo(msg: string): string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echo(msg: "hello")}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echo":"hello...hello...hello..."}}`, out)
	})

	t.Run("echoOptionalSlice(msg = ['foobar']): string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptionalSlice(msg: ["hello", "there"])}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"hello+there...hello+there...hello+there..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptionalSlice}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"foobar...foobar...foobar..."}}`, out)
	})

	t.Run("helloVoid(): void", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{helloVoid}}`)).Stdout(ctx)

		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoid":null}}`, out)
	})

	t.Run("echoOpts(msg: string, suffix: string = '', times: number = 1): string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi!hi!"}}`, out)

		t.Run("execute with unordered args", func(ctx context.Context, t *testctx.T) {
			out, err = modGen.With(daggerQuery(`{minimal{echoOpts(times: 2, msg: "order", suffix: "?")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"echoOpts":"order?order?"}}`, out)
		})
	})

	t.Run("echoMaybe(msg: string, isQuestion = false): string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoMaybe(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoMaybe":"hi...hi...hi..."}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoMaybe(msg: "hi", isQuestion: true)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoMaybe":"hi?...hi?...hi?..."}}`, out)

		t.Run("execute with unordered args", func(ctx context.Context, t *testctx.T) {
			out, err = modGen.With(daggerQuery(`{minimal{echoMaybe(isQuestion: false, msg: "hi")}}`)).Stdout(ctx)
			require.NoError(t, err)
			require.JSONEq(t, `{"minimal":{"echoMaybe":"hi...hi...hi..."}}`, out)
		})
	})
}

//go:embed testdata/modules/typescript/minimal/builtin.ts
var tsSignaturesBuiltin string

func (TypescriptSuite) TestSignaturesBuildinTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=typescript")).
		With(sdkSource("typescript", tsSignaturesBuiltin))

	out, err := modGen.With(daggerQuery(`{directory{withNewFile(path: "foo", contents: "bar"){id}}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.withNewFile.id").String()

	t.Run("async read(dir: Directory): Promise<string>", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{read(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"read":"bar"}}`, out)
	})

	t.Run("async readSlice(dir: Directory[]): Promise<string>", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readSlice(dir: ["%s"])}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readSlice":"bar"}}`, out)
	})

	t.Run("async readVariadic(...dir: Directory[]): Promise<string>", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readVariadic(dir: ["%s"])}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readVariadic":"bar"}}`, out)
	})

	t.Run("async readOptional(dir?: Directory): Promise<string>", func(ctx context.Context, t *testctx.T) {
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
func (TypescriptSuite) TestSignatureUnexported(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=typescript")).
		With(sdkSource("typescript", tsSignaturesUnexported))

	objs := inspectModuleObjects(ctx, t, modGen)

	// Now that we resolve by reference, we should only have one object
	require.Equal(t, 1, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())
}

func (TypescriptSuite) TestDocs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

func (TypescriptSuite) TestOptional(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

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

	out, err = modGen.With(daggerQuery(`{minimal{resolveValue}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal": {"resolveValue": "hello world"}}`, out)
}

func (TypescriptSuite) TestRuntimeDetection(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript")).
		With(sdkSource("typescript", `
      import { dag, Container, Directory, object, func } from "@dagger.io/dagger";
      @object()
      export class RuntimeDetection {
        @func()
        echoRuntime(): string {
          const isBunRuntime = typeof Bun === "object";
          return isBunRuntime ? "bun" : "node";
        }

        @func()
        version(): string {
          const isBunRuntime = typeof Bun === "object";
          const runtime = isBunRuntime ? "bun" : "node";
          let version = "";

          if (!isBunRuntime) {
            const [major, minor, patch] = process.versions.node.split(".").map(Number);
            version = major + "." + minor + "." + patch;
          } else {
            version = Bun.version
          }

          return runtime + "@" + version;
        }
      }
    `))

	t.Run("should default to node", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"node"}}`, out)
	})

	t.Run("should use package.json configuration node", func(ctx context.Context, t *testctx.T) {
		modGen := modGen.WithNewFile("/work/package.json", `{
        "dagger": {
          "runtime": "node"
        }
      }`,
		)

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"node"}}`, out)
	})

	t.Run("should use package.json configuration bun", func(ctx context.Context, t *testctx.T) {
		modGen := modGen.WithNewFile("/work/package.json", `{
        "dagger": {
          "runtime": "bun"
        }
      }`,
		)

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"bun"}}`, out)
	})

	t.Run("should detect package-lock.json", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript", "--source=.")).
			With(sdkSource("typescript", `
        import { dag, Container, Directory, object, func } from "@dagger.io/dagger";

        @object()
        export class RuntimeDetection {
          @func()
          echoRuntime(): string {
          const isBunRuntime = typeof Bun === "object";
          return isBunRuntime ? "bun" : "node";
          }
        }
      `)).
			WithExec([]string{"npm", "install", "--package-lock-only"})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"node"}}`, out)
	})

	t.Run("should detect bun.lockb", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From("oven/bun:1.0.27-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript")).
			With(sdkSource("typescript", `
        import { dag, Container, Directory, object, func } from "@dagger.io/dagger";

        @object()
        export class RuntimeDetection {
          @func()
          echoRuntime(): string {
          const isBunRuntime = typeof Bun === "object";
          return isBunRuntime ? "bun" : "node";
          }
        }
      `)).
			WithExec([]string{"bun", "install"})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"bun"}}`, out)
	})

	t.Run("should detect bun.lock", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From("oven/bun:1.2.4-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript")).
			With(sdkSource("typescript", `
        import { dag, Container, Directory, object, func } from "@dagger.io/dagger";

        @object()
        export class RuntimeDetection {
          @func()
          echoRuntime(): string {
          const isBunRuntime = typeof Bun === "object";
          return isBunRuntime ? "bun" : "node";
          }
        }
      `)).
			WithExec([]string{"bun", "install"})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"bun"}}`, out)
	})

	t.Run("should prioritize package.json config over file detection", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript")).
			With(sdkSource("typescript", `
        import { dag, Container, Directory, object, func } from "@dagger.io/dagger";

        @object()
        export class RuntimeDetection {
          @func()
          echoRuntime(): string {
          const isBunRuntime = typeof Bun === "object";
          return isBunRuntime ? "bun" : "node";
          }
        }
      `)).
			WithNewFile("/work/package.json", `{
          "dagger": {
            "runtime": "bun"
          }
        }`,
			).
			WithExec([]string{"npm", "install", "--package-lock-only"})

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"bun"}}`, out)
	})

	t.Run("should error if configured runtime is unknown", func(ctx context.Context, t *testctx.T) {
		modGen := modGen.WithNewFile("/work/package.json", `{
        "dagger": {
          "runtime": "xyz"
        }
      }`,
		)
		_, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.Error(t, err)
	})

	t.Run("should detect specific pinned node version 20.15.0", func(ctx context.Context, t *testctx.T) {
		modGen := modGen.WithNewFile("/work/package.json", `{
        "dagger": {
          "runtime": "node@20.15.0"
        }
      }`,
		)

		out, err := modGen.With(daggerQuery(`{runtimeDetection{version}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"version":"node@20.15.0"}}`, out)
	})

	t.Run("should detect a specific pinned node version 22.4.0", func(ctx context.Context, t *testctx.T) {
		modGen := modGen.WithNewFile("/work/package.json", `{
        "dagger": {
          "runtime": "node@22.4.0"
        }
      }`,
		)

		out, err := modGen.With(daggerQuery(`{runtimeDetection{version}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"version":"node@22.4.0"}}`, out)
	})

	t.Run("should detect a specific pinned bun version", func(ctx context.Context, t *testctx.T) {
		// We need to explicitly add the typescript version because the default bun's version is different.
		modGen := modGen.WithNewFile("/work/package.json", `{
        "dependencies": {
          "typescript": "^5.3.2",
          "@dagger.io/dagger": "./sdk"
        },
        "dagger": {
          "runtime": "bun@1.1.23"
        }
      }`,
		)

		out, err := modGen.With(daggerQuery(`{runtimeDetection{version}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"version":"bun@1.1.23"}}`, out)
	})

	t.Run("should detect deno.json", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/deno.json", `{}`).
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript", "--source=.")).
			With(sdkSource("typescript", `
			import { object, func } from "@dagger.io/dagger";

			@object()
			export class RuntimeDetection {
				@func()
				echoRuntime(): string {
					const isDenoRuntime = typeof Deno === "object";
					return isDenoRuntime ? "deno" : "node";
				}
			}
		`))

		out, err := modGen.With(daggerQuery(`{runtimeDetection{echoRuntime}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"echoRuntime":"deno"}}`, out)
	})

	t.Run("should detect specific pinned deno version", func(ctx context.Context, t *testctx.T) {
		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/deno.json", `{
			  "dagger": {
    			"baseImage": "denoland/deno:alpine-2.2.0@sha256:a58f2e1f8ba2681efd2425aada5da77a6edc6020da921332d20393efe24be431"
				}
			}`).
			With(daggerExec("init", "--name=Runtime-Detection", "--sdk=typescript", "--source=.")).
			With(sdkSource("typescript", `
			import { object, func } from "@dagger.io/dagger";

			@object()
			export class RuntimeDetection {
				@func()
				version(): string {
					const version = Deno.version.deno
					return "deno" + "@" + version
				}
			}
		`))

		out, err := modGen.With(daggerQuery(`{runtimeDetection{version}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"runtimeDetection":{"version":"deno@2.2.0"}}`, out)
	})
}

func (TypescriptSuite) TestCustomBaseImage(ctx context.Context, t *testctx.T) {
	script := `
    import { object, func } from "@dagger.io/dagger"

    @object()
    export class Test {
      @func()
      runtime(): string {
        const isBunRuntime = typeof Bun === "object";
        const runtime = isBunRuntime ? "bun" : "node";

        switch (runtime) {
          case "bun":
            return runtime + "@" + Bun.version
          case "node":
            return runtime + "@" + process.versions.node
        }
      }
    }
      `

	t.Run("should use custom base image if base image is set - bun", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("package.json", `{
      "dagger": {
        "baseImage": "oven/bun:1.2.4-alpine@sha256:66169513f6c6c653b207a4f198695a3a9750ed0ae7b1088d4a8fc09a3a0d41dc",
        "runtime": "bun"
      }
    }`).
			With(sdkSource("typescript", script)).
			With(daggerExec("init", "--name=test", "--sdk=typescript", "--source=."))

		out, err := modGen.With(daggerCall("runtime")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "bun@1.2.4", out)
	})

	t.Run("should use custom base image if base image is set - node", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("package.json", `{
      "dagger": {
        "baseImage": "node:22.10.0-alpine@sha256:fc95a044b87e95507c60c1f8c829e5d98ddf46401034932499db370c494ef0ff",
        "runtime": "node"
      }
    }`).
			With(sdkSource("typescript", script)).
			With(daggerExec("init", "--name=test", "--sdk=typescript", "--source=."))

		out, err := modGen.With(daggerCall("runtime")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "node@22.10.0", out)
	})
}

func (TypescriptSuite) TestPackageManagerDetection(ctx context.Context, t *testctx.T) {
	t.Run("should default to yarn", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Package-Detection", "--sdk=typescript", "--source=."))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, files, "yarn.lock")

		// Check that the package manager is set to yarn.
		packageManager, err := modGen.File("package.json").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, packageManager, `"packageManager": "yarn@1.22.22`)

		// Verify that it executes dagger example properly.
		out, err := modGen.With(daggerQuery(`{packageDetection{containerEcho(stringArg:"hello"){stdout}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"packageDetection":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("should use pnpm if set in package.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Package-Detection", "--sdk=typescript", "--source=.")).
			WithoutFile("yarn.lock").
			WithoutFile("package.json").
			WithNewFile("package.json", `
{
  "dependencies": {
    "typescript": "^5.3.2",
    "@dagger.io/dagger": "./sdk"
  },
  "packageManager": "pnpm@8.15.4"
}`).
			With(daggerExec("develop"))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, files, "pnpm-lock.yaml")

		// Verify that it executes dagger example properly.
		out, err := modGen.With(daggerQuery(`{packageDetection{containerEcho(stringArg:"hello"){stdout}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"packageDetection":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("should use npm if set in package.json", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Package-Detection", "--sdk=typescript", "--source=.")).
			WithoutFile("yarn.lock").
			WithoutFile("package.json").
			WithNewFile("package.json", `
{
  "dependencies": {
    "typescript": "^5.3.2",
    "@dagger.io/dagger": "./sdk"
  },
  "packageManager": "npm@10.7.0"
}`).
			With(daggerExec("develop"))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, files, "package-lock.json")

		// Verify that it executes dagger example properly.
		out, err := modGen.With(daggerQuery(`{packageDetection{containerEcho(stringArg:"hello"){stdout}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"packageDetection":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("should use npm if package-lock.json is present", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Package-Detection", "--sdk=typescript", "--source=.")).
			WithoutFile("yarn.lock").
			WithoutFile("package.json").
			WithNewFile("package.json", `
{
  "dependencies": {
    "typescript": "^5.3.2",
    "@dagger.io/dagger": "./sdk"
  }
}`).
			WithExec([]string{"npm", "install", "--package-lock-only"}).
			With(daggerExec("develop"))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, files, "package-lock.json")

		// Verify that it executes dagger example properly.
		out, err := modGen.With(daggerQuery(`{packageDetection{containerEcho(stringArg:"hello"){stdout}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"packageDetection":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("should use pnpm if pnpm-lock.yaml is present", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)
		modGen := c.Container().From("node:20-alpine").
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=Package-Detection", "--sdk=typescript", "--source=.")).
			WithoutFile("yarn.lock").
			WithoutFile("package.json").
			WithNewFile("package.json", `
{
  "dependencies": {
    "typescript": "^5.3.2",
    "@dagger.io/dagger": "./sdk"
  }
}`).
			WithExec([]string{"npm", "install", "-g", "pnpm@9.5.0"}).
			WithExec([]string{"pnpm", "install", "-lockfile-only"}).
			With(daggerExec("develop"))

		files, err := modGen.Directory(".").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, files, "pnpm-lock.yaml")

		// Verify that it executes dagger example properly.
		out, err := modGen.With(daggerQuery(`{packageDetection{containerEcho(stringArg:"hello"){stdout}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"packageDetection":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})
}

func (TypescriptSuite) TestWithOtherModuleTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=typescript")).
		With(sdkSource("typescript", `
  import {  object, func } from "@dagger.io/dagger"

@object()
export class Dep {
  @func()
  fn(): Obj {
    return new Obj("foo")
  }
}

@object()
export class Obj {
  @func()
  foo: string = ""

  constructor(foo: string) {
    this.foo = foo
  }
}

@object()
export class Foo {}
`)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=typescript", "test")).
		With(daggerExec("install", "-m=test", "./dep")).
		WithWorkdir("/work/test")

	t.Run("return as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(sdkSource("typescript", `
      import { object, func, DepObj } from "@dagger.io/dagger"

      @object()
      export class Test {
        @func()
        fn(): DepObj {
         return new DepObj()
        }
      }
      `)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrRegexp(t, err, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+cannot\s+return\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(sdkSource("typescript", `
      import { object, func, DepObj } from "@dagger.io/dagger"

      @object()
      export class Test {
        @func()
        fn(): DepObj[] {
         return [new DepObj()]
        }
      }
      `)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrRegexp(t, err, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+cannot\s+return\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "dep",
			))
		})
	})

	t.Run("arg as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.With(sdkSource("typescript", `
import { object, func, DepObj } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  fn(obj: DepObj): void {}
}
      `)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrRegexp(t, err, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+arg\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "obj", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				With(sdkSource("typescript", `
import { object, func, DepObj } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  fn(obj: DepObj[]): void {}
}
      `)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrRegexp(t, err, fmt.Sprintf(
				`object\s+%q\s+function\s+%q\s+arg\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Test", "fn", "obj", "dep",
			))
		})
	})

	t.Run("field as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				With(sdkSource("typescript", `
import { object, func, DepObj } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  fn(): Obj {
    return new Obj()
  }
}

@object()
export class Obj {
  @func()
  foo: DepObj
}
      `)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrRegexp(t, err, fmt.Sprintf(
				`object\s+%q\s+field\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Obj", "foo", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				With(sdkSource("typescript", `
import { object, func, DepObj } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  fn(): Obj {
    return new Obj()
  }
}

@object()
export class Obj {
  @func()
  foo: DepObj[]
}
      `)).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrRegexp(t, err, fmt.Sprintf(
				`object\s+%q\s+field\s+%q\s+cannot\s+reference\s+external\s+type\s+from\s+dependency\s+module\s+%q`,
				"Obj", "foo", "dep",
			))
		})
	})
}

func (TypescriptSuite) TestAliases(ctx context.Context, t *testctx.T) {
	t.Run("alias in function", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=alias", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { object, func } from "@dagger.io/dagger"

@object()
export class Alias {
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

	t.Run("nested alias in function", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=alias", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { object, func } from "@dagger.io/dagger"

@object()
export class SubSub {
	@func({ alias: "zoo" })
  subSubHello(): string {
    return "hello world"
  }
}

@object()
export class Sub {
	@func({ alias: "hello" })
  subHello(): SubSub {
    return new SubSub()
  }
}

@object()
export class Alias {
	@func({ alias: "bar" })
  foo(): Sub {
  return new Sub()
  }
}
`))

		out, err := modGen.With(daggerQuery(`{alias{bar{hello{zoo}}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"alias": {"bar": {"hello": {"zoo": "hello world"}}}}`, out)
	})

	t.Run("nested alias in field", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=alias", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { object, func, func } from "@dagger.io/dagger"

@object()
export class SuperSubSub {
  @func("farFarNested")
  far = true
}

@object()
export class SubSub {
  @func("zoo")
  a = 4

  @func("hey")
  b = [true, false, true]

  @func("far")
  subsubsub = new SuperSubSub()
}

@object()
export class Sub {
  @func("hello")
  hey = "a"

  @func("foo")
  sub = new SubSub()
}

@object()
export class Alias {
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

func (TypescriptSuite) TestPrototype(ctx context.Context, t *testctx.T) {
	t.Run("keep class prototype inside module", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  test() {
    return new PModule(new PCheck(4))
  }
}

@object()
export class PCheck {
  @func()
  value: number

  constructor(value: number) {
    this.value = value
  }

  get doubled() {
    return this.value * 2
  }
}

@object()
export class PModule {
  @func()
  value: PCheck

  constructor(value: PCheck) {
    this.value = value
  }
  @func()
  print() {
    return this.value.doubled ?? 0
  }
}
`))

		out, err := modGen.With(daggerQuery(`{test{test{print}}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test": {"test": {"print": 8 }}}`, out)
	})
}

func (TypescriptSuite) TestModuleSubPathLoading(ctx context.Context, t *testctx.T) {
	t.Run("load from subpath", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/sub").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			WithWorkdir("/work")

		_, err := modGen.With(daggerQuery(`{host{directory(path: "."){asModule(sourceRootPath: "./sub"){id}}}}`)).Stdout(ctx)
		require.NoError(t, err)
	})
}

func (TypescriptSuite) TestPrimitiveType(ctx context.Context, t *testctx.T) {
	t.Run("should throw error on String", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  str(s: String): String {
    return s
  }
}
`))

		_, err := modGen.With(daggerQuery(`{test{str("hello")}}`)).Stdout(ctx)
		requireErrOut(t, err, "Use of primitive 'String' type detected, please use 'string' instead.")
	})

	t.Run("should throw error on Number", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  integer(n: Number): Number {
    return n
  }
}
`))

		_, err := modGen.With(daggerQuery(`{test{integer(4)}}`)).Stdout(ctx)
		requireErrOut(t, err, "Use of primitive 'Number' type detected, please use 'number' instead.")
	})

	t.Run("should throw error on Boolean", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  bool(b: Boolean): Boolean {
    return b
  }
}
`))

		_, err := modGen.With(daggerQuery(`{test{bool(false)}}`)).Stdout(ctx)
		requireErrOut(t, err, "Use of primitive 'Boolean' type detected, please use 'boolean' instead.")
	})
}

func (TypescriptSuite) TestNativeEnumType(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=typescript")).
		With(sdkSource("typescript", `
import { object, func } from "@dagger.io/dagger"

/**
 * Test Enum
 */
export enum TestEnum {
    /**
     * A
     */
    A = "a",

    /**
     * B
     */
    B = "b",
}

@object()
export class Test {
  @func()
  testEnum(test: TestEnum = TestEnum.A): TestEnum {
    return test
  }
}
  `))

	t.Run("native enum type - doc", func(ctx context.Context, t *testctx.T) {
		schema := inspectModule(ctx, t, modGen)

		require.Equal(t, "Test Enum", schema.Get("enums.#.asEnum|#(name=TestEnum).description").String())
		require.Equal(t, "A", schema.Get("enums.#.asEnum|#(name=TestEnum).members.#(name=A).description").String())
		require.Equal(t, "B", schema.Get("enums.#.asEnum|#(name=TestEnum).members.#(name=B).description").String())
	})

	t.Run("native enum type - correct input / output", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("test-enum", "--test", "b")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, `B`, out)
	})

	t.Run("native enum type - default value by reference", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("test-enum")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, `A`, out)
	})
}

func (TypescriptSuite) TestReferencedDefaultValue(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--sdk=typescript")).
		With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

export const stringDefaultValue = "world"
export const integerDefaultValue = 4
export const booleanDefaultValue = true

@object()
export class Test {
  @func()
  str(s: string = stringDefaultValue): string {
    return s
  }

  @func()
  integer(n: number = integerDefaultValue): number {
    return n
  }

  @func()
  bool(b: boolean = booleanDefaultValue): boolean {
    return b
  }
}
  `))

	t.Run("check default value in doc", func(ctx context.Context, t *testctx.T) {
		schema := inspectModule(ctx, t, modGen)

		require.Equal(t, "\"world\"", schema.Get("objects.#.asObject|#(name=Test).functions.#(name=str).args.#(name=s).defaultValue").String())
		require.Equal(t, "4", schema.Get("objects.#.asObject|#(name=Test).functions.#(name=integer).args.#(name=n).defaultValue").String())
		require.Equal(t, "true", schema.Get("objects.#.asObject|#(name=Test).functions.#(name=bool).args.#(name=b).defaultValue").String())
	})

	t.Run("string variable default value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("str")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, `world`, out)
	})

	t.Run("integer variable default value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("integer")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, `4`, out)
	})

	t.Run("boolean variable default value", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerCall("bool")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, `true`, out)
	})
}

func (TypescriptSuite) TestTypeKeyword(ctx context.Context, t *testctx.T) {
	t.Run("wrap primitive type", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

export type Text = string
export type Integer = number
export type Online = boolean

@object()
export class Test {
  @func()
  str(s: Text): Text {
    return s
  }

  @func()
  integer(n: Integer): Integer {
    return n
  }

  @func()
  bool(arg: Online): Online {
    return arg
  }

  @func()
  defaultStr(s: Text = "hello"): Text {
    return s
  }

  @func()
  defaultInteger(n: Integer = 4): Integer {
    return n
  }

  @func()
  defaultBool(arg: Online = true): Online {
    return arg
  }
}
    `))

		t.Run("string", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("str", "--s", "world")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, `world`, out)
		})

		t.Run("integer ", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("integer", "--n", "4")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, `4`, out)
		})

		t.Run("boolean", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("bool", "--arg=true")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, `true`, out)
		})

		t.Run("string default value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("default-str")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, `hello`, out)
		})

		t.Run("integer default value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("default-integer")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, `4`, out)
		})

		t.Run("boolean default value", func(ctx context.Context, t *testctx.T) {
			out, err := modGen.With(daggerCall("default-bool")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, `true`, out)
		})
	})

	t.Run("object type definition", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

/**
 * Test Person
 */
export type Person = {
  /**
   * Age
   */
  age: number

  /**
   * Name
   */
  name: string
}

@object()
export class Test {
  @func()
  person(age: number, name: string): Person {
    return { age, name }
  }
}`))

		t.Run("type keyword - doc", func(ctx context.Context, t *testctx.T) {
			schema := inspectModule(ctx, t, modGen)

			require.Equal(t, "Test Person", schema.Get("objects.#.asObject|#(name=TestPerson).description").String())
			require.Equal(t, "Age", schema.Get("objects.#.asObject|#(name=TestPerson).fields.#(name=age).description").String())
			require.Equal(t, "Name", schema.Get("objects.#.asObject|#(name=TestPerson).fields.#(name=name).description").String())
		})

		out, err := modGen.With(daggerCall("person", "--age", "42", "--name", "John")).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, `TestPerson@xxh3:[a-f0-9]{16}`, out)

		out, err = modGen.With(daggerCall("person", "--age", "42", "--name", "John", "age")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, `42`, out)

		out, err = modGen.With(daggerCall("person", "--age", "42", "--name", "John", "name")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, `John`, out)
	})

	t.Run("nested object type definition", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

export type Organisation = {
  name: string
  members: Person[]
}

export type Person = {
  age: number
  name: string
}

@object()
export class Test {
  _orgs: Organisation[]

  constructor() {
    this._orgs = [
      {
        name: "dagger",
        members: [
          {
            age: 42,
            name: "John"
          },
          {
            age: 24,
            name: "Jane"
          }
        ]
      },
      {
        name: "GitHub",
        members: [
          {
            age: 42,
            name: "John"
          },
          {
            age: 24,
            name: "Jane"
          }
        ]
      }
    ]
  }

  @func()
  orgs(): Organisation[] {
    return this._orgs
  }

  @func()
  orgByName(name: string): Organisation {
    return this._orgs.find(org => org.name === name)
  }
}`))

		out, err := modGen.With(daggerCall("orgs")).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, strings.Repeat(`- TestOrganisation@xxh3:[a-f0-9]{16}\n`, 2), out)

		out, err = modGen.With(daggerCall("org-by-name", "--name", "GitHub", "members")).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, strings.Repeat(`- TestPerson@xxh3:[a-f0-9]{16}\n`, 2), out)
	})

	t.Run("nested IDable object type definition", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { dag, Directory, func, object } from "@dagger.io/dagger";

export type FileSystem = {
  name: string;
  Dirs: Folder[];
};

export type Folder = {
  name: string
  content: Directory
}

@object()
export class Test {
  _fs: FileSystem;

  constructor() {
    this._fs =
      {
        name: "school",
        Dirs: [
          { name: "math", content: dag.directory().withNewFile("math.txt", "hello world") },
          { name: "english", content: dag.directory().withNewFile("english.txt", "hello world") },
        ],
      }
  }

  @func()
  getDirs(): Folder[] {
    return this._fs.Dirs;
  }

  @func()
  getDirByName(name: string): Directory {
    return this._fs.Dirs.find(dir => dir.name === name).content;
  }
}`))

		out, err := modGen.With(daggerCall("get-dirs")).Stdout(ctx)
		require.NoError(t, err)
		require.Regexp(t, strings.Repeat(`- TestFolder@xxh3:[a-f0-9]{16}\n`, 2), out)

		out, err = modGen.With(daggerCall("get-dir-by-name", "--name", "math", "entries")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "math.txt\n", out)
	})
}

func (TypescriptSuite) TestDeprecatedFieldDecorator(ctx context.Context, t *testctx.T) {
	t.Run("@field still working", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { field, object } from "@dagger.io/dagger"

@object()
export class Test {
  @field()
  foo: string = "bar"

  constructor() {}
}
`,
			))

		out, err := modGen.With(daggerQuery(`{test{foo}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test": {"foo": "bar"}}`, out)
	})
}

func (TypescriptSuite) TestNonExportedFunctionBackwardsCompatibility(ctx context.Context, t *testctx.T) {
	t.Run("non-exported function", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

@object()
class Test {
  @func()
  foo(): string {
    return "bar"
  }
}
`,
			))

		out, err := modGen.With(daggerQuery(`{test{foo}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"test": {"foo": "bar"}}`, out)
	})
}

func (TypescriptSuite) TestInterface(ctx context.Context, t *testctx.T) {
	t.Run("doc", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript")).
			With(sdkSource("typescript", `
import { func, object } from "@dagger.io/dagger"

/**
 * A simple Duck interface
 */
export interface Duck {
  /**
   * A small quack sound
    */
  quack: () => string

  /**
   * A super quack sound
   */
  superQuack(): Promise<string>
}

@object()
export class Test {
  @func()
  duckQuack(duck: Duck): string {
    return duck.quack()
  }

  @func()
  async duckSuperQuack(duck: Duck): Promise<string> {
    return await duck.superQuack()
  }
}`))

		schema := inspectModule(ctx, t, modGen)

		require.Equal(t, "A simple Duck interface", schema.Get("interfaces.#.asInterface|#(name=TestDuck).description").String())
		require.Equal(t, "A small quack sound", schema.Get("interfaces.#.asInterface|#(name=TestDuck).functions.#(name=quack).description").String())
		require.Equal(t, "A super quack sound", schema.Get("interfaces.#.asInterface|#(name=TestDuck).functions.#(name=superQuack).description").String())
	})
}

func (TypescriptSuite) TestFloatReturnTypeSuggestion(ctx context.Context, t *testctx.T) {
	t.Run("suggest to use float instead of number if function returns a float", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript", "--source=.")).
			With(sdkSource("typescript", `import { dag, object, func } from "@dagger.io/dagger"

@object()
export class Test {
  @func()
  test(): number {
    return 4.4
  }
}

		`))

		_, err := modGen.With(daggerCall("test")).Stdout(ctx)
		requireErrOut(t, err, "cannot return float '4.4' if return type is 'number' (integer), please use 'float' as return type instead")
	})
}

func (TypescriptSuite) TestBundleLocalMigration(ctx context.Context, t *testctx.T) {
	checkFileExistence := func(t *testctx.T, files []string, filesToCheck []string) {
		for _, file := range filesToCheck {
			require.Contains(t, files, file)
		}
	}

	t.Run("default to bundle", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=test", "--sdk=typescript", "--source=."))

		files, err := modGen.Directory("/work/sdk").Entries(ctx)
		require.NoError(t, err)
		checkFileExistence(t, files, []string{"index.ts", "core.js", "core.d.ts", "telemetry.ts", "client.gen.ts"})

		out, err := modGen.With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "hello\n", out)
	})

	t.Run("migrate to bundle", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/package.json", `{
  "type": "module",
  "dependencies": {
    "typescript": "^5.3.2",
    "@dagger.io/dagger": "./sdk"
  }
}`).
			With(daggerExec("init", "--name=test", "--sdk=typescript", "--source=."))

		files, err := modGen.Directory("/work/sdk").Entries(ctx)
		require.NoError(t, err)
		checkFileExistence(t, files, []string{"package.json", "src/", "tsconfig.json"})

		t.Run("with clean sdk directory", func(ctx context.Context, t *testctx.T) {
			cleanModGen := modGen.
				WithNewFile("/work/package.json", `{
  "type": "module",
  "dependencies": {
    "typescript": "^5.3.2"
  }
}`).
				WithoutDirectory("/work/sdk").
				With(daggerExec("develop"))

			files, err := cleanModGen.Directory("/work/sdk").Entries(ctx)
			require.NoError(t, err)
			checkFileExistence(t, files, []string{"index.ts", "core.js", "core.d.ts", "telemetry.ts", "client.gen.ts"})

			out, err := cleanModGen.With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello\n", out)
		})

		t.Run("with non-clean sdk directory", func(ctx context.Context, t *testctx.T) {
			nonCleanModGen := modGen.
				WithNewFile("/work/package.json", `{
  "type": "module",
  "dependencies": {
    "typescript": "^5.3.2"
  }
}`).
				With(daggerExec("develop"))

			files, err := nonCleanModGen.Directory("/work/sdk").Entries(ctx)
			require.NoError(t, err)
			checkFileExistence(t, files, []string{"index.ts", "core.js", "core.d.ts", "telemetry.ts", "client.gen.ts", "package.json", "src/", "tsconfig.json"})

			// It should still work even if the sdk directory isn't clean.
			out, err := nonCleanModGen.With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello\n", out)
		})
	})

	t.Run("migrate to local", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/package.json", `{
  "type": "module",
  "dependencies": {
    "typescript": "^5.3.2"
  }
}`).
			With(daggerExec("init", "--name=test", "--sdk=typescript", "--source=."))

		files, err := modGen.Directory("/work/sdk").Entries(ctx)
		require.NoError(t, err)
		checkFileExistence(t, files, []string{"index.ts", "core.js", "core.d.ts", "telemetry.ts", "client.gen.ts"})

		t.Run("with clean sdk directory", func(ctx context.Context, t *testctx.T) {
			cleanModGen := modGen.
				WithNewFile("/work/package.json", `{
  "type": "module",
  "dependencies": {
    "typescript": "^5.3.2",
    "@dagger.io/dagger": "./sdk"
  }
}`).
				WithoutDirectory("/work/sdk").
				With(daggerExec("develop"))

			files, err := cleanModGen.Directory("/work/sdk").Entries(ctx)
			require.NoError(t, err)
			checkFileExistence(t, files, []string{"package.json", "src/", "tsconfig.json"})

			out, err := cleanModGen.With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello\n", out)
		})

		t.Run("with non-clean sdk directory", func(ctx context.Context, t *testctx.T) {
			nonCleanModGen := modGen.
				WithNewFile("/work/package.json", `{
  "type": "module",
  "dependencies": {
    "typescript": "^5.3.2",
    "@dagger.io/dagger": "./sdk"
  }
}`).
				With(daggerExec("develop"))

			files, err := nonCleanModGen.Directory("/work/sdk").Entries(ctx)
			require.NoError(t, err)
			checkFileExistence(t, files, []string{"index.ts", "core.js", "core.d.ts", "telemetry.ts", "client.gen.ts", "package.json", "src/", "tsconfig.json"})

			// It should still work even if the sdk directory isn't clean.
			out, err := nonCleanModGen.With(daggerCall("container-echo", "--string-arg", "hello", "stdout")).Stdout(ctx)
			require.NoError(t, err)
			require.Equal(t, "hello\n", out)
		})
	})
}
