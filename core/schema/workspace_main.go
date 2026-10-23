package schema

import (
	"bytes"
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceMainArgs struct {
	Name string
}

func (s *workspaceSchema) workspaceModuleEntrypoint(
	_ context.Context,
	parent *core.WorkspaceModule,
	_ struct{},
) (dagql.Boolean, error) {
	return dagql.Boolean(parent.Main), nil
}

func (s *workspaceSchema) main(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.String, error) {
	data, err := s.configRead(ctx, parent, configReadArgs{})
	if err != nil {
		return "", err
	}
	cfg, err := workspace.ParseConfig([]byte(data))
	if err != nil {
		return "", err
	}
	name, err := workspace.MainName(cfg)
	if err != nil {
		return "", err
	}
	return dagql.String(name), nil
}

func (s *workspaceSchema) withMain(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceMainArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.Name == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module name is required")
	}
	return s.setWorkspaceMain(ctx, parent, args.Name)
}

func (s *workspaceSchema) withoutMain(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResult[*core.Workspace], error) {
	return s.setWorkspaceMain(ctx, parent, "")
}

// setWorkspaceMain selects name, or clears the selection when name is
// empty, in the base workspace config.
func (s *workspaceSchema) setWorkspaceMain(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	name string,
) (dagql.ObjectResult[*core.Workspace], error) {
	if envName, ok := selectedWorkspaceEnv(ctx, parent.Self()); ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("the workspace main cannot be set in env %q; set it in the base workspace config", envName)
	}
	// Clearing a selection that cannot exist must not create a config.
	if parent.Self().ConfigFile == "" {
		if err := workspace.SetMain(nil, ".", name); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		return parent, nil
	}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := workspace.SetMain(staged.Config, staged.ConfigDir, name); err != nil {
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
