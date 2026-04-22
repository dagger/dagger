package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) envList(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.Array[dagql.String], error) {
	if !parent.HasConfig {
		return nil, fmt.Errorf("no config.toml found in workspace")
	}

	cfg, err := readWorkspaceConfig(ctx, parent)
	if err != nil {
		return nil, err
	}

	names := workspace.EnvNames(cfg)
	out := make(dagql.Array[dagql.String], len(names))
	for i, name := range names {
		out[i] = dagql.String(name)
	}
	return out, nil
}

type workspaceEnvMutationArgs struct {
	Name string
}

func (s *workspaceSchema) envCreate(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceEnvMutationArgs,
) (dagql.String, error) {
	if args.Name == "" {
		return "", fmt.Errorf("environment name is required")
	}
	if !parent.HasConfig {
		return "", fmt.Errorf("no config.toml found in workspace")
	}

	cfg, err := readWorkspaceConfig(ctx, parent)
	if err != nil {
		return "", err
	}

	if workspace.EnsureEnv(cfg, args.Name) {
		if err := writeWorkspaceConfig(ctx, parent, cfg); err != nil {
			return "", err
		}
	}

	return dagql.String(args.Name), nil
}

func (s *workspaceSchema) envRemove(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceEnvMutationArgs,
) (dagql.String, error) {
	if args.Name == "" {
		return "", fmt.Errorf("environment name is required")
	}
	if !parent.HasConfig {
		return "", fmt.Errorf("no config.toml found in workspace")
	}

	cfg, err := readWorkspaceConfig(ctx, parent)
	if err != nil {
		return "", err
	}

	if err := workspace.RemoveEnv(cfg, args.Name); err != nil {
		return "", err
	}
	if err := writeWorkspaceConfig(ctx, parent, cfg); err != nil {
		return "", err
	}

	return dagql.String(args.Name), nil
}
