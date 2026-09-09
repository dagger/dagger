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
		// An env selection has nowhere to resolve from without a config, and
		// silently listing nothing would read as "the env is empty".
		if envName, ok := selectedWorkspaceEnv(ctx, ws); ok {
			return nil, fmt.Errorf("workspace env %q requires dagger.toml", envName)
		}
		return dagql.ObjectResultArray[*core.WorkspaceModule]{}, nil
	}

	cfg, err := readWorkspaceConfig(ctx, ws)
	if err != nil {
		return nil, err
	}
	// The listing is the effective view, merged in the same order as module
	// loading (base, user-level overlay, selected env overlay), so modules an
	// overlay adds are discoverable. A missing or broken selected env fails the
	// read instead of falling back to the base config.
	cfg, err = effectiveWorkspaceConfig(ctx, ws, cfg)
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
// from the workspace cwd). Local and Git workspaces retain their backing source
// and pending overlay; a genuinely Directory-backed workspace remains a
// DIR_SOURCE.
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

	switch ws.BaseSource().(type) {
	case *core.WorkspaceSourceClientLocal, *core.WorkspaceSourceGitRef:
		return (&moduleSourceSchema{}).workspaceModuleSource(ctx, parent, filepath.ToSlash(resolvedPath))
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

// loadWorkspaceModule loads the module behind a WorkspaceModule
// result, with the effective (overlay-merged) workspace config. mod is nil
// when the config entry has no source.
func (s *workspaceSchema) loadWorkspaceModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceModule],
) (mod *core.Module, effectiveCfg *workspace.Config, err error) {
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, nil, err
	}

	// modules creates WorkspaceModule results from Workspace.__workspaceModule,
	// so the DagQL receiver is the workspace that owns this module entry.
	receiver, err := parent.Receiver(ctx, srv)
	if err != nil {
		return nil, nil, err
	}
	wsResult, ok := receiver.(dagql.ObjectResult[*core.Workspace])
	if !ok {
		return nil, nil, fmt.Errorf("workspace module %q has unexpected receiver %T", parent.Self().Name, receiver)
	}
	ws := wsResult.Self()
	cfg, err := readWorkspaceConfig(ctx, ws)
	if err != nil {
		return nil, nil, err
	}

	// Values come from the user-level overlay and the selected env overlay,
	// merged in the same order as module loading. The entry lookup is also
	// effective so modules an overlay itself adds resolve their settings.
	effectiveCfg, err = effectiveWorkspaceConfig(ctx, ws, cfg)
	if err != nil {
		return nil, nil, err
	}

	entry, ok := effectiveCfg.Modules[parent.Self().Name]
	if !ok {
		return nil, nil, fmt.Errorf("module %q is not installed in the workspace", parent.Self().Name)
	}
	if entry.Source == "" {
		return nil, effectiveCfg, nil
	}

	configDir, err := workspaceConfigDirectory(ws)
	if err != nil {
		return nil, nil, err
	}
	ctx, srv, err = workspaceSettingsHintIntrospectionContext(ctx, ws)
	if err != nil {
		return nil, nil, err
	}
	mod, err = introspectWorkspaceModule(ctx, srv, ws, configDir, entry.Source)
	if err != nil {
		return nil, nil, fmt.Errorf("introspect module %q: %w", parent.Self().Name, err)
	}
	return mod, effectiveCfg, nil
}

func (s *workspaceSchema) moduleSettings(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceModule],
	_ struct{},
) ([]*core.WorkspaceModuleSetting, error) {
	mod, effectiveCfg, err := s.loadWorkspaceModule(ctx, parent)
	if err != nil {
		return nil, err
	}
	if mod == nil {
		return nil, nil
	}
	hints := constructorHintsFromModule(mod)

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
			IsObject:    hint.IsObject,
		})
	}

	return settings, nil
}

// moduleFunctions lists the main object's functions in GraphQL field form.
func (s *workspaceSchema) moduleFunctions(
	ctx context.Context,
	parent dagql.ObjectResult[*core.WorkspaceModule],
	_ struct{},
) (dagql.Array[dagql.String], error) {
	mod, _, err := s.loadWorkspaceModule(ctx, parent)
	if err != nil {
		return nil, err
	}
	return dagql.NewStringArray(mainObjectFunctionNames(mod)...), nil
}

// introspectWorkspaceModule loads the module behind a config entry's source.
func introspectWorkspaceModule(
	ctx context.Context,
	srv *dagql.Server,
	ws *core.Workspace,
	configDir string,
	source string,
) (*core.Module, error) {
	if core.FastModuleSourceKindCheck(source, "") != core.ModuleSourceKindLocal {
		return introspectModule(ctx, srv, source)
	}

	resolvedSource := workspace.ResolveModuleEntrySource(configDir, source)
	if filepath.IsAbs(resolvedSource) {
		return introspectModule(ctx, srv, resolvedSource)
	}
	if rootfs, ok := ws.SourceDirectory(); ok && rootfs.Self() != nil {
		return introspectModuleFromDirectory(ctx, srv, rootfs, resolvedSource)
	}
	if ws.HostPath() != "" {
		return introspectModule(ctx, srv, filepath.Join(ws.HostPath(), resolvedSource))
	}
	return nil, fmt.Errorf("workspace project root is required for local module source %q", source)
}

func workspaceSettingConfigKey(moduleName, settingName string) string {
	return workspace.JoinConfigPath("modules", moduleName, "settings", settingName)
}
