package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/dagql"
)

type workspaceUninstallArgs struct {
	Name string
	Here bool `default:"false"`
}

func (s *workspaceSchema) uninstall(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceUninstallArgs,
) (dagql.String, error) {
	if parent.CompatWorkspace() != nil {
		return "", fmt.Errorf("workspace is using legacy dagger.json config; run dagger setup first")
	}
	if args.Name == "" {
		return "", fmt.Errorf("module name is required")
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigMustExist, args.Here)
	if err != nil {
		return "", err
	}

	if _, ok := cfg.Modules[args.Name]; !ok {
		return "", fmt.Errorf("module %q is not installed in the workspace", args.Name)
	}

	delete(cfg.Modules, args.Name)
	if err := writeWorkspaceConfig(ctx, parent, cfg); err != nil {
		return "", err
	}

	cfgPath, err := configHostPath(parent)
	if err != nil {
		return "", err
	}

	return dagql.String(fmt.Sprintf("Uninstalled module %q from %s", args.Name, cfgPath)), nil
}
