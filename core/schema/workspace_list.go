package schema

import (
	"context"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) moduleList(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.Array[*core.WorkspaceModule], error) {
	if !parent.HasConfig {
		return nil, nil
	}

	cfg, err := readWorkspaceConfig(ctx, parent)
	if err != nil {
		return nil, err
	}

	configDir := filepath.Dir(parent.ConfigPath)
	modules := make(core.WorkspaceModules, 0, len(cfg.Modules))
	for name, entry := range cfg.Modules {
		source := filepath.ToSlash(workspace.ResolveModuleEntrySource(configDir, entry.Source))
		modules = append(modules, &core.WorkspaceModule{
			Name:       name,
			Entrypoint: entry.Entrypoint,
			Source:     source,
		})
	}
	modules.Sort()

	return dagql.Array[*core.WorkspaceModule](modules), nil
}
