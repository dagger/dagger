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
	"github.com/stretchr/testify/require"

	"github.com/dagger/dagger/testctx"
)

// LegacySuite contains tests for module versioning compatibility
type LegacySuite struct{}

func TestLegacy(t *testing.T) {
	testctx.Run(testCtx, t, LegacySuite{}, Middleware()...)
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

		_, err = console.SendLine("pwd")
		require.NoError(t, err)

		_, err = console.ExpectString("/coolworkdir")
		require.NoError(t, err)

		_, err = console.SendLine("echo $COOLENV")
		require.NoError(t, err)

		err = console.ExpectLineRegex(ctx, "woo")
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

		_, err = console.SendLine("pwd")
		require.NoError(t, err)

		_, err = console.ExpectString("/coolworkdir")
		require.NoError(t, err)

		_, err = console.SendLine("echo $COOLENV")
		require.NoError(t, err)

		err = console.ExpectLineRegex(ctx, "woo")
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
	require.ErrorContains(t, err, "no command has been set")

	_, err = modGen.With(daggerCall("no-exec", "stderr")).Stdout(ctx)
	require.ErrorContains(t, err, "no command has been set")
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
		With(daggerQuery(`{test{test}}`)).
		Stdout(ctx)

	require.NoError(t, err)
	require.JSONEq(t, `{"test": {"test": ""}}`, out)
}

