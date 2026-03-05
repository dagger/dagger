package core

import (
	"context"
)

func (container *Container) setupWSFSMounts(ctx context.Context, _ ContainerMountData) (func() error, error) {
	_ = ctx

	// WSFS runtime mounting is introduced in a later stage. For now workspace
	// mounts are represented via regular mount sources.
	return func() error { return nil }, nil
}
