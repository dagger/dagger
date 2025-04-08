package core

import (
	"context"
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

// LegacySuite contains tests for module versioning compatibility
type LegacySuite struct{}

func TestLegacy(t *testing.T) {
	testctx.New(t, Middleware()...).RunTests(LegacySuite{})
}

func (LegacySuite) TestLegacyExportAbsolutePath(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#7500
	//
	// Ensure that the old schemas return bools instead of absolute paths. This
	// change *likely* doesn't affect any prod code, but its probably still
	// worth making "absolute"ly sure (haha).

	c := connect(ctx, t)

	modGen := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=bare", "--source=.", "--sdk=go")).
		WithNewFile("dagger.json", `{"name": "bare", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`).
		WithNewFile("main.go", `package main

import "context"

type Bare struct {}

func (m *Bare) TestContainer(ctx context.Context) (bool, error) {
	return dag.Container().WithNewFile("./path").Export(ctx, "./path")
}

func (m *Bare) TestDirectory(ctx context.Context) (bool, error) {
	return dag.Container().WithNewFile("./path").Directory("").Export(ctx, "./path")
}

func (m *Bare) TestFile(ctx context.Context) (bool, error) {
	return dag.Container().WithNewFile("./path").File("./path").Export(ctx, "./path")
}
`,
		)

	out, err := modGen.
		With(daggerQuery(`{bare{testContainer, testDirectory, testFile}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"bare": {"testContainer": true, "testDirectory": true, "testFile": true}}`, out)
}

func (LegacySuite) TestLegacyTerminal(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#7586
	//
	// Some modules (e.g. github.com/sagikazarmark/daggerverse/borgo@77db6a9)
	// construct and return the terminal type, so these old schemas should
	// process these types as before.

	src := []byte(fmt.Sprintf(`package main
import (
	"context"
	"dagger/test/internal/dagger"
)

func New(ctx context.Context) *Test {
	return &Test{
		Ctr: dag.Container().
			From("%s").
			WithEnvVariable("COOLENV", "woo").
			WithWorkdir("/coolworkdir"),
	}
}

type Test struct {
	Ctr *dagger.Container
}

func (t *Test) Debug() *dagger.Terminal {
	return t.Ctr.Terminal()
}
`, alpineImage))

	t.Run("from cli", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()

		_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(modDir, "dagger.json"), []byte(`{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`), 0o644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(modDir, "main.go"), src, 0644)
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		// We want the size to be big enough to fit the output we're expecting, but increasing
		// the size also eventually slows down the tests due to more output being generated and
		// needing parsing.
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "ctr", "terminal")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		prompt := fmt.Sprintf("/coolworkdir%s $ ", resetSeq)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("pwd")
		require.NoError(t, err)

		_, err = console.ExpectString("/coolworkdir\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("echo $COOLENV")
		require.NoError(t, err)

		_, err = console.ExpectString("woo\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("exit")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})

	t.Run("from module", func(ctx context.Context, t *testctx.T) {
		modDir := t.TempDir()

		_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(modDir, "dagger.json"), []byte(`{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`), 0o644)
		require.NoError(t, err)
		err = os.WriteFile(filepath.Join(modDir, "main.go"), src, 0644)
		require.NoError(t, err)

		// cache the module load itself so there's less to wait for in the shell invocation below
		_, err = hostDaggerExec(ctx, t, modDir, "functions")
		require.NoError(t, err)

		// timeout for waiting for each expected line is very generous in case CI is under heavy load or something
		console, err := newTUIConsole(t, 60*time.Second)
		require.NoError(t, err)
		defer console.Close()

		tty := console.Tty()

		// We want the size to be big enough to fit the output we're expecting, but increasing
		// the size also eventually slows down the tests due to more output being generated and
		// needing parsing.
		err = pty.Setsize(tty, &pty.Winsize{Rows: 6, Cols: 16})
		require.NoError(t, err)

		cmd := hostDaggerCommand(ctx, t, modDir, "call", "debug")
		cmd.Stdin = tty
		cmd.Stdout = tty
		cmd.Stderr = tty

		err = cmd.Start()
		require.NoError(t, err)

		prompt := fmt.Sprintf("/coolworkdir%s $ ", resetSeq)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("pwd")
		require.NoError(t, err)

		_, err = console.ExpectString("/coolworkdir\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("echo $COOLENV")
		require.NoError(t, err)

		_, err = console.ExpectString("woo\r\n")
		require.NoError(t, err)

		_, err = console.ExpectString(prompt)
		require.NoError(t, err)

		_, err = console.SendLine("exit")
		require.NoError(t, err)

		go console.ExpectEOF()

		err = cmd.Wait()
		require.NoError(t, err)
	})
}

func (LegacySuite) TestContainerWithNewFile(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#7293
	//
	// Ensure that the old schemas have an optional "contents" argument
	// instead of required.

	c := connect(ctx, t)

	out, err := daggerCliBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`).
		WithNewFile("main.go", `package main

import "context"

type Test struct {}

func (m *Test) Container(ctx context.Context) (string, error) {
    return dag.Container().
        WithNewFile("./foo", ContainerWithNewFileOpts{
            Contents: "bar",
        }).
        File("./foo").
        Contents(ctx)
}

func (m *Test) Default(ctx context.Context) (string, error) {
    return dag.Container().
        WithNewFile("./foo").
        File("./foo").
        Contents(ctx)
}
`,
		).
		With(daggerQuery(`{test{container default}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test": {"container": "bar", "default": ""}}`, out)
}

func (LegacySuite) TestExecWithEntrypoint(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#7136
	//
	// Ensure that the old schemas default to use the entrypoint.

	c := connect(ctx, t)

	modGen := daggerCliBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`).
		WithNewFile("main.go", fmt.Sprintf(`package main

import "dagger/test/internal/dagger"

func New() *Test {
    return &Test{
        Container: dag.Container().
            From("%s").
            WithEntrypoint([]string{"echo"}),
    }
}

type Test struct {
    Container *dagger.Container
}

func (m *Test) Use() *dagger.Container {
    return m.Container.WithExec([]string{"hello"})

}

func (m *Test) Skip() *dagger.Container {
    return m.Container.WithExec([]string{"echo", "hello"}, dagger.ContainerWithExecOpts{
        SkipEntrypoint: true,
    })
}
`, alpineImage),
		)

	out, err := modGen.With(daggerCall("use", "stdout")).Stdout(ctx)
	require.NoError(t, err)
	require.Equal(t, "hello\n", out)

	out, err = modGen.With(daggerCall("skip", "stdout")).Stdout(ctx)
	require.NoError(t, err)
	// if the entrypoint was not skipped, it would return "echo hello\n"
	require.Equal(t, "hello\n", out)
}

func (LegacySuite) TestExecWithSkipEntrypointCompat(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#8281
	//
	// Ensure that old schemas still have skipEntrypoint API
	//
	// Tests backwards compatibility with `skipEntrypoint: false` option.
	// Doesn't work on Go because it can't distinguish between unset and
	// empty value.

	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=python", "--source=.")).
		WithWorkdir("/work").
		WithNewFile("dagger.json", `{"name": "test", "sdk": "python", "source": ".", "engineVersion": "v0.12.6"}`).
		With(pythonSource(`
import dagger
from dagger import dag

@dagger.object_type
class Test:
    @dagger.function
    async def fn(self) -> str:
        return await dag.container().from_("alpine").with_entrypoint(["sh", "-c"]).with_exec(["echo $HOME"], skip_entrypoint=False).stdout()

`)).
		With(daggerCall("fn")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "/root\n", out)
}

func (LegacySuite) TestLegacyNoExec(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#7857
	//
	// Older schemas should continue to fallback to the default command on
	// stdout and stderr.

	c := connect(ctx, t)

	modGen := daggerCliBase(t, c).
		WithWorkdir("/work").
		With(daggerExec("init", "--name=test", "--source=.", "--sdk=go")).
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`).
		WithNewFile("main.go", fmt.Sprintf(`package main

import (
    "context"
    "dagger/test/internal/dagger"
)

func New() *Test {
    return &Test{
        Container: dag.Container().
            From("%s").
            WithDefaultArgs([]string{"sh", "-c", "echo hello; echo goodbye > /dev/stderr"}),
    }
}

type Test struct {
    Container *dagger.Container
}

func (m *Test) Stdout(ctx context.Context) (string, error) {
    return m.Container.Stdout(ctx)
}

func (m *Test) Stderr(ctx context.Context) (string, error) {
    return m.Container.Stderr(ctx)
}

func (m *Test) NoExec(ctx context.Context) *dagger.Container {
	return m.Container.
        WithoutDefaultArgs().
        WithoutEntrypoint()
}
`, alpineImage),
		)

	out, err := modGen.With(daggerQuery(`{test{stdout stderr}}`)).Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"test": {"stdout": "hello\n", "stderr": "goodbye\n"}}`, out)

	_, err = modGen.With(daggerCall("no-exec", "stdout")).Stdout(ctx)
	requireErrOut(t, err, "no command has been set")

	_, err = modGen.With(daggerCall("no-exec", "stderr")).Stdout(ctx)
	requireErrOut(t, err, "no command has been set")
}

func (LegacySuite) TestReturnVoid(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#7773
	//
	// Ensure that the old schemas return Void next to error, instead of
	// just an error. Only Go is a breaking change. Not necessary to test
	// the others.

	c := connect(ctx, t)

	out, err := daggerCliBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithWorkdir("/work").
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`).
		WithNewFile("main.go", `package main

import "context"

type Test struct {}

func (m *Test) Test(ctx context.Context) (string, error) {
    val, err := dag.Dep().Dummy(ctx)
    return string(val), err
}
`,
		).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=go")).
		With(sdkSource("go", `package main

type Dep struct {}

func (m *Dep) Dummy() error {
    return nil
}
`,
		)).
		WithWorkdir("/work").
		With(daggerExec("install", "./dep", "--compat=skip")).
		With(daggerQuery(`{test{test}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test": {"test": ""}}`, out)
}

func (LegacySuite) TestGoAlias(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#7831
	//
	// Ensure that old dagger aliases are preserved.

	c := connect(ctx, t)

	mod := daggerCliBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.11.9"}`).
		WithNewFile("main.go", `package main

type Test struct {}

func (m *Test) Container(ctr *Container, msg string) *Container {
	return ctr.WithExec([]string{"echo", "hello " + msg})
}

func (m *Test) Proto(proto NetworkProtocol) NetworkProtocol {
	switch proto {
	case Tcp:
		return Udp
	case Udp:
		return Tcp
	default:
		panic("nope")
	}
}
`,
		)
	out, err := mod.
		With(daggerCall("container", "--ctr=alpine", "--msg=world", "stdout")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "hello world")

	out, err = mod.
		With(daggerCall("proto", "--proto=TCP")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "UDP")
}

func (LegacySuite) TestPipeline(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#8281
	//
	// Ensure that pipeline still exists in old schemas.
	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithWorkdir("/work").
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.12.7"}`).
		WithNewFile("main.go", `package main

import "context"

type Test struct {}

func (m *Test) Fn(ctx context.Context) (string, error) {
	return dag.Pipeline("foo").Version(ctx)
}
`,
		).
		With(daggerCall("fn")).
		Stdout(ctx)

	require.NoError(t, err)
	require.NotEmpty(t, out)
}

func (LegacySuite) TestModuleSourceCloneURL(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#8281
	//
	// Ensure that cloneURL still exists in old schemas.

	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithWorkdir("/work").
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.12.7"}`).
		WithNewFile("main.go", `package main

import "context"

type Test struct {}

func (m *Test) Fn(ctx context.Context) (string, error) {
	return dag.ModuleSource("https://github.com/dagger/dagger.git@v0.12.6").CloneURL(ctx)
}
`,
		).
		With(daggerCall("fn")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "https://github.com/dagger/dagger.git", out)
}

func (LegacySuite) TestGoCodegenOptionals(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#8106
	//
	// Ensure that Go's codegen produces a required argument in old schemas
	// when there's a non-null with a default.

	c := connect(ctx, t)

	out, err := daggerCliBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithWorkdir("/work").
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.12.7"}`).
		WithNewFile("main.go", `package main

import "context"

type Test struct {}

func (m *Test) Greet(ctx context.Context) (string, error) {
    // In v0.13 *name* is an optional argument
    return dag.Dep("Dagger").Greeting(ctx)
}
`,
		).
		WithWorkdir("/work/dep").
		With(daggerExec("init", "--name=dep", "--sdk=python")).
		With(fileContents("src/dep/__init__.py", `import dagger

@dagger.object_type
class Dep:
    name: str = "World"

    @dagger.function
    def greeting(self) -> str:
        return f"Hello, {self.name}!"
`,
		)).
		WithWorkdir("/work").
		With(daggerExec("install", "./dep", "--compat=skip")).
		With(daggerCall("greet")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "Hello, Dagger!", out)
}

func (LegacySuite) TestGitWithKeepDir(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#8318
	//
	// Ensure that the old schemas default to keeping KeepGitDir.
	//
	// v0.9.9 is a very old version that ensures we call treeLegacy+gitLegacy
	// v0.12.6 is a more recent version that ensures we call gitLegacy

	c := connect(ctx, t)

	for _, version := range []string{"v0.9.9", "0.12.6"} {
		ctr := daggerCliBase(t, c).
			With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
			WithWorkdir("/work").
			WithNewFile("dagger.json", fmt.Sprintf(`{"name": "test", "sdk": "go", "source": ".", "engineVersion": "%s"}`, version)).
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/test/internal/dagger"
)

type Test struct {}

func (m *Test) GetCommit(ctx context.Context, cmtID string) (string, error) {
	return dag.Git("github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Commit(cmtID).Commit(ctx)
}

func (m *Test) GetContents(ctx context.Context, cmtID string) (string, error) {
	return dag.Git("github.com/dagger/dagger", dagger.GitOpts{KeepGitDir: true}).Commit(cmtID).Tree().File(".git/HEAD").Contents(ctx)
}

func (m *Test) GetContentsNoKeepGitDirOpt(ctx context.Context, cmtID string) (string, error) {
	return dag.Git("github.com/dagger/dagger").Commit(cmtID).Tree().File(".git/HEAD").Contents(ctx)
}
`)

		out, err := ctr.With(daggerCall("get-commit", "--cmtID=c80ac2c13df7d573a069938e01ca13f7a81f0345")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "c80ac2c13df7d573a069938e01ca13f7a81f0345", out)

		out, err = ctr.With(daggerCall("get-contents", "--cmtID=c80ac2c13df7d573a069938e01ca13f7a81f0345")).Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "c80ac2c13df7d573a069938e01ca13f7a81f0345\n", out)

		if version == "0.12.6" {
			_, err = ctr.With(daggerCall("get-contents-no-keep-git-dir-opt", "--cmtID=c80ac2c13df7d573a069938e01ca13f7a81f0345")).Stdout(ctx)
			requireErrRegexp(t, err, ".*\\.git/HEAD: no such file or directory.*")
		}
	}
}

func (LegacySuite) TestGoUnscopedEnumValues(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#8669
	//
	// Ensure that old dagger unscoped enum values are preserved.

	c := connect(ctx, t)

	mod := daggerCliBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.13.4"}`).
		WithNewFile("main.go", `package main

import "dagger/test/internal/dagger"

type Test struct {}

func (m *Test) OldProto(proto dagger.NetworkProtocol) dagger.NetworkProtocol {
	switch proto {
	case dagger.Tcp:
		return dagger.Udp
	case dagger.Udp:
		return dagger.Tcp
	default:
		panic("nope")
	}
}

func (m *Test) NewProto(proto dagger.NetworkProtocol) dagger.NetworkProtocol {
	switch proto {
	case dagger.NetworkProtocolTcp:
		return dagger.NetworkProtocolUdp
	case dagger.NetworkProtocolUdp:
		return dagger.NetworkProtocolTcp
	default:
		panic("nope")
	}
}
`,
		)

	out, err := mod.
		With(daggerCall("old-proto", "--proto=TCP")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "UDP")

	out, err = mod.
		With(daggerCall("new-proto", "--proto=TCP")).
		Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "UDP")
}

func (LegacySuite) TestContainerWithFocus(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#8647
	//
	// Ensure that the old schemas still have withFocus/withoutFocus.

	c := connect(ctx, t)

	out, err := goGitBase(t, c).
		With(daggerExec("init", "--name=test", "--sdk=go", "--source=.")).
		WithWorkdir("/work").
		WithNewFile("dagger.json", `{"name": "test", "sdk": "go", "source": ".", "engineVersion": "v0.13.3"}`).
		WithNewFile("main.go", `package main

import "context"

type Test struct {}

func (m *Test) Fn(ctx context.Context) (string, error) {
	return dag.Container().
		From("alpine").
		WithFocus().
		WithoutFocus().
		WithExec([]string{"echo", "hello world"}).
		Stdout(ctx)
}
`,
		).
		With(daggerCall("fn")).
		Stdout(ctx)

	require.NoError(t, err)
	require.Equal(t, "hello world\n", out)
}

func (LegacySuite) TestContainerAsService(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#8865
	//
	// Ensure that the legacy AsService api uses entrypoint by default
	// and use WithExec if that is configured

	c := connect(ctx, t)

	serversource := `package main

import (
	"fmt"
	"net/http"
	"os"
	"strings"
)

func main() {
	http.HandleFunc("/hello", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, "args: %s", strings.Join(os.Args, ","))
	})

	fmt.Println(http.ListenAndServe(":8080", nil))
}`

	daggermodmaingo := `package main

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"

	"dagger/foo/internal/dagger"
)

type Foo struct{}

func (f *Foo) TestServiceBindingEntrypoint(ctx context.Context, app *dagger.File) (string, error) {
	ctr, err := f.StartEntrypointByDefault(ctx, app)
	if err != nil {
		return "", err
	}

	return dag.Container().
		From("alpine:3.20.2").
		WithExec([]string{"sh", "-c", "apk add curl"}).
		WithServiceBinding("myapp", ctr.AsService()).
		WithExec([]string{"sh", "-c", "curl -vXGET 'http://myapp:8080/hello'"}).
		Stdout(ctx)
}

func (f *Foo) TestServiceUpEntrypoint(ctx context.Context, app *dagger.File) (string, error) {
	ctr, err := f.StartEntrypointByDefault(ctx, app)
	if err != nil {
		return "", err
	}
	go ctr.AsService().Up(ctx, dagger.ServiceUpOpts{
		Ports: []dagger.PortForward{
			{
				Protocol: dagger.NetworkProtocolTcp,
				Frontend: 8080,
				Backend:  8080,
			},
		},
	})
	return fetch()
}

func (f *Foo) TestContainerUpEntrypoint(ctx context.Context, app *dagger.File) (string, error) {
	ctr, err := f.StartEntrypointByDefault(ctx, app)
	if err != nil {
		return "", err
	}
	go ctr.Up(ctx, dagger.ContainerUpOpts{
		Ports: []dagger.PortForward{
			{
				Protocol: dagger.NetworkProtocolTcp,
				Frontend: 8080,
				Backend:  8080,
			},
		},
	})
	return fetch()
}

func (f *Foo) StartEntrypointByDefault(ctx context.Context, app *dagger.File) (*dagger.Container, error) {
	return dag.Container().
		From("alpine:3.20.2").
		WithFile("/bin/app", app).
		WithEntrypoint([]string{"/bin/app", "via-entrypoint"}).
		WithDefaultArgs([]string{"/bin/app", "via-default-args"}).
		WithExposedPort(8080), nil
}

func (f *Foo) TestServiceBindingWithExec(ctx context.Context, app *dagger.File) (string, error) {
	ctr, err := f.UseWithExecWhenAvailable(ctx, app)
	if err != nil {
		return "", err
	}

	return dag.Container().
		From("alpine:3.20.2").
		WithExec([]string{"sh", "-c", "apk add curl"}).
		WithServiceBinding("myapp", ctr.AsService()).
		WithExec([]string{"sh", "-c", "curl -vXGET 'http://myapp:8080/hello'"}).
		Stdout(ctx)
}

func (f *Foo) TestServiceUpWithExec(ctx context.Context, app *dagger.File) (string, error) {
	ctr, err := f.UseWithExecWhenAvailable(ctx, app)
	if err != nil {
		return "", err
	}
	go ctr.AsService().Up(ctx, dagger.ServiceUpOpts{
		Ports: []dagger.PortForward{
			{
				Protocol: dagger.NetworkProtocolTcp,
				Frontend: 8080,
				Backend:  8080,
			},
		},
	})
	return fetch()
}

func (f *Foo) TestContainerUpWithExec(ctx context.Context, app *dagger.File) (string, error) {
	ctr, err := f.UseWithExecWhenAvailable(ctx, app)
	if err != nil {
		return "", err
	}
	go ctr.Up(ctx, dagger.ContainerUpOpts{
		Ports: []dagger.PortForward{
			{
				Protocol: dagger.NetworkProtocolTcp,
				Frontend: 8080,
				Backend:  8080,
			},
		},
	})
	return fetch()
}

func (f *Foo) UseWithExecWhenAvailable(ctx context.Context, app *dagger.File) (*dagger.Container, error) {
	return dag.Container().
		From("alpine:3.20.2").
		WithFile("/bin/app", app).
		WithEntrypoint([]string{"/bin/app", "via-entrypoint"}).
		WithDefaultArgs([]string{"/bin/app", "via-default-args"}).
		WithExec([]string{"/bin/app", "via-withExec"}).
		WithExposedPort(8080), nil
}

func fetch() (string, error) {
	for range 10 {
		time.Sleep(2 * time.Second)
		client := http.Client{
			Timeout: 2 * time.Second,
		}
		resp, err := client.Get("http://localhost:8080/hello")
		if err != nil {
			fmt.Println(err)
			continue
		}
		defer resp.Body.Close()
		dt, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Println(err)
			continue
		}
		return string(dt), nil
	}
	return "", fmt.Errorf("could not fetch")
}
`

	app := c.Container().
		From(golangImage).
		WithWorkdir("/work").
		WithNewFile("main.go", serversource).
		WithExec([]string{"go", "build", "-o=app", "main.go"}).
		File("/work/app")

	ctr := daggerCliBase(t, c).
		WithWorkdir("/work/").
		WithFile("app", app).
		WithNewFile("main.go", daggermodmaingo).
		WithNewFile("dagger.json", `{"name": "foo", "sdk": "go", "source": ".", "engineVersion": "v0.13.7"}`)

	// verify that the engine uses the entrypoint when serving the legacy AsService api
	t.Run("use entrypoint by default", func(ctx context.Context, t *testctx.T) {
		output, err := ctr.
			With(daggerExec("call", "test-service-binding-entrypoint", "--app=app")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-entrypoint,/bin/app,via-default-args", output)

		output, err = ctr.
			With(daggerExec("call", "test-service-up-entrypoint", "--app=app")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-entrypoint,/bin/app,via-default-args", output)

		output, err = ctr.
			With(daggerExec("call", "test-container-up-entrypoint", "--app=app")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-entrypoint,/bin/app,via-default-args", output)
	})

	// verify that the engine uses the entrypoint when serving the legacy AsService api
	t.Run("use withExec when used", func(ctx context.Context, t *testctx.T) {
		output, err := ctr.
			With(daggerExec("call", "test-service-binding-with-exec", "--app=app")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-withExec", output)

		output, err = ctr.
			With(daggerExec("call", "test-service-up-with-exec", "--app=app")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-withExec", output)

		output, err = ctr.
			With(daggerExec("call", "test-container-up-with-exec", "--app=app")).
			Stdout(ctx)
		require.NoError(t, err)
		require.Equal(t, "args: /bin/app,via-withExec", output)
	})
}

func (LegacySuite) TestDirectoryTrailingSlash(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#9118
	//
	// Ensure that the legacy methods that return paths don't return trailing
	// slashes for directories.

	c := connect(ctx, t)

	modGen := goGitBase(t, c).
		With(daggerExec("init", "--name=bare", "--sdk=go", "--source=.")).
		WithWorkdir("/work").
		WithNewFile("dagger.json", `{"name": "bare", "sdk": "go", "source": ".", "engineVersion": "v0.16.0"}`).
		WithNewFile("main.go", `package main

import (
	"context"
	"dagger/bare/internal/dagger"
)

type Bare struct {}

func (m *Bare) TestEntries(ctx context.Context) ([]string, error) {
	return m.dir().Entries(ctx)
}

func (m *Bare) TestGlob(ctx context.Context) ([]string, error) {
	return m.dir().Glob(ctx, "**/*")
}

func (m *Bare) TestName(ctx context.Context) (string, error) {
	return m.dir().Directory("foo").Name(ctx)
}

func (m *Bare) dir() *dagger.Directory {
	return dag.Directory().
		WithDirectory("foo", dag.Directory()).
		WithNewFile("foo/bar", "").
		WithNewFile("baz", "")
}
`)

	out, err := modGen.
		With(daggerQuery(`{bare{testEntries, testGlob, testName}}`)).
		Stdout(ctx)
	require.NoError(t, err)
	require.JSONEq(t, `{"bare": {"testEntries": ["baz", "foo"], "testGlob": ["baz", "foo", "foo/bar"], "testName": "foo"}}`, out)
}

func (LegacySuite) TestDockerBuild(ctx context.Context, t *testctx.T) {
	// Changed in dagger/dagger#9118
	//
	// Ensure that the legacy methods that return paths don't return trailing
	// slashes for directories.

	c := connect(ctx, t)
	t.Run("with build secrets", func(ctx context.Context, t *testctx.T) {
		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/foo").
			WithNewFile("main.go", `package main

import (
	"context"
	"dagger/foo/internal/dagger"
)

type Foo struct{}

// Returns a container that echoes whatever string argument is provided
func (m *Foo) CtrBuild(ctx context.Context, dir *dagger.Directory, mySecret *dagger.Secret) (string, error) {
	secretVal, err := mySecret.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	buildSecret := dag.SetSecret("my-secret", secretVal)
	return dag.Container().
		Build(dir, dagger.ContainerBuildOpts{
			Secrets: []*dagger.Secret{buildSecret},
		}).
		WithExec(nil).Stdout(ctx)
}

func (m *Foo) DirBuild(ctx context.Context, dir *dagger.Directory, mySecret *dagger.Secret) (string, error) {
	secretVal, err := mySecret.Plaintext(ctx)
	if err != nil {
		return "", err
	}

	buildSecret := dag.SetSecret("my-secret", secretVal)
	return dir.DockerBuild(dagger.DirectoryDockerBuildOpts{
		Secrets: []*dagger.Secret{buildSecret},
	}).
		WithExec(nil).Stdout(ctx)
}
`).
			WithNewFile("dagger.json", `{
  "name": "foo",
  "engineVersion": "v0.18.1",
  "sdk": {
    "source": "go"
  }
}`)

		dockerfile := `FROM golang:1.18.2-alpine
WORKDIR /src
RUN --mount=type=secret,id=my-secret,required=true test "$(cat /run/secrets/my-secret)" = "barbar"
RUN --mount=type=secret,id=my-secret,required=true cp /run/secrets/my-secret /secret
CMD cat /secret && (cat /secret | tr "[a-z]" "[A-Z]")
`

		t.Run("container.build builtin frontend", func(ctx context.Context, t *testctx.T) {
			stdout, err := base.
				WithNewFile("Dockerfile", dockerfile).
				WithNewFile("mysecret.txt", "barbar").
				With(daggerCall("ctr-build", "--my-secret=file://./mysecret.txt", "--dir=.")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})

		t.Run("container.build remote frontend", func(ctx context.Context, t *testctx.T) {
			stdout, err := base.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile).
				WithNewFile("mysecret.txt", "barbar").
				With(daggerCall("ctr-build", "--my-secret=file://./mysecret.txt", "--dir=.")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})

		t.Run("directory.dockerBuild builtin frontend", func(ctx context.Context, t *testctx.T) {
			stdout, err := base.
				WithNewFile("Dockerfile", dockerfile).
				WithNewFile("mysecret.txt", "barbar").
				With(daggerCall("dir-build", "--my-secret=file://./mysecret.txt", "--dir=.")).
				Stdout(ctx)
			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})

		t.Run("directory.dockerBuild remote frontend", func(ctx context.Context, t *testctx.T) {
			stdout, err := base.WithNewFile("Dockerfile", "#syntax=docker/dockerfile:1\n"+dockerfile).
				WithNewFile("mysecret.txt", "barbar").
				With(daggerCall("dir-build", "--my-secret=file://./mysecret.txt", "--dir=.")).
				Stdout(ctx)

			require.NoError(t, err)
			require.Contains(t, stdout, "***")
			require.Contains(t, stdout, "BARBAR")
		})
	})

	t.Run("prevent duplicate secret transform", func(ctx context.Context, t *testctx.T) {
		base := c.Container().From(golangImage).
			WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
			WithWorkdir("/work/foo").
			WithNewFile("main.go", fmt.Sprintf(`package main

import (
	"context"
	"dagger/foo/internal/dagger"
)

type Foo struct{}

func (m *Foo) CtrBuild(ctx context.Context, dir *dagger.Directory, mySecret *dagger.Secret) (*dagger.Container, error) {
	secretVal, err := mySecret.Plaintext(ctx)
	if err != nil {
		return nil, err
	}
	buildSecret := dag.SetSecret("gh-secret", secretVal)

	return dag.Container().
		From("%s").
		WithWorkdir("/src").
		WithMountedSecret("/run/secret", buildSecret).
		WithExec([]string{"cat", "/run/secret"}).
		WithNewFile("Dockerfile", "FROM alpine\nCOPY / /\n").
		Directory("/src").
		DockerBuild().
		Sync(ctx)
}

`, alpineImage)).
			WithNewFile("dagger.json", `{
  "name": "foo",
  "engineVersion": "v0.18.1",
  "sdk": {
    "source": "go"
  }
}`)

		// building src should only transform the secrets from the raw
		// Dockerfile, not from the src input
		_, err := base.WithNewFile("mysecret.txt", "barbar").
			With(daggerCall("ctr-build", "--my-secret=file://./mysecret.txt", "--dir=.")).
			Stdout(ctx)

		require.NoError(t, err)
	})
}
