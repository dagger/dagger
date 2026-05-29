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
	args struct {
		All bool `default:"false"`
	},
) (dagql.Array[dagql.String], error) {
	var cfg *workspace.Config
	if parent.ConfigFile != "" {
		var err error
		cfg, err = readWorkspaceConfig(ctx, parent)
		if err != nil {
			return nil, err
		}
	}

	userCfg, _, _, err := readUserConfig(ctx)
	if err != nil {
		return nil, err
	}

	var names []string
	if args.All {
		names = workspace.EnvNamesAll(cfg, userCfg)
	} else {
		names = workspace.EnvNamesForWorkspace(cfg, userCfg, parent.EnvConfigKey)
	}
	out := make(dagql.Array[dagql.String], len(names))
	for i, name := range names {
		out[i] = dagql.String(name)
	}
	return out, nil
}

type workspaceEnvMutationArgs struct {
	Name   string
	Here   bool `default:"false"`
	Global bool `default:"false"`
}

func (s *workspaceSchema) envCreate(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceEnvMutationArgs,
) (dagql.String, error) {
	if args.Name == "" {
		return "", fmt.Errorf("environment name is required")
	}
	if args.Global || (parent.ConfigFile == "" && parent.LocalConfigReadOnly()) {
		if err := updateUserConfigBytes(ctx, func(existingData []byte) ([]byte, error) {
			userCfg := &workspace.UserConfig{}
			if len(existingData) > 0 {
				var err error
				userCfg, err = workspace.ParseUserConfig(existingData)
				if err != nil {
					return nil, err
				}
			}
			workspace.EnsureUserEnv(userCfg, args.Name, "")
			return workspace.SerializeUserConfig(userCfg), nil
		}); err != nil {
			return "", err
		}
		return dagql.String(args.Name), nil
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigInitIfMissing, args.Here)
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
	if args.Global {
		if err := updateUserConfigBytes(ctx, func(existingData []byte) ([]byte, error) {
			userCfg := &workspace.UserConfig{}
			if len(existingData) > 0 {
				var err error
				userCfg, err = workspace.ParseUserConfig(existingData)
				if err != nil {
					return nil, err
				}
			}
			if err := workspace.RemoveUserEnv(userCfg, args.Name); err != nil {
				return nil, err
			}
			return workspace.SerializeUserConfig(userCfg), nil
		}); err != nil {
			return "", err
		}
		return dagql.String(args.Name), nil
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigMustExist, args.Here)
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
