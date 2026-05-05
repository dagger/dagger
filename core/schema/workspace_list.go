package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) moduleList(
	ctx context.Context,
	parent *core.Workspace,
	_ struct{},
) (dagql.ObjectResultArray[*core.WorkspaceModule], error) {
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

	results := make(dagql.ObjectResultArray[*core.WorkspaceModule], 0, len(modules))
	dag := dagql.CurrentDagqlServer(ctx)
	if dag == nil {
		return nil, fmt.Errorf("workspace module list: dagql server not found")
	}
	for _, module := range modules {
		var result dagql.ObjectResult[*core.WorkspaceModule]
		if err := dag.Select(ctx, dag.Root(), &result, dagql.Selector{
			Field: "__workspaceModule",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(module.Name)},
				{Name: "entrypoint", Value: dagql.Boolean(module.Entrypoint)},
				{Name: "source", Value: dagql.String(module.Source)},
			},
		}); err != nil {
			return nil, fmt.Errorf("workspace module list: create module %q: %w", module.Name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *workspaceSchema) workspaceModule(
	ctx context.Context,
	parent *core.Query,
	args struct {
		Name       string
		Entrypoint bool
		Source     string
	},
) (*core.WorkspaceModule, error) {
	return &core.WorkspaceModule{
		Name:       args.Name,
		Entrypoint: args.Entrypoint,
		Source:     args.Source,
	}, nil
}
