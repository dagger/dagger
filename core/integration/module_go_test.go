package core

import (
	"context"
	_ "embed"
	"fmt"
	"testing"

	"dagger.io/dagger"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type GoSuite struct{}

func TestGo(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(GoSuite{})
}

func (GoSuite) TestInit(ctx context.Context, t *testctx.T) {
	t.Run("from scratch", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("reserved go.mod name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=go", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{go{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"go":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("uses expected Go module name, camel-cases Dagger module name", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=My-Module", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{myModule{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"myModule":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		generated, err := modGen.File("go.mod").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, generated, "module dagger/my-module")
	})

	t.Run("creates go.mod beneath an existing go.mod if context is beneath it", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		// don't use .git under /work so the context ends up being /work/ci
		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/go.mod", "module example.com/test\n").
			WithNewFile("/work/foo.go", "package foo\n").
			WithWorkdir("/work/ci").
			With(daggerExec("init", "--name=beneathGoMod", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{beneathGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"beneathGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("names Go module after Dagger module", func(ctx context.Context, t *testctx.T) {
			generated, err := modGen.Directory(".").File("go.mod").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "module dagger/beneath-go-mod")
		})
	})

	t.Run("respects existing go.work", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "work", "init"}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go", "--source=."))

		out, err := modGen.
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("go.work is edited", func(ctx context.Context, t *testctx.T) {
			generated, err := modGen.File("go.work").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "use .\n")
		})
	})

	t.Run("respects go.work for subdir if git dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "work", "init"}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go", "--include", "../go.work*", "subdir"))

		out, err := modGen.
			WithWorkdir("./subdir").
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("go.work is edited", func(ctx context.Context, t *testctx.T) {
			generated, err := modGen.File("go.work").Contents(ctx)
			require.NoError(t, err)
			require.Contains(t, generated, "use ./subdir\n")
		})
	})

	t.Run("ignores go.work for subdir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "work", "init"}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go", "subdir"))

		// we can't write to the go.work at the top-level so it should remain
		// unedited, but we should still be able to execute the module as
		// expected

		out, err := modGen.
			WithWorkdir("./subdir").
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasGoMod":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		t.Run("go.work is unedited", func(ctx context.Context, t *testctx.T) {
			generated, err := modGen.File("go.work").Contents(ctx)
			require.NoError(t, err)
			require.NotContains(t, generated, "use")
		})
	})

	t.Run("respects existing main.go", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/main.go", `
					package main

					type HasMainGo struct {}

					func (m *HasMainGo) Hello() string { return "Hello, world!" }
				`,
			).
			With(daggerExec("init", "--name=hasMainGo", "--sdk=go", "--source=."))

		out, err := modGen.
			With(daggerQuery(`{hasMainGo{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasMainGo":{"hello":"Hello, world!"}}`, out)
	})

	t.Run("respects existing main.go even if it uses types not generated yet", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/main.go", `
					package main
					import (
						"dagger/has-dagger-types/internal/dagger"
					)

					type HasDaggerTypes struct {}

					func (m *HasDaggerTypes) Hello() *dagger.Container {
						return dag.Container().
							From("`+alpineImage+`").
							WithExec([]string{"echo", "Hello, world!"})
					}
				`,
			).
			With(daggerExec("init", "--source=.", "--name=hasDaggerTypes", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{hasDaggerTypes{hello{stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasDaggerTypes":{"hello":{"stdout":"Hello, world!\n"}}}`, out)
	})

	t.Run("respects existing package without creating main.go", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("/work/notmain.go", `package main

type HasNotMainGo struct {}

func (m *HasNotMainGo) Hello() string { return "Hello, world!" }
`,
			).
			With(daggerExec("init", "--source=.", "--name=hasNotMainGo", "--sdk=go"))

		out, err := modGen.
			With(daggerQuery(`{hasNotMainGo{hello}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"hasNotMainGo":{"hello":"Hello, world!"}}`, out)
	})

	t.Run("with source", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go", "--source=some/subdir"))

		out, err := modGen.
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		sourceSubdirEnts, err := modGen.Directory("/work/some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, sourceSubdirEnts, "main.go")

		sourceRootEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.NotContains(t, sourceRootEnts, "main.go")
	})

	t.Run("multiple modules in go.work", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := goGitBase(t, c).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "work", "init"}).
			With(daggerExec("init", "--sdk=go", "--include", "../go.work*,../bar/", "foo")).
			With(daggerExec("init", "--sdk=go", "--include", "../go.work*,../foo/", "bar"))

		generated, err := modGen.File("go.work").Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, generated, "\t./foo\n")
		require.Contains(t, generated, "\t./bar\n")

		out, err := modGen.
			WithWorkdir("./foo").
			With(daggerQuery(`{foo{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"foo":{"containerEcho":{"stdout":"hello\n"}}}`, out)

		out, err = modGen.
			WithWorkdir("./bar").
			With(daggerQuery(`{bar{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bar":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("init module in .dagger if files present in current dir", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("main.go", `package main

import "fmt"

func main() {
	fmt.Println("hello")
}
`).
			WithExec([]string{"go", "mod", "init", "my-app"}).
			With(daggerExec("init", "--name=bare", "--sdk=go"))

		daggerDirEnts, err := modGen.Directory("/work/.dagger").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerDirEnts, "go.mod", "go.sum", "main.go")

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
			WithExec([]string{"mkdir", "-p", ".foo"}).
			With(daggerExec("init", "--name=bare", "--sdk=go"))

		daggerDirEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerDirEnts, "go.mod", "go.sum", "main.go")

		out, err := modGen.
			WithWorkdir("/work").
			With(daggerQuery(`{bare{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"bare":{"containerEcho":{"stdout":"hello\n"}}}`, out)
	})

	t.Run("fails if go.mod exists", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			With(daggerExec("init", "--name=hasGoMod", "--sdk=go", "--source=."))

		_, err := modGen.
			With(daggerQuery(`{hasGoMod{containerEcho(stringArg:"hello"){stdout}}}`)).
			Stdout(ctx)
		require.Error(t, err)
		requireErrRegexp(t, err, `existing go.mod path ".*" does not match the module's name ".*"`)
	})

	t.Run("do not merge go.mod with parent", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"go", "mod", "init", "example.com/test"}).
			With(daggerExec("init", "--name=bare", "--sdk=go", "--source=some/subdir"))

		sourceSubdirEnts, err := modGen.Directory("/work/some/subdir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, sourceSubdirEnts, "go.mod", "go.sum")
	})

	t.Run("empty dir, init without sdk or source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init"))

		dirEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, dirEnts, []string{"dagger.json"})
	})

	t.Run("empty dir, init with sdk but no source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--sdk=go"))

		dirEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "dagger.json", "go.mod", "go.sum", "main.go")
	})

	t.Run("non-empty dir, init without sdk or source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		daggerjson, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("main.go", `package main\n func main() {}`).
			With(daggerExec("init")).
			File("dagger.json").
			Contents(ctx)

		require.NoError(t, err)
		require.NotContains(t, daggerjson, ".dagger")
	})

	t.Run("non-empty dir, init with sdk but no source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("main.go", `package main\n func main() {}`).
			With(daggerExec("init", "--sdk=go"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)

		require.NoError(t, err)
		require.Contains(t, daggerjson, ".dagger")

		dirEnts, err := modGen.Directory(".dagger").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "go.mod", "go.sum", "main.go", "internal")
	})

	t.Run("non-empty dir, init without sdk but source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("main.go", `package main\n func main() {}`).
			With(daggerExec("init", "--source=some-dir"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)

		require.NoError(t, err)
		require.Contains(t, daggerjson, "some-dir")
	})

	t.Run("non-empty dir, init with sdk and source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithNewFile("main.go", `package main\n func main() {}`).
			With(daggerExec("init", "--source=some-dir", "--sdk=go"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)

		require.NoError(t, err)
		require.Contains(t, daggerjson, "some-dir")

		dirEnts, err := modGen.Directory("some-dir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "go.mod", "go.sum", "main.go", "internal")
	})

	t.Run(".dagger dir exists, init without sdk or source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"sh", "-c", "mkdir -p .dagger"}).
			With(daggerExec("init")).
			With(daggerExec("develop", "--sdk=go"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)

		require.NoError(t, err)
		require.Contains(t, daggerjson, ".dagger")
	})

	t.Run(".dagger dir exists, init without sdk but source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"sh", "-c", "mkdir -p .dagger"}).
			With(daggerExec("init", "--source=some-dir"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)

		require.NoError(t, err)
		require.Contains(t, daggerjson, "some-dir")
	})

	t.Run(".dagger dir exists, init with sdk and source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			WithExec([]string{"sh", "-c", "mkdir -p .dagger"}).
			With(daggerExec("init", "--source=some-dir", "--sdk=go"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)

		require.NoError(t, err)
		require.Contains(t, daggerjson, "some-dir")

		dirEnts, err := modGen.Directory("some-dir").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "go.mod", "go.sum", "main.go", "internal")
	})

	t.Run("empty dir, init without sdk or source flag and then develop", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init")).
			With(daggerExec("develop", "--sdk=go"))

		dirEnts, err := modGen.Directory("/work").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "dagger.json", "go.mod", "go.sum", "main.go", "internal")
	})

	t.Run("empty dir, init without sdk or source flag, then make dir non-empty and then develop", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init")).
			WithNewFile("/work/main.go", `package main\n func main() {}`).
			With(daggerExec("develop", "--sdk=go"))

		dirEnts, err := modGen.Directory("/work/.dagger").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "go.mod", "go.sum", "main.go", "internal")
	})

	t.Run("init in subdir, without sdk or source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "foo"))

		dirEnts, err := modGen.Directory("/work/foo").Entries(ctx)
		require.NoError(t, err)
		require.Equal(t, dirEnts, []string{"dagger.json"})
	})

	t.Run("init in subdir, with sdk but no source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "foo", "--sdk=go"))

		dirEnts, err := modGen.Directory("/work/foo").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "go.mod", "go.sum", "main.go", "internal", "dagger.json")
	})

	t.Run("init in subdir, with sdk and source flag", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "foo", "--sdk=go", "--source=foo/bar"))

		dirEnts, err := modGen.Directory("/work/foo/bar").Entries(ctx)
		require.NoError(t, err)
		require.Contains(t, dirEnts, "go.mod", "go.sum", "main.go", "internal", "dagger.json")

		daggerjson, err := modGen.Directory("foo").File("dagger.json").
			Contents(ctx)

		require.NoError(t, err)
		require.Contains(t, daggerjson, "bar")
	})

	t.Run("from scratch with self calls", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go", "--with-self-calls"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "\"SELF_CALLS\": true")
	})

	t.Run("enable self calls", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.NotContains(t, daggerjson, "SELF_CALLS")

		modGen = modGen.With(daggerExec("develop", "--with-self-calls"))

		daggerjson, err = modGen.File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "\"SELF_CALLS\": true")
	})

	t.Run("disable self calls", func(ctx context.Context, t *testctx.T) {
		c := connect(ctx, t)

		modGen := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--name=bare", "--sdk=go", "--with-self-calls"))

		daggerjson, err := modGen.File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "\"SELF_CALLS\": true")

		modGen = modGen.With(daggerExec("develop", "--without-self-calls"))

		daggerjson, err = modGen.File("dagger.json").
			Contents(ctx)
		require.NoError(t, err)
		require.Contains(t, daggerjson, "\"SELF_CALLS\": false")
	})
}

//go:embed testdata/modules/go/minimal/main.go
var goSignatures string

func (GoSuite) TestSignatures(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", goSignatures)

	t.Run("func Hello() string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"hello":"hello"}}`, out)
	})

	t.Run("func Echo(string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echo(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echo":"hello...hello...hello..."}}`, out)
	})

	t.Run("func EchoPointer(*string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoPointer(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoPointer":"hello...hello...hello..."}}`, out)
	})

	t.Run("func EchoPointerPointer(**string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoPointerPointer(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoPointerPointer":"hello...hello...hello..."}}`, out)
	})

	t.Run("func EchoOptional(string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptional(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"hello...hello...hello..."}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{echoOptional}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptional":"default...default...default..."}}`, out)
	})

	t.Run("func EchoOptionalPointer(string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptionalPointer(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalPointer":"hello...hello...hello..."}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{echoOptionalPointer}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalPointer":"default...default...default..."}}`, out)
	})

	t.Run("func EchoOptionalSlice([]string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptionalSlice(msg: ["hello", "there"])}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"hello+there...hello+there...hello+there..."}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{echoOptionalSlice}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptionalSlice":"foobar...foobar...foobar..."}}`, out)
	})

	t.Run("func Echoes([]string) []string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoes(msgs: ["hello"])}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoes":["hello...hello...hello..."]}}`, out)
	})

	t.Run("func EchoesVariadic(...string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoesVariadic(msgs: ["hello"])}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoesVariadic":"hello...hello...hello..."}}`, out)
	})

	t.Run("func HelloContext(context.Context) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{helloContext}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloContext":"hello context"}}`, out)
	})

	t.Run("func EchoContext(context.Context, string) string", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoContext(msg: "hello")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoContext":"ctx.hello...ctx.hello...ctx.hello..."}}`, out)
	})

	t.Run("func HelloStringError() (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{helloStringError}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloStringError":"hello i worked"}}`, out)
	})

	t.Run("func HelloVoid()", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{helloVoid}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoid":null}}`, out)
	})

	t.Run("func HelloVoidError() error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{helloVoidError}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"helloVoidError":null}}`, out)
	})

	t.Run("func EchoOpts(string, string, int) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOpts(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOpts":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInline(struct{string, string, int}) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInline(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInline":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInline(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInline":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInlinePointer(*struct{string, string, int}) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInlinePointer(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlinePointer":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInlinePointer(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlinePointer":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInlineCtx(ctx, struct{string, string, int}) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInlineCtx(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlineCtx":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInlineCtx(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlineCtx":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsInlineTags(struct{string, string, int}) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptsInlineTags(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlineTags":"hi"}}`, out)

		out, err = modGen.With(daggerQuery(`{minimal{echoOptsInlineTags(msg: "hi", suffix: "!", times: 2)}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsInlineTags":"hi!hi!"}}`, out)
	})

	t.Run("func EchoOptsPragmas(string, string, int) error", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(`{minimal{echoOptsPragmas(msg: "hi")}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"echoOptsPragmas":"hi...hi...hi..."}}`, out)
	})
}

func (GoSuite) TestSignaturesBuiltinTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"context"
	"dagger/minimal/internal/dagger"
)

type Minimal struct {}

func (m *Minimal) Read(ctx context.Context, dir dagger.Directory) (string, error) {
	return dir.File("foo").Contents(ctx)
}

func (m *Minimal) ReadPointer(ctx context.Context, dir *dagger.Directory) (string, error) {
	return dir.File("foo").Contents(ctx)
}

func (m *Minimal) ReadSlice(ctx context.Context, dir []dagger.Directory) (string, error) {
	return dir[0].File("foo").Contents(ctx)
}

func (m *Minimal) ReadVariadic(ctx context.Context, dir ...dagger.Directory) (string, error) {
	return dir[0].File("foo").Contents(ctx)
}

func (m *Minimal) ReadOptional(
	ctx context.Context,
	dir *dagger.Directory, // +optional
) (string, error) {
	if dir != nil {
		return dir.File("foo").Contents(ctx)
	}
	return "", nil
}
			`,
		)

	out, err := modGen.With(daggerQuery(`{directory{withNewFile(path: "foo", contents: "bar"){id}}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.withNewFile.id").String()

	t.Run("func Read(ctx, Directory) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{read(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"read":"bar"}}`, out)
	})

	t.Run("func ReadPointer(ctx, *dagger.Directory) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readPointer(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readPointer":"bar"}}`, out)
	})

	t.Run("func ReadSlice(ctx, []Directory) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readSlice(dir: ["%s"])}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readSlice":"bar"}}`, out)
	})

	t.Run("func ReadVariadic(ctx, ...Directory) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readVariadic(dir: ["%s"])}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readVariadic":"bar"}}`, out)
	})

	t.Run("func ReadOptional(ctx, Optional[Directory]) (string, error)", func(ctx context.Context, t *testctx.T) {
		out, err := modGen.With(daggerQuery(fmt.Sprintf(`{minimal{readOptional(dir: "%s")}}`, dirID))).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readOptional":"bar"}}`, out)
		out, err = modGen.With(daggerQuery(`{minimal{readOptional}}`)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, `{"minimal":{"readOptional":""}}`, out)
	})
}

func (GoSuite) TestSignaturesUnexported(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

type Minimal struct {}

type Foo struct {}

type bar struct {}

func (m *Minimal) Hello(name string) string {
	return name
}

func (f *Foo) Hello(name string) string {
	return name
}

func (b *bar) Hello(name string) string {
	return name
}
`,
		)

	objs := inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 1, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())

	modGen = c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

type Minimal struct {}

type Foo struct {}

type bar struct {}

func (m *Minimal) Hello(name string) Foo {
	return Foo{}
}

func (f *Foo) Hello(name string) string {
	return name
}

func (b *bar) Hello(name string) string {
	return name
}
`,
		)

	objs = inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 2, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())
	require.Equal(t, "MinimalFoo", objs.Get("1.name").String())

	modGen = c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

type Minimal struct {}

type Foo struct {
	Bar bar
}

type bar struct {}

func (m *Minimal) Hello(name string) Foo {
	return Foo{}
}

func (f *Foo) Hello(name string) string {
	return name
}

func (b *bar) Hello(name string) string {
	return name
}
`,
		)

	_, err := modGen.With(moduleIntrospection).Stderr(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	require.Regexp(t, "cannot code-generate unexported type bar", logs.String())
}

func (GoSuite) TestSignaturesMixMatch(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

type Minimal struct {}

func (m *Minimal) Hello(name string, opts struct{}, opts2 struct{}) string {
	return name
}
`,
		)

	_, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	require.Regexp(t, "nested structs are not supported", logs.String())
}

func (GoSuite) TestSignaturesNameConflict(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

type Minimal struct {
	Foo Foo
	Bar Bar
	Baz Baz
}

type Foo struct {}
type Bar struct {}
type Baz struct {}

func (m *Foo) Hello(name string) string {
	return name
}

func (f *Bar) Hello(name string, name2 string) string {
	return name + name2
}

func (b *Baz) Hello() (string, error) {
	return "", nil
}
`,
		)

	objs := inspectModuleObjects(ctx, t, modGen)
	require.Equal(t, 4, len(objs.Array()))
	require.Equal(t, "Minimal", objs.Get("0.name").String())
	require.Equal(t, "MinimalFoo", objs.Get("1.name").String())
	require.Equal(t, "MinimalBar", objs.Get("2.name").String())
	require.Equal(t, "MinimalBaz", objs.Get("3.name").String())
}

func (GoSuite) TestDocs(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", goSignatures)

	logGen(ctx, t, modGen.Directory("."))

	obj := inspectModuleObjects(ctx, t, modGen).Get("0")
	require.Equal(t, "Minimal", obj.Get("name").String())

	hello := obj.Get(`functions.#(name="hello")`)
	require.Equal(t, "hello", hello.Get("name").String())
	require.Empty(t, hello.Get("description").String())
	require.Empty(t, hello.Get("args").Array())

	// test the args-based form
	echoOpts := obj.Get(`functions.#(name="echoOpts")`)
	require.Equal(t, "echoOpts", echoOpts.Get("name").String())
	require.Equal(t, "EchoOpts does some opts things", echoOpts.Get("description").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "the message to echo", echoOpts.Get("args.0.description").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "Number of times to repeat the message", echoOpts.Get("args.2.description").String())

	// test the inline struct form
	echoOpts = obj.Get(`functions.#(name="echoOptsInline")`)
	require.Equal(t, "echoOptsInline", echoOpts.Get("name").String())
	require.Equal(t, "EchoOptsInline does some opts things", echoOpts.Get("description").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "the message to echo", echoOpts.Get("args.0.description").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "Number of times to repeat the message", echoOpts.Get("args.2.description").String())

	// test the arg-based form (with pragmas)
	echoOpts = obj.Get(`functions.#(name="echoOptsPragmas")`)
	require.Equal(t, "echoOptsPragmas", echoOpts.Get("name").String())
	require.Len(t, echoOpts.Get("args").Array(), 3)
	require.Equal(t, "msg", echoOpts.Get("args.0.name").String())
	require.Equal(t, "", echoOpts.Get("args.0.defaultValue").String())
	require.Equal(t, "suffix", echoOpts.Get("args.1.name").String())
	require.Equal(t, "String to append to the echoed message", echoOpts.Get("args.1.description").String())
	require.Equal(t, "\"...\"", echoOpts.Get("args.1.defaultValue").String())
	require.Equal(t, "times", echoOpts.Get("args.2.name").String())
	require.Equal(t, "3", echoOpts.Get("args.2.defaultValue").String())
	require.Equal(t, "Number of times to repeat the message", echoOpts.Get("args.2.description").String())
}

func (GoSuite) TestDocsEdgeCases(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

// Minimal is a thing
type Minimal struct {
	// X is this
	X, Y string  // Y is not this

	// +private
	Z string
}

// some docs
func (m *Minimal) Hello(foo string, bar string,
// hello
baz string, qux string, x string, // lol
) string {
	return foo + bar
}

func (m *Minimal) HelloMore(
	// foo here
	foo,
	// bar here
	bar string,
) string {
	return foo + bar
}

func (m *Minimal) HelloMoreInline(opts struct{
	// foo here
	foo, bar string
}) string {
	return opts.foo + opts.bar
}

func (m *Minimal) HelloAgain( // docs for helloagain
	foo string,
	bar string, // docs for bar
	baz string,
) string {
	return foo + bar
}

func (m *Minimal) HelloFinal(
	foo string) string { // woops
	return foo
}
`,
		)

	logGen(ctx, t, modGen.Directory("."))

	obj := inspectModuleObjects(ctx, t, modGen).Get("0")
	require.Equal(t, "Minimal", obj.Get("name").String())
	require.Equal(t, "Minimal is a thing", obj.Get("description").String())

	hello := obj.Get(`functions.#(name="hello")`)
	require.Equal(t, "hello", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 5)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "", hello.Get("args.1.description").String())
	require.Equal(t, "baz", hello.Get("args.2.name").String())
	require.Equal(t, "hello", hello.Get("args.2.description").String())
	require.Equal(t, "qux", hello.Get("args.3.name").String())
	require.Equal(t, "", hello.Get("args.3.description").String())
	require.Equal(t, "x", hello.Get("args.4.name").String())
	require.Equal(t, "lol", hello.Get("args.4.description").String())

	hello = obj.Get(`functions.#(name="helloMore")`)
	require.Equal(t, "helloMore", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 2)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "foo here", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "bar here", hello.Get("args.1.description").String())

	hello = obj.Get(`functions.#(name="helloMoreInline")`)
	require.Equal(t, "helloMoreInline", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 2)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "foo here", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "", hello.Get("args.1.description").String())

	hello = obj.Get(`functions.#(name="helloAgain")`)
	require.Equal(t, "helloAgain", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 3)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())
	require.Equal(t, "bar", hello.Get("args.1.name").String())
	require.Equal(t, "docs for bar", hello.Get("args.1.description").String())
	require.Equal(t, "baz", hello.Get("args.2.name").String())
	require.Equal(t, "", hello.Get("args.2.description").String())

	hello = obj.Get(`functions.#(name="helloFinal")`)
	require.Equal(t, "helloFinal", hello.Get("name").String())
	require.Len(t, hello.Get("args").Array(), 1)
	require.Equal(t, "foo", hello.Get("args.0.name").String())
	require.Equal(t, "", hello.Get("args.0.description").String())

	require.Len(t, obj.Get(`fields`).Array(), 2)
	prop := obj.Get(`fields.#(name="x")`)
	require.Equal(t, "x", prop.Get("name").String())
	require.Equal(t, "X is this", prop.Get("description").String())
	prop = obj.Get(`fields.#(name="y")`)
	require.Equal(t, "y", prop.Get("name").String())
	require.Equal(t, "", prop.Get("description").String())
}

func (GoSuite) TestPragmaParsing(ctx context.Context, t *testctx.T) {
	// corner cases of pragma parsing

	c := connect(ctx, t)

	// corner cases where a +default pragma has a value that itself has a + in it
	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("main.go", `package main

type Test struct {}

func (t *Test) Hello(
	// +optional
	// +default="blah+dagger-ci@dagger.io"
	argWhereDefaultHasAPlusSign string,
) string {
	return argWhereDefaultHasAPlusSign
}
`,
		)

	out, err := modGen.With(daggerCall("hello")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, `blah+dagger-ci@dagger.io`, out)
}

func (GoSuite) TestWeirdFields(ctx context.Context, t *testctx.T) {
	// these are all cases that used to panic due to the disparity in the type spec and the ast

	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

type Z string

type Minimal struct {
	// field with single (normal) name
	W string

	// field with multiple names
	X, Y string

	// field with no names
	Z
}

func New() Minimal {
	return Minimal{
		W: "-",
		X: "-",
		Y: "-",
		Z: Z("-"),
	}
}

// struct with no fields
type Bar struct{}

func (m *Minimal) Say(
	// field with single (normal) name
	a string,
	// field with multiple names
	b, c string,
	// field with no names (not included, mixed names not allowed)
	// string
) string {
	return a + " " + b + " " + c
}

func (m *Minimal) Hello(
	// field with no names
	string,
) string {
	return "hello"
}

func (m *Minimal) SayOpts(opts struct{
	// field with single (normal) name
	A string
	// field with multiple names
	B, C string
	// field with no names (not included because of above)
	// string
}) string {
	return opts.A + " " + opts.B + " " + opts.C
}

func (m *Minimal) HelloOpts(opts struct{
	// field with no names
	string
}) string {
	return "hello"
}
`,
		)

	out, err := modGen.With(daggerQuery(`{minimal{w, x, y, z}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal": {"w": "-", "x": "-", "y": "-", "z": "-"}}`, out)

	for _, name := range []string{"say", "sayOpts"} {
		out, err := modGen.With(daggerQuery(`{minimal{%s(a: "hello", b: "world", c: "!")}}`, name)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"minimal": {"%s": "hello world !"}}`, name), out)
	}

	for _, name := range []string{"hello", "helloOpts"} {
		out, err := modGen.With(daggerQuery(`{minimal{%s(string: "")}}`, name)).Stdout(ctx)
		require.NoError(t, err)
		require.JSONEq(t, fmt.Sprintf(`{"minimal": {"%s": "hello"}}`, name), out)
	}
}

func (GoSuite) TestFieldMustBeNil(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"fmt"
	"dagger/minimal/internal/dagger"
)

type Minimal struct {
	Src *dagger.Directory
	Name *string
}

func New() *Minimal {
	return &Minimal{}
}

func (m *Minimal) IsEmpty() bool {
	if m.Name != nil {
		panic(fmt.Sprintf("name should be nil but is %v", m.Name))
	}
	if m.Src != nil {
		panic(fmt.Sprintf("src should be nil but is %v", m.Src))
	}
	return true
}
`,
		)

	out, err := modGen.With(daggerQuery(`{minimal{isEmpty}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal": {"isEmpty": true}}`, out)
}

func (GoSuite) TestPrivateEnumField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--sdk=go", "dep")).
		WithNewFile("dep/main.go", `package main

import (
	"context"
	"dagger/dep/internal/dagger"
)

type Dep struct {
	Opts []dagger.ContainerPublishOpts // +private
}

func New() *Dep {
	return &Dep{
		Opts: []dagger.ContainerPublishOpts{
			{PlatformVariants: []*dagger.Container{dag.Container().From("alpine")}},
		},
	}
}

func (m *Dep) Publish(ctx context.Context) (string, error) {
	// dry run a publish
	return "registry/repo:latest", nil
}
`,
		).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		With(daggerExec("install", "./dep")).
		WithNewFile("main.go", `package main

import (
	"context"
)

type Test struct {}

func (m Test) Publish(ctx context.Context) (string, error) {
	return dag.Dep().Publish(ctx)
}
`,
		)

	out, err := ctr.With(daggerQuery(`{test{publish}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test": {"publish": "registry/repo:latest"}}`, out)
}

func (GoSuite) TestJSONField(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"dagger/minimal/internal/dagger"
)

type Minimal struct {
	Config dagger.JSON
}

func New() *Minimal {
	return &Minimal{
		Config: "{\"a\":1}",
	}
}
`,
		)

	out, err := modGen.With(daggerQuery(`{minimal{config}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal":{"config":"{\"a\":1}"}}`, out)
}

// this is no longer allowed, but verify the Engine errors out
func (GoSuite) TestExtendCore(ctx context.Context, t *testctx.T) {
	moreContents := `package dagger

import (
	"context"
)

func (c *Container) Echo(ctx context.Context, msg string) (string, error) {
	return c.WithExec([]string{"echo", msg}).Stdout(ctx)
}
`

	t.Run("in different mod name", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))
		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
			WithoutFile("/work/.gitignore"). // Remove .gitignore so we can override files inside internal/dagger without ignoring them.
			WithNewFile("/work/internal/dagger/more.go", moreContents).
			With(daggerQuery(`{container{from(address:"` + alpineImage + `"){echo(msg:"echo!"){stdout}}}}`)).
			Sync(ctx)
		require.Error(t, err)
		require.NoError(t, c.Close())
		t.Log(logs.String())

		// With lazy module loading, the error is no longer thrown by the SDK but directly by the engine
		// when evaluating the query against the engine GQL schema.
		require.Contains(t, logs.String(), `Cannot query field \"echo\" on type \"Container\"`)
	})

	t.Run("in same mod name", func(ctx context.Context, t *testctx.T) {
		var logs safeBuffer
		c := connect(ctx, t, dagger.WithLogOutput(&logs))
		_, err := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work").
			With(daggerExec("init", "--source=.", "--name=container", "--sdk=go")).
			WithoutFile("/work/.gitignore"). // Remove .gitignore so we can override files inside internal/dagger without ignoring them.
			WithNewFile("/work/internal/dagger/more.go", moreContents).
			With(daggerQuery(`{container{from(address:"` + alpineImage + `"){echo(msg:"echo!"){stdout}}}}`)).
			Sync(ctx)
		require.Error(t, err)
		require.NoError(t, c.Close())
		t.Log(logs.String())
		// With lazy module loading, the error is no longer thrown by the SDK but directly by the engine
		// when evaluating the query against the engine GQL schema.
		require.Contains(t, logs.String(), `type "Container" is already defined by module "daggercore"`)
	})
}

func (GoSuite) TestBadCtx(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=foo", "--sdk=go")).
		WithNewFile("main.go", `package main

import "context"

type Foo struct {}

func (f *Foo) Echo(ctx context.Context, ctx2 context.Context) (string, error) {
	return "", nil
}
`,
		).
		With(daggerQuery(`{foo{echo}}`)).
		Sync(ctx)
	require.Error(t, err)
	require.NoError(t, c.Close())
	t.Log(logs.String())
	require.Regexp(t, "unexpected context type", logs.String())
}

func (GoSuite) TestWithOtherModuleTypes(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := goGitBase(t, c).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--source=.", "--name=dep", "--sdk=go")).
		WithNewFile("main.go", `package main

type Dep struct{}

type Obj struct {
	Foo string
}

func (m *Dep) Fn() Obj {
	return Obj{Foo: "foo"}
}
`,
		).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=test", "--name=test", "--sdk=go", "test")).
		With(daggerExec("install", "-m=test", "./dep")).
		WithWorkdir("/work/test")

	t.Run("return as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn() (*dagger.DepObj, error) {
	return nil, nil
}
`,
				).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q function %q cannot return external type from dependency module %q",
				"Test", "Fn", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn() ([]*dagger.DepObj, error) {
	return nil, nil
}
`,
				).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q function %q cannot return external type from dependency module %q",
				"Test", "Fn", "dep",
			))
		})
	})

	t.Run("arg as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn(obj *dagger.DepObj) error {
	return nil
}
`,
			).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "Fn", "obj", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Fn(obj []*dagger.DepObj) error {
	return nil
}
`,
			).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q function %q arg %q cannot reference external type from dependency module %q",
				"Test", "Fn", "obj", "dep",
			))
		})
	})

	t.Run("field as other module object", func(ctx context.Context, t *testctx.T) {
		t.Run("direct", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct{}

type Obj struct {
	Foo *dagger.DepObj
}

func (m *Test) Fn() (*Obj, error) {
	return nil, nil
}
`,
				).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q field %q cannot reference external type from dependency module %q",
				"Obj", "Foo", "dep",
			))
		})

		t.Run("list", func(ctx context.Context, t *testctx.T) {
			_, err := ctr.
				WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct{}

type Obj struct {
	Foo []*dagger.DepObj
}

func (m *Test) Fn() (*Obj, error) {
	return nil, nil
}
`,
				).
				With(daggerFunctions()).
				Stdout(ctx)
			require.Error(t, err)
			requireErrOut(t, err, fmt.Sprintf(
				"object %q field %q cannot reference external type from dependency module %q",
				"Obj", "Foo", "dep",
			))
		})
	})
}

func (GoSuite) TestUseDaggerTypesDirect(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

import "dagger/minimal/internal/dagger"

type Minimal struct{}

func (m *Minimal) Foo(dir *dagger.Directory) (*dagger.Directory) {
	return dir.WithNewFile("foo", "xxx")
}

func (m *Minimal) Bar(dir *dagger.Directory) (*dagger.Directory) {
	return dir.WithNewFile("bar", "yyy")
}

`,
		)

	out, err := modGen.With(daggerQuery(`{directory{id}}`)).Stdout(ctx)
	require.NoError(t, err)
	dirID := gjson.Get(out, "directory.id").String()

	out, err = modGen.With(daggerQuery(`{minimal{foo(dir: "%s"){file(path: "foo"){contents}}}}`, dirID)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal":{"foo":{"file":{"contents": "xxx"}}}}`, out)

	out, err = modGen.With(daggerQuery(`{minimal{bar(dir: "%s"){file(path: "bar"){contents}}}}`, dirID)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal":{"bar":{"file":{"contents": "yyy"}}}}`, out)
}

func (GoSuite) TestUtilsPkg(ctx context.Context, t *testctx.T) {
	var logs safeBuffer
	c := connect(ctx, t, dagger.WithLogOutput(&logs))

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=minimal", "--sdk=go")).
		WithNewFile("main.go", `package main

import (
	"context"
	"dagger/minimal/utils"
)

type Minimal struct{}

func (m *Minimal) Hello(ctx context.Context) (string, error) {
	return utils.Foo().File("foo").Contents(ctx)
}

`,
		).
		WithNewFile("utils/util.go", `package utils

import "dagger/minimal/internal/dagger"

func Foo() *dagger.Directory {
	return dagger.Connect().Directory().WithNewFile("/foo", "hello world")
}

`,
		)

	out, err := modGen.With(daggerQuery(`{minimal{hello}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"minimal":{"hello":"hello world"}}`, out)
}

func (GoSuite) TestNameCase(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	ctr = ctr.
		WithWorkdir("/toplevel/ssh").
		With(daggerExec("init", "--name=ssh", "--sdk=go", "--source=.")).
		WithNewFile("main.go", `package main

type Ssh struct {}

func (ssh *Ssh) SayHello() string {
        return "hello!"
}
`,
		)
	out, err := ctr.With(daggerQuery(`{ssh{sayHello}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"ssh":{"sayHello":"hello!"}}`, out)

	ctr = ctr.
		WithWorkdir("/toplevel").
		With(daggerExec("init", "--name=toplevel", "--sdk=go", "--source=.")).
		With(daggerExec("install", "./ssh")).
		WithNewFile("main.go", `package main

import "context"

type Toplevel struct {}

func (t *Toplevel) SayHello(ctx context.Context) (string, error) {
        return dag.SSH().SayHello(ctx)
}
`,
		)
	logGen(ctx, t, ctr.Directory("."))

	out, err = ctr.With(daggerQuery(`{toplevel{sayHello}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"toplevel":{"sayHello":"hello!"}}`, out)
}

func (GoSuite) TestEmbedded(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	ctr := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c))

	ctr = ctr.
		WithWorkdir("/playground").
		With(daggerExec("init", "--name=playground", "--sdk=go", "--source=.")).
		WithNewFile("main.go", `package main

import (
	"dagger/playground/internal/dagger"
)

type Playground struct {
	*dagger.Directory
}

func New() Playground {
	return Playground{Directory: dag.Directory()}
}

func (p *Playground) SayHello() string {
	return "hello!"
}
`,
		)

	out, err := ctr.With(daggerQuery(`{playground{sayHello, directory{entries}}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"playground":{"sayHello":"hello!", "directory":{"entries": []}}}`, out)
}
