package core

import (
	"context"

	"dagger.io/dagger/telemetry"
	"github.com/dagger/dagger/internal/buildkit/executor"
)

func (container *Container) setupWSFSMounts(
	ctx context.Context,
	mounts ContainerMountData,
	execMounts []executor.Mount,
) (func() error, error) {
	hasWorkspaceMount := false
	for _, mount := range container.Mounts {
		if mount.WorkspaceSource != nil {
			hasWorkspaceMount = true
			break
		}
	}
	if !hasWorkspaceMount {
		return setupWSFSMountsImpl(ctx, container, mounts, execMounts)
	}

	setupCtx, setupSpan := Tracer(ctx).Start(ctx, "workspace mounts: setup")
	cleanup, err := setupWSFSMountsImpl(setupCtx, container, mounts, execMounts)
	telemetry.EndWithCause(setupSpan, &err)
	if err != nil {
		return nil, err
	}

	return func() error {
		cleanupCtx := context.WithoutCancel(ctx)
		cleanupCtx, cleanupSpan := Tracer(cleanupCtx).Start(cleanupCtx, "workspace mounts: cleanup")
		cleanupErr := cleanup()
		telemetry.EndWithCause(cleanupSpan, &cleanupErr)
		return cleanupErr
	}, nil
}
