package core

import (
	"context"

	"github.com/dagger/dagger/internal/buildkit/executor"
)

func (container *Container) setupWSFSMounts(
	ctx context.Context,
	mounts ContainerMountData,
	execMounts []executor.Mount,
) (func() error, error) {
	return setupWSFSMountsImpl(ctx, container, mounts, execMounts)
}
