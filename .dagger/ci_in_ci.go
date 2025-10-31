package main

import (
	"context"

	"github.com/dagger/dagger/.dagger/internal/dagger"
)

// "CI in CI": check that Dagger can still run its own CI
// Note: this doesn't actually call all CI checks: only a small subset,
// selected for maximum coverage of Dagger features with limited compute expenditure.
// The actual checks being performed is an implementation detail, and should NOT be relied on.
// In other words, don't skip running <foo> just because it happens to be run here!
func (dev *DaggerDev) CiInCi(ctx context.Context) (MyCheckStatus, error) {
	ctr, err := dev.Playground(ctx, nil, false, false)
	if err != nil {
		return CheckCompleted, err
	}
	cmd := []string{"dagger", "call"}
	if dev.DockerCfg != nil {
		cmd = append(cmd, "--docker-cfg=file:$HOME/.docker/config.json")
	}
	cmd = append(cmd, "test-sdks")
	_, err = ctr.
		With(dev.withDockerCfg).
		WithMountedDirectory("./dagger", dev.Source).
		WithMountedDirectory("./dagger/.git/", dev.Git.Head().Tree().Directory(".git/")).
		WithWorkdir("./dagger").
		WithExec(cmd, dagger.ContainerWithExecOpts{Expand: true}).
		Sync(ctx)
	return CheckCompleted, err
}
