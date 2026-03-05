//go:build !linux

package core

import (
	"context"

	"github.com/dagger/dagger/internal/buildkit/executor"
)

func setupWSFSMountsImpl(
	ctx context.Context,
	container *Container,
	mounts ContainerMountData,
	execMounts []executor.Mount,
) (func() error, error) {
	_ = ctx
	_ = container
	_ = mounts
	_ = execMounts
	return func() error { return nil }, nil
}
