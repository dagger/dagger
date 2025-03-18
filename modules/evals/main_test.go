package main

import (
	"context"
	"dagger/evals/internal/dagger"
	"path/filepath"
	"testing"

	"github.com/dagger/testctx"
	"github.com/dagger/testctx/oteltest"
	"github.com/google/go-containerregistry/pkg/v1/tarball"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) {
	oteltest.Main(m)
}

func TestEvals(t *testing.T) {
	testctx.New(t,
		testctx.WithParallel(),
		oteltest.WithTracing[*testing.T](),
		oteltest.WithLogging[*testing.T]()).RunTests(EvalsSuite{})
}

type EvalsSuite struct{}

func (EvalsSuite) TestUndoSingle(ctx context.Context, t *testctx.T) {
	evals := &Evals{}

	res := evals.UndoSingle()

	out, err := res.WithExec([]string{"php", "--version"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "PHP 7")

	out, err = res.WithExec([]string{"vim", "--version"}).Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "VIM - Vi IMproved")

	_, err = res.WithExec([]string{"which", "nano"}, dagger.ContainerWithExecOpts{
		Expect: dagger.ReturnTypeFailure,
	}).Sync(ctx)
	require.NoError(t, err)

	tmp := t.TempDir()
	path, err := res.AsTarball().Export(ctx, filepath.Join(tmp, "image.tar"))
	require.NoError(t, err)

	image, err := tarball.ImageFromPath(path, nil)
	require.NoError(t, err)

	config, err := image.ConfigFile()
	require.NoError(t, err)

	require.NotEmpty(t, config.History)
	for _, layer := range config.History {
		require.NotContains(t, layer.CreatedBy, "nano", "Layer should not contain nano")
	}
}

func (EvalsSuite) TestBuildMulti(ctx context.Context, t *testctx.T) {
	evals := &Evals{}

	res := evals.BuildMulti()

	ctr := dag.Container().
		From("alpine").
		WithFile("/bin/booklit", res).
		WithExec([]string{"chmod", "+x", "/bin/booklit"}).
		WithExec([]string{"/bin/booklit", "--version"})

	out, err := ctr.Stdout(ctx)
	require.NoError(t, err)
	require.Contains(t, out, "0.0.0-dev")
}
