package schema

import (
	"context"
	"path"

	"github.com/dagger/dagger/core"
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

	configDir := path.Dir(parent.ConfigPath)
	modules := make(core.WorkspaceModules, 0, len(cfg.Modules))
	for name, entry := range cfg.Modules {
		source := entry.Source
		if core.FastModuleSourceKindCheck(source, "") == core.ModuleSourceKindLocal {
			source = path.Join(configDir, source)
		}
		modules = append(modules, &core.WorkspaceModule{
			Name:      name,
			Blueprint: entry.Blueprint,
			Source:    source,
		})
	}
	modules.Sort()

	return dagql.Array[*core.WorkspaceModule](modules), nil
}
