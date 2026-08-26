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

	var plan workspaceInstallConfigPlan
	if envName, ok := selectedWorkspaceEnv(ctx); ok {
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
	if envName, ok := selectedWorkspaceEnv(ctx); ok {
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
	if envName, ok := selectedWorkspaceEnv(ctx); ok {
		return s.withoutEnvModule(ctx, parent, staged, envName, args.Name)
	}
	entry, ok := staged.Config.Modules[args.Name]
	if !ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module %q is not installed in the workspace", args.Name)
	}

	managedModulePath, removeManagedModuleDir, err := removeSDKManagedModuleReference(staged.Config, staged.ConfigDir, entry)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
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
	if _, ok := selectedWorkspaceEnv(ctx); ok {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDKs are not env-scoped; uninstall SDKs in the base workspace config")
	}
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

func (s *workspaceSchema) withInitModule(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceInitModuleArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	changes, scope, err := s.initModuleChanges(
		withoutWorkspaceLookupLock(ctx),
		parent,
		args,
	)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	updated, err := s.workspaceWithChangeset(ctx, parent, changes)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if args.NoGenerate {
		return updated, nil
	}
	return s.withScopedGeneration(ctx, updated, scope)
}

func (s *workspaceSchema) withInitClient(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args workspaceInitClientArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	changes, scope, err := s.initClientChanges(
		withoutWorkspaceLookupLock(ctx),
		parent,
		args,
	)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	updated, err := s.workspaceWithChangeset(ctx, parent, changes)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if args.NoGenerate {
		return updated, nil
	}
	return s.withScopedGeneration(ctx, updated, scope)
}

// initScope identifies what an init just created: the workspace-root-relative
// path of the new module or client, and the module entry name of the SDK that
// owns it.
type initScope struct {
	sdk  string
	path string
}

// sdkGenerators collects the generators an installed SDK exposes, reading them
// off the SDK's own module.
//
// Workspace.generators answers the same question for the workspace as a whole,
// but sources its modules from the ones the client loaded, so asking it for a
// single SDK means demanding workspace module loading. This loads the one SDK
// named and nothing else — the same load init already performs to scaffold with.
func (s *workspaceSchema) sdkGenerators(
	ctx context.Context,
	parentResult dagql.ObjectResult[*core.Workspace],
	args struct {
		SDK string
	},
) (*core.GeneratorGroup, error) {
	parent := parentResult.Self()
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent, workspaceConfigMustExist, false)
	if err != nil {
		return nil, err
	}
	sdkName, _, sdkRef, err := installedSDKSource(staged.Config, args.SDK)
	if err != nil {
		return nil, err
	}
	loaded, err := s.loadWorkspaceSDK(ctx, parent, staged.ConfigDir, sdkRef)
	if err != nil {
		return nil, fmt.Errorf("load SDK %q: %w", sdkName, err)
	}
	mod, ok := loaded.AsModule()
	if !ok {
		// A builtin SDK is a packaged binary, not a module, so it has no
		// generator functions to expose.
		return &core.GeneratorGroup{}, nil
	}
	mod, err = moduleUnderWorkspaceName(ctx, mod, sdkName)
	if err != nil {
		return nil, fmt.Errorf("load SDK %q as %q: %w", sdkRef, sdkName, err)
	}

	gg, err := core.NewGeneratorGroup(ctx, mod, nil)
	if err != nil {
		return nil, fmt.Errorf("generators from SDK %q: %w", sdkName, err)
	}
	// Name the tree after the workspace entry, so generator paths read the same
	// as they do under `dagger generate <sdk>`.
	reparentWorkspaceTreeRoot(gg.Node, sdkName)
	// The SDK compares the workspace it returns with this exact input workspace,
	// so it sees every staged init edit while returning only its own generated
	// files.
	gg.BoundWorkspace = parentResult
	return gg, nil
}

// moduleUnderWorkspaceName reloads mod under the name its workspace entry gives
// it, matching how workspace module loading installs it.
//
// The name is load-bearing, not cosmetic: currentModule.asSDK — which every SDK
// generator calls to find the modules and clients it manages — matches the
// running module's name against the dagger.toml entries. An SDK loaded from its
// ref carries its own name ("go-sdk"), so without this it fails to match its
// entry ("dagger-go-sdk") and reports that it is not installed as an SDK.
func moduleUnderWorkspaceName(
	ctx context.Context,
	mod dagql.ObjectResult[*core.Module],
	name string,
) (dagql.ObjectResult[*core.Module], error) {
	if mod.Self().Name() == name {
		return mod, nil
	}
	src := mod.Self().Source
	if !src.Valid {
		return mod, fmt.Errorf("module has no source")
	}
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return mod, err
	}
	var renamed dagql.ObjectResult[*core.Module]
	if err := dag.Select(ctx, src.Value, &renamed,
		dagql.Selector{
			Field: "withName",
			Args:  []dagql.NamedInput{{Name: "name", Value: dagql.String(name)}},
		},
		dagql.Selector{Field: "asModule"},
	); err != nil {
		return mod, err
	}
	return renamed, nil
}

// withScopedGeneration runs the owning SDK's generators against updated with
// scope.path as the workspace cwd, and folds their output back into it — so an
// init returns a single changeset carrying both the scaffold and the generated
// code it needs to be loadable.
//
// Scoping is by cwd, not by generator selection: every SDK's @generate is
// anchored at the workspace cwd (it generates the module it's in plus the ones
// beneath, and only the clients under that path), so pointing it at what was
// just created is what keeps the rest of the workspace untouched. This is the
// same mechanism the internal local-dependency generator uses per dependency.
//
// The generators come from __sdkGenerators, which reads them off the SDK's own
// module, rather than from Workspace.generators, which resolves them out of the
// client's loaded workspace modules. Init already loads the SDK to scaffold
// with, so the former needs nothing else; the latter would make init demand
// workspace module loading for a set it only ever filters back down to this one
// SDK.
func (s *workspaceSchema) withScopedGeneration(
	ctx context.Context,
	updated dagql.ObjectResult[*core.Workspace],
	scope initScope,
) (dagql.ObjectResult[*core.Workspace], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	// Nothing to stage: what was just created has no already-generated local
	// dependency closure to carry in.
	scoped, err := scopedStagedWorkspace(ctx, dag, updated, scope.path, dagql.ObjectResult[*core.Changeset]{})
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("scope workspace to %q: %w", scope.path, err)
	}

	// Resolved on the scoped workspace, because the group binds to its receiver:
	// that is what carries both the post-init overlay — where the entry init just
	// recorded in dagger.toml lives, without which the generator would not see the
	// new module or client — and the cwd that scopes it.
	var generators dagql.ObjectResult[*core.GeneratorGroup]
	if err := dag.Select(ctx, scoped, &generators, dagql.Selector{
		Field: "__sdkGenerators",
		Args: []dagql.NamedInput{
			{Name: "sdk", Value: dagql.String(scope.sdk)},
		},
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if len(generators.Self().Generators) == 0 {
		// An SDK that exposes no generator has nothing to contribute; the
		// config and scaffold init planned still stand.
		return updated, nil
	}

	var generated dagql.ObjectResult[*core.Changeset]
	if err := dag.Select(ctx, generators, &generated,
		dagql.Selector{Field: "run"},
		dagql.Selector{Field: "changes"},
	); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	// Generation ran with scope.path as cwd, so its changeset is rooted there.
	// Re-root it to the workspace root so it overlays in the right place.
	generated, err = rerootChangesetUnder(ctx, dag, generated, scope.path)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("reroot changeset under %q: %w", scope.path, err)
	}
	return s.workspaceWithChangeset(ctx, updated, generated)
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
	return s.workspaceWithChangeset(operationCtx, parent, changes)
}
