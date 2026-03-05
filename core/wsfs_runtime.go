package core

import (
	"context"
	"fmt"
	"strings"
)

func (container *Container) setupWSFSMounts(ctx context.Context, _ ContainerMountData) (func() error, error) {
	_ = ctx

	if !container.HasWorkspaceMount() {
		return func() error { return nil }, nil
	}

	targets := make([]string, 0, len(container.Mounts))
	for _, mount := range container.Mounts {
		if mount.WorkspaceSource != nil {
			targets = append(targets, mount.Target)
		}
	}

	return nil, fmt.Errorf(
		"container.withMountedWorkspace: wsfs runtime not implemented yet (mounts: %s)",
		strings.Join(targets, ", "),
	)
}
