package core

// Workspace alignment: mostly aligned; coverage targets post-workspace module error-surface semantics, but setup still relies on historical module helpers.
// Scope: Execution error translation, unexposed host/engine APIs, and preservation of large exec error output.
// Intent: Keep module error handling and error-surface behavior separate from the remaining umbrella runtime coverage.

import (
	"context"
	"os"
	"path/filepath"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestExecError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := c.Container().From(alpineImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=playground", "--sdk=go")).
		WithNewFile("main.go", `
package main

import (
	"context"
	"errors"
)

type Playground struct{}

func (p *Playground) DoThing(ctx context.Context) error {
	_, err := dag.Container().From("`+alpineImage+`").WithExec([]string{"sh", "-c", "exit 5"}).Sync(ctx)
	var e *ExecError
	if errors.As(err, &e) {
		if e.ExitCode == 5 {
			return nil
		}
	}
	panic("yikes")
}
`,
		)

	_, err := modGen.
		With(daggerQuery(`{doThing}`)).
		Stdout(ctx)
	require.NoError(t, err)
}

// TestHostError verifies the host api is not exposed to modules
func (ModuleSuite) TestHostError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", `package main
 			import (
 				"context"
				"dagger/test/internal/dagger"
 			)
 			type Test struct {}
 			func (m *Test) Fn(ctx context.Context) *dagger.Directory {
 				return dag.Host().Directory(".")
 			}
 			`,
		).
		With(daggerCall("fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Host undefined")
}

// TestEngineError verifies the engine api is not exposed to modules
func (ModuleSuite) TestEngineError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := c.Container().From(golangImage).
		WithMountedFile(testCLIBinPath, daggerCliFile(t, c)).
		WithWorkdir("/work").
		With(daggerExec("init", "--source=.", "--name=test", "--sdk=go")).
		WithNewFile("/work/main.go", `package main
 			import (
 				"context"
 			)
 			type Test struct {}
 			func (m *Test) Fn(ctx context.Context) error {
 				_, _ = dag.Engine().LocalCache().EntrySet().Entries(ctx)
				return nil
 			}
 			`,
		).
		With(daggerCall("fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Engine undefined")
}

func (ModuleSuite) TestLargeErrors(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()

	_, err := hostDaggerExec(ctx, t, modDir, "init", "--source=.", "--name=test", "--sdk=go")
	require.NoError(t, err)

	moduleSrc := `package main

import (
  "context"
)

type Test struct{}

func (m *Test) RunNoisy(ctx context.Context) error {
	_, err := dag.Container().
		From("` + alpineImage + `").
		WithExec([]string{"sh", "-c", ` + "`" + `
			for i in $(seq 100); do
				for j in $(seq 1024); do
					echo -n x
					echo -n y >/dev/stderr
				done
				echo
			done
			exit 42
		` + "`" + `}).
		Sync(ctx)
	return err
}
`
	err = os.WriteFile(filepath.Join(modDir, "main.go"), []byte(moduleSrc), 0o644)
	require.NoError(t, err)

	c := connect(ctx, t)

	err = c.ModuleSource(modDir).AsModule().Serve(ctx)
	require.NoError(t, err)

	_, err = testutil.QueryWithClient[struct {
		Test struct {
			RunNoisy any
		}
	}](c, t, `{test{runNoisy}}`, nil)
	var execError *dagger.ExecError
	require.ErrorAs(t, err, &execError)

	// if we get `2` here, that means we're getting the less helpful error:
	// process "/runtime" did not complete successfully: exit code: 2
	require.Equal(t, 42, execError.ExitCode)
	require.Contains(t, execError.Stdout, "xxxxx")
	require.Contains(t, execError.Stderr, "yyyyy")
}
