package schema

import (
	"bytes"
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceEntrypointArgs struct {
	Name string
}

func (s *workspaceSchema) withEntrypoint(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceEntrypointArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.Name == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module name is required")
	}
	return s.setWorkspaceEntrypoint(ctx, parent, args.Name)
}

func (s *workspaceSchema) withoutEntrypoint(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResult[*core.Workspace], error) {
	return s.setWorkspaceEntrypoint(ctx, parent, "")
}

// setWorkspaceEntrypoint selects name, or clears the selection when name is
// empty, in the base workspace config.
func (s *workspaceSchema) setWorkspaceEntrypoint(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	name string,
) (dagql.ObjectResult[*core.Workspace], error) {
	if envName, ok := selectedWorkspaceEnv(ctx, parent.Self()); ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("entrypoints cannot be set in env %q; set the entrypoint in the base workspace config", envName)
	}
	// Clearing a selection that cannot exist must not create a config.
	if parent.Self().ConfigFile == "" {
		if err := workspace.SetEntrypoint(nil, ".", name); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		return parent, nil
	}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := workspace.SetEntrypoint(staged.Config, staged.ConfigDir, name); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	updated, err := workspace.UpdateConfigBytesAt(ctx, staged.Data, staged.Config, staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace config: %w", err)
	}
	if bytes.Equal(updated, staged.Data) {
		return parent, nil
	}
	return s.stageWorkspaceConfigAndLock(ctx, parent, staged, updated, nil)
}
