package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"sort"

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
	return s.overlayEdit(ctx, parent, touched, nil, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
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
	if envName, ok := selectedWorkspaceEnv(ctx, parent.Self()); ok && !isExplicitEnvConfigKey(args.Key) {
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
	if envName, ok := selectedWorkspaceEnv(ctx, parent.Self()); ok && !isExplicitEnvConfigKey(args.Key) {
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
	if !args.AsSdk {
		if sdkName, ok := workspace.SDKNameForModule(staged.Config, resolved.Name); ok {
			// A reinstall preserves the SDK's explicit workspace name.
			args.AsSdk = true
			args.AsSdkName = sdkName
		} else {
			isSDK, err := detectWorkspaceSDKCapabilities(lookupCtx, resolved.ModuleSource)
			if err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, err
			}
			if isSDK {
				args.AsSdk = true
			}
		}
	}

	var plan workspaceInstallConfigPlan
	if envName, ok := selectedWorkspaceEnv(ctx, parent.Self()); ok {
		plan, err = planWorkspaceEnvInstallConfig(staged.Config, envName, args, resolved.Name, resolved.ConfigSource)
	} else {
		plan, err = planWorkspaceInstallConfig(staged.Config, args, resolved.Name, resolved.ConfigSource)
	}
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
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
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
	// Reject before the ref is resolved and fetched; the plan keeps the same
	// check as a backstop for other callers.
	if envName, ok := selectedWorkspaceEnv(ctx, parent.Self()); ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDKs cannot be installed in env %q; install SDKs in the base workspace config", envName)
	}
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
	if envName, ok := selectedWorkspaceEnv(ctx, parent.Self()); ok {
		if _, installed := staged.Config.Modules[args.Name]; installed {
			if _, isSDK := workspace.SDKNameForModule(staged.Config, args.Name); isSDK {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDKs are not env-scoped; uninstall SDKs in the base workspace config")
			}
		}
		return s.withoutEnvModule(ctx, parent, staged, envName, args.Name)
	}
	entry, ok := staged.Config.Modules[args.Name]
	if !ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module %q is not installed in the workspace", args.Name)
	}

	managedModulePath, removeManagedModuleDir, err := removeSDKManagedModuleReference(staged.Config, staged.ConfigDir, args.Name, entry)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if sdkName, isSDK := workspace.SDKNameForModule(staged.Config, args.Name); isSDK {
		delete(staged.Config.SDKs, sdkName)
	}
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
	return s.overlayEdit(ctx, parent, touched, nil, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
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

// withoutEnvModule removes a module from the selected env's overlay only; the
// base config is never touched under an env selection. Removal is env-strict
// like unsets: the env must exist, and the module must be *installed* by it,
// i.e. its overlay entry carries a source. A settings-only overlay entry does
// not install anything, so it is cleared with `settings --unset`, not uninstall.
//
// Uninstalling removes the install aspect only: when the entry also carries
// settings and the base config still installs the module, the entry is kept as
// a settings-only overlay so the env's overrides survive. Without a base
// module those settings would apply to nothing, so the entry goes entirely.
func (s *workspaceSchema) withoutEnvModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	envName string,
	name string,
) (dagql.ObjectResult[*core.Workspace], error) {
	env, ok := staged.Config.Env[envName]
	if !ok {
		return dagql.ObjectResult[*core.Workspace]{}, workspace.NewUndefinedEnvError(staged.Config, envName)
	}
	entry, ok := env.Modules[name]
	if !ok || entry.Source == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module %q is not installed in env %q", name, envName)
	}
	_, baseInstalled := staged.Config.Modules[name]
	if len(entry.Settings) > 0 && baseInstalled {
		env.Modules[name] = workspace.EnvModuleOverlay{Settings: entry.Settings}
	} else {
		delete(env.Modules, name)
		if len(env.Modules) == 0 {
			env.Modules = nil
		}
	}
	staged.Config.Env[envName] = env
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.stageWorkspaceConfigBytes(ctx, parent, staged, updated)
}

func (s *workspaceSchema) withoutSDK(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceUninstallArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if _, ok := selectedWorkspaceEnv(ctx, parent.Self()); ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDKs are not env-scoped; uninstall SDKs in the base workspace config")
	}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigMustExist, args.Here)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	sdkName, _, _, err := installedSDKSource(staged.Config, args.Name)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	args.Name = staged.Config.SDKs[sdkName].Module
	return s.withoutModule(ctx, parent, args)
}

// workspaceWithFile overlays one workspace-root-relative file onto dir.
func workspaceWithFile(
	ctx context.Context,
	dag *dagql.Server,
	dir dagql.ObjectResult[*core.Directory],
	path string,
	contents []byte,
) (dagql.ObjectResult[*core.Directory], error) {
	var out dagql.ObjectResult[*core.Directory]
	err := dag.Select(ctx, dir, &out, dagql.Selector{
		Field: "withNewFile",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(path)},
			{Name: "contents", Value: dagql.String(contents)},
			{Name: "permissions", Value: dagql.Int(0o644)},
		},
	})
	return out, err
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
	return s.overlayEdit(ctx, parent, touched, nil, func(base dagql.ObjectResult[*core.Directory]) (dagql.ObjectResult[*core.Directory], error) {
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

type workspaceLockUpdateArgs struct {
	NoGenerate bool `default:"false"`
}

func (s *workspaceSchema) withUpdatedLock(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceLockUpdateArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	operationCtx := ctx
	if ws.ClientID != "" {
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
	// The update loop writes refreshed values itself. Keep the lock visible to
	// credential discovery, but make nested resolver calls ignore stale pins
	// and avoid writing entries of their own.
	updateCtx := withWorkspaceLookupLockRefresh(operationCtx)
	if err := core.UpdateWorkspaceLock(updateCtx, query, lock); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace lock: %w", err)
	}

	changes, err := s.workspaceLockChangeset(operationCtx, ws, lock)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	updated, err := s.workspaceWithChangeset(operationCtx, parent, changes)
	if err != nil || args.NoGenerate {
		return updated, err
	}
	selections, err := selectSDKModuleClients(staged, cleanWorkspaceRelPath(ws.Cwd), sdkModuleClientUpdateArgs{All: true})
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.generateSDKModuleClientSelections(ctx, updated, staged, selections)
}

type workspaceModuleUpdateArgs struct {
	Names []string `default:"[]"`
}

func (s *workspaceSchema) withUpdatedModules(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceModuleUpdateArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	effective := staged.Config
	if envName, ok := selectedWorkspaceEnv(ctx, ws); ok {
		effective, err = workspace.ApplyEnvOverlay(effective, envName)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}

	names := append([]string(nil), args.Names...)
	if len(names) == 0 {
		for name := range effective.Modules {
			names = append(names, name)
		}
	}
	sort.Strings(names)

	operationCtx := ctx
	if ws.ClientID != "" {
		operationCtx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace client context: %w", err)
		}
	}
	selected, overlayLock, err := s.prepareWorkspaceOverlayLock(operationCtx, ws, staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	refreshed := workspace.NewLock()
	refreshCtx := withWorkspaceLookupLockOverride(operationCtx, refreshed)
	moduleSources := make([]string, 0, len(names))
	for _, name := range names {
		entry, ok := effective.Modules[name]
		if !ok {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module %q is not installed in the workspace", name)
		}
		moduleSources = append(moduleSources, workspace.ResolveModuleEntrySource(staged.ConfigDir, entry.Source))
		if workspace.IsLocalRef(entry.Source, entry.Pin) {
			continue
		}
		if _, _, err := s.resolveWorkspaceInstallSource(refreshCtx, selected, entry.Source, staged.ConfigDir); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update module %q: %w", name, err)
		}
	}
	if err := overlayLock.Lock.Merge(refreshed); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("merge refreshed module lock entries: %w", err)
	}

	changes, err := s.workspaceLockChangeset(operationCtx, selected, overlayLock.Lock)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	updated, err := s.workspaceWithChangeset(operationCtx, parent, changes)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	selections, err := selectSDKModuleClientsForModuleSources(staged, moduleSources)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.generateSDKModuleClientSelections(ctx, updated, staged, selections)
}
