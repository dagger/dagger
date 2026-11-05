package main

import (
	"context"

	"dagger/python-client-dev/internal/dagger"
)

type TestForPythonVersion struct {
	// The base container to run the tests
	// +private
	Container *dagger.Container
	// The python version to test against
	// +private
	Version string
}

// Run python slow tests
// +check
func (t *TestForPythonVersion) Slow(ctx context.Context, ws *dagger.Workspace) error {
	return t.Run(ctx, ws, []string{"-Wd", "-l", "-m", "slow and not provision"})
}

// Run python unit tests
// +check
func (t *TestForPythonVersion) Unit(ctx context.Context, ws *dagger.Workspace) error {
	return t.Run(ctx, ws, []string{"-m", "not slow and not provision"})
}

// Run the pytest command.
func (t *TestForPythonVersion) Run(
	ctx context.Context,
	ws *dagger.Workspace,
	// Arguments to pass to pytest
	args []string,
) error {
	return dag.Pytest(ws, dagger.PytestOpts{
		Container:  t.Container.WithEnvVariable("UV_PYTHON", t.Version),
		SourcePath: "sdk/python",
		Args:       args,
	}).Test(ctx)
}
