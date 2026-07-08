package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceModuleListArgs struct {
	Module string `default:""`
}

func (s *workspaceSchema) moduleList(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceModuleListArgs,
) (dagql.ObjectResultArray[*core.WorkspaceModule], error) {
	ws := parent.Self()
	if isSyntheticWorkspace(ws) {
		return dagql.ObjectResultArray[*core.WorkspaceModule]{}, nil
	}
	if ws.ConfigFile == "" {
		return nil, nil
	}

	cfg, err := readWorkspaceConfig(ctx, ws)
	if err != nil {
		return nil, err
	}

	configDir, err := workspaceConfigDirectory(ws)
	if err != nil {
		return nil, err
	}
	modules := make(core.WorkspaceModules, 0, len(cfg.Modules))
	for name, entry := range cfg.Modules {
		if args.Module != "" && name != args.Module {
			continue
		}
		source := filepath.ToSlash(workspace.ResolveModuleEntrySource(configDir, entry.Source))
		modules = append(modules, &core.WorkspaceModule{
			Name:       name,
			Entrypoint: entry.Entrypoint,
			Source:     source,
		})
	}
	if args.Module != "" && len(modules) == 0 {
		return nil, fmt.Errorf("module %q is not installed in the workspace", args.Module)
	}
	modules.Sort()

	results := make(dagql.ObjectResultArray[*core.WorkspaceModule], 0, len(modules))
	dag := dagql.CurrentDagqlServer(ctx)
	if dag == nil {
		return nil, fmt.Errorf("workspace module list: dagql server not found")
	}
	for _, module := range modules {
		var result dagql.ObjectResult[*core.WorkspaceModule]
		if err := dag.Select(ctx, parent, &result, dagql.Selector{
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
	parent *core.Workspace,
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

func (s *workspaceSchema) moduleSettings(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceModule],
	_ struct{},
) ([]*core.WorkspaceModuleSetting, error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	// moduleList creates WorkspaceModule results from Workspace.__workspaceModule,
	// so the DagQL receiver is the workspace that owns this module entry.
	receiver, err := parent.Receiver(ctx, srv)
	if err != nil {
		return nil, err
	}
	ws, ok := receiver.(dagql.ObjectResult[*core.Workspace])
	if !ok {
		return nil, fmt.Errorf("workspace module %q has unexpected receiver %T", parent.Self().Name, receiver)
	}
	if err := unsupportedSyntheticWorkspaceFeature(ws.Self(), "module settings"); err != nil {
		return nil, err
	}

	cfg, err := readWorkspaceConfig(ctx, ws.Self())
	if err != nil {
		return nil, err
	}

	// Source comes from base config; values come from the selected env overlay.
	effectiveCfg := cfg
	if envName, ok := selectedWorkspaceEnv(ctx); ok {
		effectiveCfg, err = workspace.ApplyEnvOverlay(cfg, envName)
		if err != nil {
			return nil, err
		}
	}

	entry, ok := cfg.Modules[parent.Self().Name]
	if !ok {
		return nil, fmt.Errorf("module %q is not installed in the workspace", parent.Self().Name)
	}

	configDir, err := workspaceConfigDirectory(ws.Self())
	if err != nil {
		return nil, err
	}
	if entry.Source == "" {
		return nil, nil
	}

	ctx, srv, err = workspaceSettingsHintIntrospectionContext(ctx, ws.Self())
	if err != nil {
		return nil, err
	}

	hints, err := introspectWorkspaceModuleSettings(ctx, srv, ws.Self(), configDir, entry.Source)
	if err != nil {
		return nil, fmt.Errorf("discover settings for module %q: %w", parent.Self().Name, err)
	}

	settings := make([]*core.WorkspaceModuleSetting, 0, len(hints))
	effectiveConfigBytes := workspace.SerializeConfig(effectiveCfg)
	for _, hint := range hints {
		value := ""
		if _, ok := effectiveCfg.Modules[parent.Self().Name].Settings[hint.Name]; ok {
			value, err = workspace.ReadConfigValue(effectiveConfigBytes, workspaceSettingConfigKey(parent.Self().Name, hint.Name))
			if err != nil {
				return nil, err
			}
		}
		settings = append(settings, &core.WorkspaceModuleSetting{
			Key:         hint.Name,
			Value:       value,
			Description: hint.Description,
		})
	}

	return settings, nil
}

func introspectWorkspaceModuleSettings(
	ctx context.Context,
	srv *dagql.Server,
	ws *core.Workspace,
	configDir string,
	source string,
) ([]workspace.ConstructorArgHint, error) {
	if core.FastModuleSourceKindCheck(source, "") != core.ModuleSourceKindLocal {
		return introspectConstructorArgs(ctx, srv, source)
	}

	resolvedSource := workspace.ResolveModuleEntrySource(configDir, source)
	if filepath.IsAbs(resolvedSource) {
		return introspectConstructorArgs(ctx, srv, resolvedSource)
	}
	if ws.HostPath() != "" {
		return introspectConstructorArgs(ctx, srv, filepath.Join(ws.HostPath(), resolvedSource))
	}
	if rootfs := ws.Rootfs(); rootfs.Self() != nil {
		return introspectConstructorArgsFromDirectory(ctx, srv, rootfs, resolvedSource)
	}
	return nil, fmt.Errorf("workspace project root is required for local module source %q", source)
}

func workspaceSettingConfigKey(moduleName, settingName string) string {
	return workspace.JoinConfigPath("modules", moduleName, "settings", settingName)
}
