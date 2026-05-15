package core

// These tests cover error surfaces from module code. They verify execution
// error translation, hidden host/engine APIs, and large exec error output.
//
// See also:
// - module_call_test.go: successful module invocation flows.
// - module_validation_test.go: load-time validation errors.

import (
	"context"

	"dagger.io/dagger"
	"github.com/dagger/dagger/internal/testutil"
	"github.com/dagger/testctx"
	"github.com/stretchr/testify/require"
)

func (ModuleSuite) TestExecError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	modGen := moduleFixture(t, c, "go/exec-error")

	_, err := modGen.
		With(daggerQueryAt(".", `{doThing}`)).
		Stdout(ctx)
	require.NoError(t, err)
}

// TestHostError verifies the host api is not exposed to modules
func (ModuleSuite) TestHostError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := moduleFixture(t, c, "go/host-error").
		With(daggerCallAt(".", "fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Host undefined")
}

// TestEngineError verifies the engine api is not exposed to modules
func (ModuleSuite) TestEngineError(ctx context.Context, t *testctx.T) {
	c := connect(ctx, t)

	_, err := moduleFixture(t, c, "go/engine-error").
		With(daggerCallAt(".", "fn")).
		Sync(ctx)
	requireErrOut(t, err, "dag.Engine undefined")
}

func (ModuleSuite) TestLargeErrors(ctx context.Context, t *testctx.T) {
	modDir := t.TempDir()
	copyTestdataFixture(ctx, t, modDir, "modules", "go", "large-errors")

	c := connect(ctx, t)

	err := c.ModuleSource(modDir).AsModule().Serve(ctx)
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
