package main

import (
	"context"

	"dagger/python-sdk-dev/internal/dagger"
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
func (t *TestForPythonVersion) Slow(ctx context.Context) error {
	return t.Run(ctx, []string{"-Wd", "-l", "-m", "slow and not provision"})
}

// Run python unit tests
// +check
func (t *TestForPythonVersion) Unit(ctx context.Context) error {
	return t.Run(ctx, []string{"-m", "not slow and not provision"})
}

// Run the pytest command.
func (t *TestForPythonVersion) Run(
	ctx context.Context,
	// Arguments to pass to pytest
	args []string,
) error {
	return dag.Pytest(dagger.PytestOpts{
		Container: t.Container,
		Source:    t.Container.Directory("/src/sdk/python"),
	}).Test(ctx, dagger.PytestTestOpts{
		Version: t.Version,
		Args:    args,
	})
}
