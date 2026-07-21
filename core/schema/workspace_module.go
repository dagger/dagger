package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) workspaceModules(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	name string,
) (dagql.ObjectResultArray[*core.WorkspaceModule], error) {
	ws := parent.Self()
	if ws.ConfigFile == "" {
		return dagql.ObjectResultArray[*core.WorkspaceModule]{}, nil
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
	for moduleName, entry := range cfg.Modules {
		if name != "" && moduleName != name {
			continue
		}
		source := filepath.ToSlash(workspace.ResolveModuleEntrySource(configDir, entry.Source))
		modules = append(modules, &core.WorkspaceModule{
			Name:       moduleName,
			Entrypoint: entry.Entrypoint,
			Source:     source,
		})
	}
	if name != "" && len(modules) == 0 {
		return nil, fmt.Errorf("module %q is not installed in the workspace", name)
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

func (s *workspaceSchema) modules(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResultArray[*core.WorkspaceModule], error) {
	return s.workspaceModules(ctx, parent, "")
}

func (s *workspaceSchema) module(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		Name string
	},
) (dagql.ObjectResult[*core.WorkspaceModule], error) {
	if args.Name == "" {
		return dagql.ObjectResult[*core.WorkspaceModule]{}, fmt.Errorf("module name is required")
	}
	modules, err := s.workspaceModules(ctx, parent, args.Name)
	if err != nil {
		return dagql.ObjectResult[*core.WorkspaceModule]{}, err
	}
	return modules[0], nil
}

type workspaceModuleSourceArgs struct {
	Path string
}

// moduleSource loads a module source from a path within the workspace, applying
// the standard workspace path rules (absolute from the workspace root, relative
// from the workspace cwd). The whole workspace tree is materialized so the
// module's dagger.json and dependency include paths resolve; asModuleSource then
// scopes to sourceRootPath. Host reads route to the workspace owner via
// workspaceOverlayRootfs, so this works both from the session that owns the
// workspace and from a module that received the workspace as an argument.
func (s *workspaceSchema) moduleSource(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceModuleSourceArgs,
) (inst dagql.ObjectResult[*core.ModuleSource], _ error) {
	ws := parent.Self()
	resolvedPath, err := resolveWorkspacePath(args.Path, ws.Cwd)
	if err != nil {
		return inst, err
	}

	root, err := s.workspaceOverlayRootfs(ctx, ws)
	if err != nil {
		return inst, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return inst, err
	}

	// asModuleSource errors if the resolved path holds no module config, so it
	// doubles as the "path is not an initialized module" check.
	if err := srv.Select(ctx, root, &inst, dagql.Selector{
		Field: "asModuleSource",
		Args: []dagql.NamedInput{
			{Name: "sourceRootPath", Value: dagql.String(filepath.ToSlash(resolvedPath))},
		},
	}); err != nil {
		return inst, fmt.Errorf("workspace module source %q: %w", args.Path, err)
	}
	return inst, nil
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

	// modules creates WorkspaceModule results from Workspace.__workspaceModule,
	// so the DagQL receiver is the workspace that owns this module entry.
	receiver, err := parent.Receiver(ctx, srv)
	if err != nil {
		return nil, err
	}
	ws, ok := receiver.(dagql.ObjectResult[*core.Workspace])
	if !ok {
		return nil, fmt.Errorf("workspace module %q has unexpected receiver %T", parent.Self().Name, receiver)
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
			IsList:      hint.IsList,
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
) ([]constructorArgHint, error) {
	if core.FastModuleSourceKindCheck(source, "") != core.ModuleSourceKindLocal {
		return introspectConstructorArgs(ctx, srv, source)
	}

	resolvedSource := workspace.ResolveModuleEntrySource(configDir, source)
	if filepath.IsAbs(resolvedSource) {
		return introspectConstructorArgs(ctx, srv, resolvedSource)
	}
	if rootfs, ok := ws.SourceDirectory(); ok && rootfs.Self() != nil {
		return introspectConstructorArgsFromDirectory(ctx, srv, rootfs, resolvedSource)
	}
	if ws.HostPath() != "" {
		return introspectConstructorArgs(ctx, srv, filepath.Join(ws.HostPath(), resolvedSource))
	}
	return nil, fmt.Errorf("workspace project root is required for local module source %q", source)
}

func workspaceSettingConfigKey(moduleName, settingName string) string {
	return workspace.JoinConfigPath("modules", moduleName, "settings", settingName)
}
