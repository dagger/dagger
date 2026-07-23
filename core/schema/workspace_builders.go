package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type stagedWorkspaceConfig struct {
	Config     *workspace.Config
	Data       []byte
	ConfigDir  string
	ConfigFile string
}

type workspaceConfigMutationPolicy int

const (
	workspaceConfigMustExist workspaceConfigMutationPolicy = iota
	workspaceConfigInitIfMissing
)

func (s *workspaceSchema) loadWorkspaceConfigForOverlay(
	ctx context.Context,
	ws *core.Workspace,
	policy workspaceConfigMutationPolicy,
	here bool,
) (*stagedWorkspaceConfig, error) {
	if ws == nil {
		return nil, fmt.Errorf("workspace is required")
	}
	if ws.CompatWorkspace() != nil {
		return nil, fmt.Errorf("workspace is using legacy dagger.json config; run dagger setup first")
	}

	configDir := workspaceConfigDirectoryForWrite(ws, here)
	if ws.ConfigFile != "" && (!here || workspaceSameConfigDirectory(ws, workspaceConfigDirectoryForWrite(ws, true))) {
		configFile, err := workspaceConfigFile(ws)
		if err != nil {
			return nil, err
		}
		data, err := readConfigBytes(ctx, ws)
		if err != nil {
			return nil, err
		}
		cfg, err := workspace.ParseConfig(data)
		if err != nil {
			return nil, err
		}
		if cfg.Modules == nil {
			cfg.Modules = map[string]workspace.ModuleEntry{}
		}
		return &stagedWorkspaceConfig{
			Config:     cfg,
			Data:       data,
			ConfigDir:  filepath.Dir(configFile),
			ConfigFile: configFile,
		}, nil
	}

	if policy == workspaceConfigMustExist {
		return nil, fmt.Errorf("no dagger.toml found in workspace")
	}

	configFile := cleanWorkspaceRelPath(filepath.Join(configDir, workspace.ConfigFileName))
	cfg, err := workspace.ParseConfig([]byte(initialWorkspaceConfig))
	if err != nil {
		return nil, err
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}
	return &stagedWorkspaceConfig{
		Config:     cfg,
		Data:       []byte(initialWorkspaceConfig),
		ConfigDir:  cleanWorkspaceRelPath(configDir),
		ConfigFile: configFile,
	}, nil
}

func (s *workspaceSchema) stageWorkspaceConfigBytes(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	data []byte,
) (dagql.ObjectResult[*core.Workspace], error) {
	return s.stageWorkspaceConfigAndLock(ctx, parent, staged, data, nil)
}

func (s *workspaceSchema) stageWorkspaceConfigAndLock(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	data []byte,
	lock *workspaceOverlayLock,
) (dagql.ObjectResult[*core.Workspace], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	lockPath, lockData, lockChanged, err := lock.updatedFile()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	configPath := filepath.ToSlash(staged.ConfigFile)
	touched := []string{configPath}
	if lockChanged {
		touched = append(touched, filepath.ToSlash(lockPath))
	}
	return s.overlayEdit(ctx, parent, touched, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		updated, err := workspaceWithFile(ctx, dag, base, configPath, data)
		if err != nil {
			return dagql.ObjectResult[*core.Directory]{}, fmt.Errorf("stage workspace config update: %w", err)
		}
		if lockChanged {
			updated, err = workspaceWithFile(ctx, dag, updated, filepath.ToSlash(lockPath), lockData)
			if err != nil {
				return dagql.ObjectResult[*core.Directory]{}, fmt.Errorf("stage workspace lock update: %w", err)
			}
		}
		return updated, nil
	}, func(ws *core.Workspace) {
		setWorkspaceConfigSelection(ws, staged.ConfigDir)
	})
}

func (s *workspaceSchema) withConfigValue(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceConfigValueArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigInitIfMissing, args.Here)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	writeKey := args.Key
	if envName, ok := selectedWorkspaceEnv(ctx); ok && !isExplicitEnvConfigKey(args.Key) {
		writeKey, err = envScopedConfigKey(staged.Config, envName, args.Key, workspaceConfigInitIfMissing)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}

	var updated []byte
	if args.Values.Valid {
		if args.Value != "" {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("value and values are mutually exclusive")
		}
		elements := make([]string, 0, len(args.Values.Value))
		for _, v := range args.Values.Value {
			elements = append(elements, v.String())
		}
		updated, err = workspace.WriteConfigValues(staged.Data, writeKey, elements)
	} else {
		updated, err = workspace.WriteConfigValue(staged.Data, writeKey, args.Value)
	}
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

func (s *workspaceSchema) withoutConfigValue(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceConfigKeyArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigMustExist, args.Here)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	unsetKey := args.Key
	if envName, ok := selectedWorkspaceEnv(ctx); ok && !isExplicitEnvConfigKey(args.Key) {
		unsetKey, err = envScopedConfigKey(staged.Config, envName, args.Key, workspaceConfigMustExist)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}

	updated, err := workspace.DeleteConfigValue(staged.Data, unsetKey)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

func (s *workspaceSchema) withConfigEnv(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceConfigEnvArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.Name == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("environment name is required")
	}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigInitIfMissing, args.Here)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if !workspace.EnsureEnv(staged.Config, args.Name) {
		return parent, nil
	}
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

func (s *workspaceSchema) withoutConfigEnv(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceConfigEnvArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.Name == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("environment name is required")
	}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigMustExist, args.Here)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := workspace.RemoveEnv(staged.Config, args.Name); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

type workspaceModuleInstallArgs struct {
	Ref  string
	Name string `default:""`
	Here bool   `default:"false"`
}

func (s *workspaceSchema) withModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceModuleInstallArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	return s.withModuleInstall(ctx, parent, workspaceInstallArgs{
		Ref:  args.Ref,
		Name: args.Name,
		Here: args.Here,
	})
}

func (s *workspaceSchema) withModuleInstall(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceInstallArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigInitIfMissing, args.Here)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	selected, overlayLock, err := s.prepareWorkspaceOverlayLock(ctx, parent.Self(), staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	lookupCtx := withWorkspaceLookupLockOverride(ctx, overlayLock.Lock)
	resolved, err := s.resolveWorkspaceInstallForOverlay(lookupCtx, selected, args.Ref, args.Name, args.Here)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	plan, err := planWorkspaceInstallConfig(staged.Config, args, resolved.Name, resolved.ConfigSource)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if !plan.Changed {
		_, _, lockChanged, err := overlayLock.updatedFile()
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		if !lockChanged {
			return parent, nil
		}
		return s.stageWorkspaceConfigAndLock(ctx, parent, staged, staged.Data, overlayLock)
	}
	var hints map[string][]workspace.ConstructorArgHint
	if plan.Added {
		hints = collectWorkspaceSettingsHintsFromSource(lookupCtx, resolved.Name, resolved.ModuleSource)
	}
	updated, err := workspace.UpdateConfigBytesWithHints(staged.Data, staged.Config, hints)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.stageWorkspaceConfigAndLock(ctx, parent, staged, updated, overlayLock)
}

type workspaceSDKInstallArgs struct {
	Ref       string
	Name      string `default:""`
	Here      bool   `default:"false"`
	AsSdkName string `default:""`
}

func (s *workspaceSchema) withSDK(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceSDKInstallArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	return s.withModuleInstall(ctx, parent, workspaceInstallArgs{
		Ref:       args.Ref,
		Name:      args.Name,
		Here:      args.Here,
		AsSdk:     true,
		AsSdkName: args.AsSdkName,
	})
}

func (s *workspaceSchema) withoutModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceUninstallArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.Name == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module name is required")
	}

	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigMustExist, args.Here)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	entry, ok := staged.Config.Modules[args.Name]
	if !ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module %q is not installed in the workspace", args.Name)
	}

	managedModulePath, removeManagedModuleDir := removeSDKManagedModuleReference(staged.Config, staged.ConfigDir, entry)
	delete(staged.Config.Modules, args.Name)
	updatedConfig, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	configPath := filepath.ToSlash(staged.ConfigFile)
	managedDirPath := path.Clean(filepath.ToSlash(managedModulePath))
	touched := []string{configPath}
	if removeManagedModuleDir {
		touched = append(touched, managedDirPath)
	}
	return s.overlayEdit(ctx, parent, touched, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		updatedRoot, err := workspaceWithFile(ctx, dag, base, configPath, updatedConfig)
		if err != nil {
			return dagql.ObjectResult[*core.Directory]{}, fmt.Errorf("stage workspace config update: %w", err)
		}
		if removeManagedModuleDir {
			updatedRoot, err = workspaceMigrationSelectDirectory(ctx, updatedRoot, "withoutDirectory", []dagql.NamedInput{
				{Name: "path", Value: dagql.String(managedDirPath)},
			})
			if err != nil {
				return dagql.ObjectResult[*core.Directory]{}, fmt.Errorf("stage workspace directory removal %q: %w", managedModulePath, err)
			}
		}
		return updatedRoot, nil
	}, func(ws *core.Workspace) {
		setWorkspaceConfigSelection(ws, staged.ConfigDir)
	})
}

func (s *workspaceSchema) withoutSDK(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceUninstallArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	return s.withoutModule(ctx, parent, args)
}

func (s *workspaceSchema) workspaceWithChangeset(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	changes dagql.ObjectResult[*core.Changeset],
) (dagql.ObjectResult[*core.Workspace], error) {
	if changes.Self() == nil {
		return parent, nil
	}
	changesID, err := changes.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	touched, err := changesetTouchedPaths(ctx, changes.Self())
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.overlayEdit(ctx, parent, touched, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
		var updated dagql.ObjectResult[*core.Directory]
		err := srv.Select(ctx, base, &updated, dagql.Selector{
			Field: "withChanges",
			Args: []dagql.NamedInput{
				{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)},
			},
		})
		return updated, err
	}, nil)
}

func (s *workspaceSchema) withInitModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceInitModuleArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	changes, err := s.initModuleChanges(
		workspaceInstallContextWithLockMode(ctx, workspace.LockModeDisabled),
		parent,
		args,
	)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.workspaceWithChangeset(ctx, parent, changes)
}

func (s *workspaceSchema) withInitClient(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceInitClientArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	changes, err := s.initClientChanges(
		workspaceInstallContextWithLockMode(ctx, workspace.LockModeDisabled),
		parent,
		args,
	)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.workspaceWithChangeset(ctx, parent, changes)
}

func (s *workspaceSchema) withUpdatedLock(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	if ws.ConfigFile == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("no workspace config found")
	}

	operationCtx := ctx
	if ws.ClientID != "" {
		var err error
		operationCtx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace client context: %w", err)
		}
	}

	lock, err := s.readWorkspaceLockForOverlay(operationCtx, ws)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	query, err := core.CurrentQuery(operationCtx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := core.UpdateWorkspaceLock(operationCtx, query, lock); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace lock: %w", err)
	}

	changes, err := s.workspaceLockChangeset(operationCtx, ws, lock)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.workspaceWithChangeset(operationCtx, parent, changes)
}
