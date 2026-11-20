package main

import (
	"context"

	"dagger/python-client-dev/internal/dagger"
	"dagger/python-client-dev/internal/dagger/clients/pytest"
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
	ctr := pytest.New(ws, pytest.PytestOpts{SourcePath: "sdk/python"}).
		InstallPytestOtel(
			// Make the venv for this Python version before the plugin goes in:
			// otherwise `uv run` replaces the venv, and the plugin with it.
			t.Container.
				WithEnvVariable("UV_PYTHON", t.Version).
				WithExec(uv("sync")),
			pytest.PytestInstallPytestOtelOpts{
				Runner:    pytest.PytestRunnerUv,
				EnvTarget: pytest.PytestEnvTargetVenv,
			})
	// The tests must reach the dev engine that DevContainer configures, not
	// the engine running this exec: unset the session it gives every exec.
	_, err := ctr.
		WithExec(append([]string{"env", "-u", "DAGGER_SESSION_PORT", "-u", "DAGGER_SESSION_TOKEN", "uv", "run", "pytest"}, args...)).
		Sync(ctx)
	return err
}
