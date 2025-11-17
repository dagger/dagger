package main

import (
	"context"

	"github.com/dagger/dagger/.github/internal/dagger"
)

// +check
//
// # Build dagger from source, and check that it can bootstrap its own CI
//
// Note: this doesn't actually call all CI checks: only a small subset,
// selected for maximum coverage of Dagger features with limited compute expenditure.
// The actual checks being performed is an implementation detail, and should NOT be relied on.
// In other words, don't skip running <foo> just because it happens to be run here!
func (ci *CI) Bootstrap(
	ctx context.Context,
	// The Dagger repository to run CI against
	// +defaultPath="/"
	repo *dagger.GitRepository,
) error {
	source := repo.Head().Tree().WithChanges(repo.Uncommitted())
	engine := dag.EngineDev()
	cmd := []string{"dagger", "call"}
	if engine.ClientDockerConfig() != nil {
		cmd = append(cmd, "--docker-cfg=file:$HOME/.docker/config.json")
	}
	cmd = append(cmd, "test-sdks")
	_, err := dag.EngineDev().
		Playground().
		WithMountedDirectory("./dagger", source).
		WithWorkdir("./dagger").
		WithExec(cmd, dagger.ContainerWithExecOpts{Expand: true}).
		Sync(ctx)
	return err
}
