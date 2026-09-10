package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
)

type sdkModuleInitArgs struct {
	SDK        string
	Name       string    `default:""`
	Path       string    `default:""`
	Settings   core.JSON `default:""`
	Install    dagql.Optional[dagql.Boolean]
	Entrypoint dagql.Optional[dagql.Boolean]
}

type sdkModuleDetectScopeArgs struct {
	SDK string
}

type sdkModuleClientAddArgs struct {
	Module   string
	SDK      string    `default:""`
	Settings core.JSON `default:""`
}

type sdkModuleClientRemoveArgs struct {
	Module string
	SDK    string `default:""`
}

type sdkModuleClientUpdateArgs struct {
	Modules []string `default:"[]"`
	All     bool     `default:"false"`
	SDK     string   `default:""`
}

type selectedSDKModule struct {
	name     string
	entry    workspace.SDKEntry
	provider workspace.ModuleEntry
	ref      string
}

// withSDKModuleInitialized records the selected location as a module scope and
// reconciles all SDK-managed state in that scope.
func (s *workspaceSchema) withSDKModuleInitialized(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args sdkModuleInitArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	install, entrypoint := optionalInitControl(args.Install), optionalInitControl(args.Entrypoint)
	if _, err := workspace.PlanModuleInit(args.Path != "", args.Name != "", install, entrypoint); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	ws := parent.Self()
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	if strings.TrimSpace(args.SDK) == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK name is required")
	}
	scopePath, moduleName, explicitPath, err := resolveSDKModuleInit(ws, args.Path, args.Name)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	selected, err := selectSDKModule(staged.Config, args.SDK)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	explicitSettings, err := decodeSDKModuleSettings(args.Settings)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if !explicitPath {
		sdkPath, err := s.sdkDefaultModulePath(ctx, parent, staged, selected, explicitSettings, moduleName)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		if sdkPath != "" {
			scopePath = sdkPath
		}
	}

	configScopePath, _, err := workspace.SDKScopeKey(selected.entry, staged.ConfigDir, scopePath)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	modulePath, err := workspace.SDKManagedPathFor(staged.ConfigDir, scopePath)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := planSDKModuleInitInstall(staged.Config, moduleName, modulePath, explicitPath, args.Name != "", install, entrypoint); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if owner, found, err := moduleScopeOwner(staged.Config, staged.ConfigDir, scopePath); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	} else if found && owner != selected.name {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module scope %q is already managed by SDK %q", scopePath, owner)
	}
	entry := staged.Config.SDKs[selected.name]
	if entry.Scopes == nil {
		entry.Scopes = map[string]workspace.SDKScope{}
	}
	scope := entry.Scopes[configScopePath]
	scope.IsModule = true
	if args.Name != "" {
		scope.Name = args.Name
	} else if scope.Name == "" && (install != nil || entrypoint != nil) {
		inferredName, err := resolveSDKModuleName(ws, staged.Config, staged.ConfigDir, scopePath, scope.Name)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		if inferredName != moduleName {
			scope.Name = moduleName
		}
	}
	mergeSDKModuleSettings(&scope, explicitSettings)
	entry.Scopes[configScopePath] = scope
	staged.Config.SDKs[selected.name] = entry
	if err := validateSDKModuleGenerationGraph(staged.Config, staged.ConfigDir); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	updated, err := s.stageSDKModuleConfig(ctx, parent, staged, nil)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.generateSDKModuleScope(ctx, updated, staged, selected, configScopePath, scopePath, scope)
}

func optionalInitControl(value dagql.Optional[dagql.Boolean]) *bool {
	if !value.Valid {
		return nil
	}
	result := bool(value.Value)
	return &result
}

func planSDKModuleInitInstall(cfg *workspace.Config, moduleName, sourcePath string, explicitPath, explicitName bool, install, entrypoint *bool) error {
	plan, err := workspace.PlanModuleInit(explicitPath, explicitName, install, entrypoint)
	if err != nil {
		return err
	}
	if !plan.Install {
		return nil
	}
	if plan.AutomaticEntrypoint {
		var entrypoints []string
		for name, entry := range cfg.Modules {
			if name != moduleName && entry.Entrypoint {
				entrypoints = append(entrypoints, name)
			}
		}
		if len(entrypoints) > 0 {
			sort.Strings(entrypoints)
			return fmt.Errorf(
				"workspace already has entrypoint module %q; pass --name to initialize an additional namespaced module",
				entrypoints[0],
			)
		}
	}

	if _, err := planWorkspaceInstallConfig(cfg, workspaceInstallArgs{}, moduleName, sourcePath); err != nil {
		return err
	}
	if plan.Entrypoint {
		return workspace.SetEntrypoint(cfg, moduleName)
	}
	return nil
}

// resolveSDKModuleInit resolves the module name and the path to use when the
// selected SDK does not choose one. The explicit result reports whether the
// user passed --path, in which case no SDK may override the result.
func resolveSDKModuleInit(ws *core.Workspace, pathArg, nameArg string) (string, string, bool, error) {
	if ws == nil {
		return "", "", false, fmt.Errorf("workspace is required")
	}

	var explicitPath string
	if pathArg != "" {
		var err error
		explicitPath, err = resolveWorkspacePath(pathArg, ws.Cwd)
		if err != nil {
			return "", "", false, fmt.Errorf("module path: %w", err)
		}
		explicitPath = cleanWorkspaceRelPath(explicitPath)
	}

	configDir := "."
	if ws.ConfigFile != "" {
		configDir = filepath.Dir(cleanWorkspaceRelPath(ws.ConfigFile))
	}
	moduleName, err := resolveSDKModuleName(ws, nil, configDir, explicitPath, nameArg)
	if err != nil {
		return "", "", false, err
	}

	scopePath := explicitPath
	if pathArg == "" {
		scopePath = filepath.Join(configDir, ".dagger", "modules", moduleName)
	}
	scopePath = cleanWorkspaceRelPath(scopePath)
	return scopePath, moduleName, pathArg != "", nil
}

// resolveSDKModuleName computes the name without changing the saved scope.
// Default init records an entrypoint installation, which preserves its name
// even when the SDK chooses a different directory for the module.
func resolveSDKModuleName(ws *core.Workspace, cfg *workspace.Config, configDir, scopePath, name string) (string, error) {
	if name == "" && cfg != nil && scopePath != "" {
		var entrypoints []string
		for installedName, entry := range cfg.Modules {
			if !entry.Entrypoint || !workspace.IsLocalRef(entry.Source, entry.Pin) {
				continue
			}
			sourcePath, err := workspace.ResolveSDKManagedPath(configDir, entry.Source)
			if err != nil {
				return "", fmt.Errorf("entrypoint module %q: %w", installedName, err)
			}
			if sourcePath == cleanWorkspaceRelPath(scopePath) {
				entrypoints = append(entrypoints, installedName)
			}
		}
		if len(entrypoints) > 1 {
			sort.Strings(entrypoints)
			return "", fmt.Errorf("module scope %q has multiple entrypoint names %q; set an explicit scope name", scopePath, entrypoints)
		}
		if len(entrypoints) == 1 {
			name = entrypoints[0]
		}
	}
	return workspace.ModuleInitName(name, scopePath, configDir, workspaceRootDirectoryName(ws))
}

func workspaceRootDirectoryName(ws *core.Workspace) string {
	return workspace.ModuleRootDirectoryName(ws.HostPath(), ws.Address, ws.Cwd)
}

// sdkModuleDetectScope returns the selected SDK module's current scope for the
// Workspace.cwd.
func (s *workspaceSchema) sdkModuleDetectScope(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args sdkModuleDetectScopeArgs,
) (string, error) {
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigMustExist, false)
	if err != nil {
		return "", err
	}
	selection, err := s.resolveCurrentSDKModuleScope(ctx, parent, staged, args.SDK)
	if err != nil {
		return "", err
	}
	return selection.scope, nil
}

// withSDKModuleClient adds one client record and reconciles its SDK scope.
func (s *workspaceSchema) withSDKModuleClient(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args sdkModuleClientAddArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.Module == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module target is required")
	}
	if _, err := clientModuleRefKind(args.Module); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if len(args.Settings) > 0 && args.SDK == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK name is required when client settings are supplied")
	}
	ws := parent.Self()
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	currentScope, err := s.resolveSDKModuleClientScope(ctx, parent, staged, args.SDK)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if currentScope.scope == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client generation is not available from workspace cwd %q", cleanWorkspaceRelPath(ws.Cwd))
	}

	operationCtx := ctx
	if ws.ClientID != "" {
		operationCtx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace client context: %w", err)
		}
	}
	_, overlayLock, err := s.prepareWorkspaceOverlayLock(operationCtx, ws, staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	operationCtx = withWorkspaceLookupLockOverride(operationCtx, overlayLock.Lock)
	moduleLoadRef, configTarget, err := resolveWorkspaceClientModuleInput(
		staged.ConfigDir,
		ws.Cwd,
		args.Module,
	)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if _, err := s.resolveClientTargetModule(operationCtx, parent, moduleLoadRef); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	configScopePath := currentScope.configScopePath
	explicitSettings, err := decodeSDKModuleSettings(args.Settings)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	entry := staged.Config.SDKs[currentScope.sdk.name]
	if entry.Scopes == nil {
		entry.Scopes = map[string]workspace.SDKScope{}
	}
	scope := entry.Scopes[configScopePath]
	for _, recorded := range scope.Clients {
		loadRef, err := resolveSDKManagedClientModule(staged.ConfigDir, recorded)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		if loadRef == moduleLoadRef {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client target %q already exists in SDK %q scope %q", recorded, currentScope.sdk.name, currentScope.scope)
		}
	}
	scope.Clients = append(scope.Clients, configTarget)
	mergeSDKModuleSettings(&scope, explicitSettings)
	entry.Scopes[configScopePath] = scope
	staged.Config.SDKs[currentScope.sdk.name] = entry
	if err := validateSDKModuleGenerationGraph(staged.Config, staged.ConfigDir); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	updated, err := s.stageSDKModuleConfig(ctx, parent, staged, overlayLock)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.generateSDKModuleScope(ctx, updated, staged, currentScope.sdk, configScopePath, currentScope.scope, scope)
}

// withoutSDKModuleClient removes one client record and reconciles its SDK
// scope. The SDK provider owns removal of obsolete generated files.
func (s *workspaceSchema) withoutSDKModuleClient(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args sdkModuleClientRemoveArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	if args.Module == "" {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("module target is required")
	}
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, parent.Self(), workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	requestedSDK := args.SDK
	if requestedSDK != "" {
		selected, err := selectSDKModule(staged.Config, requestedSDK)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		requestedSDK = selected.name
	}
	var matches []resolvedSDKModuleScope
	cwd := cleanWorkspaceRelPath(parent.Self().Cwd)
	for sdkName, entry := range staged.Config.SDKs {
		if requestedSDK != "" && sdkName != requestedSDK {
			continue
		}
		for configScopePath, scope := range entry.Scopes {
			if !slices.Contains(scope.Clients, args.Module) {
				continue
			}
			workspaceScope, err := workspace.ResolveSDKManagedPath(staged.ConfigDir, configScopePath)
			if err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK %q scope: %w", sdkName, err)
			}
			if workspacePathContains(workspaceScope, cwd) {
				matches = append(matches, resolvedSDKModuleScope{
					sdk:             selectedSDKModule{name: sdkName},
					scope:           cleanWorkspaceRelPath(workspaceScope),
					configScopePath: configScopePath,
				})
			}
		}
	}
	if len(matches) == 0 {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client target %q is not recorded for the current scope", args.Module)
	}
	winner, err := selectDeepestSDKModuleScope(matches)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client target %q: %w", args.Module, err)
	}
	entry := staged.Config.SDKs[winner.sdk.name]
	scope := entry.Scopes[winner.configScopePath]
	scope.Clients = slices.DeleteFunc(scope.Clients, func(target string) bool { return target == args.Module })
	entry.Scopes[winner.configScopePath] = scope
	staged.Config.SDKs[winner.sdk.name] = entry
	updated, err := s.stageSDKModuleConfig(ctx, parent, staged, nil)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	var invalid []string
	for _, target := range scope.Clients {
		if _, err := resolveSDKManagedClientModule(staged.ConfigDir, target); err != nil {
			invalid = append(invalid, fmt.Sprintf("%q", target))
		}
	}
	if len(invalid) > 0 {
		slog.GlobalLogger(ctx, core.InstrumentationLibrary).Warn(fmt.Sprintf(
			"SDK %q scope %q: client removed; generation skipped because these targets are invalid: %s. Use explicit local paths within the workspace or module addresses, or remove the targets with 'dagger module client rm'.",
			winner.sdk.name, winner.scope, strings.Join(invalid, ", "),
		))
		return updated, nil
	}
	selected, err := selectSDKModule(staged.Config, winner.sdk.name)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.generateSDKModuleScope(
		ctx,
		updated,
		staged,
		selected,
		winner.configScopePath,
		winner.scope,
		scope,
	)
}

// sdkModuleClientSelection is one scope with the client targets that a client
// update selected inside it.
type sdkModuleClientSelection struct {
	sdkName         string
	configScopePath string
	workspaceScope  string
	scope           workspace.SDKScope
	targets         []string
}

// selectSDKModuleClients collects the scopes and client targets that a client
// update applies to. Without All, only scopes that contain the workspace cwd
// are eligible, matching dagger module client list.
func selectSDKModuleClients(
	staged *stagedWorkspaceConfig,
	cwd string,
	args sdkModuleClientUpdateArgs,
) ([]sdkModuleClientSelection, error) {
	return selectSDKModuleClientsWhere(staged, cwd, args, nil)
}

func selectSDKModuleClientsWhere(
	staged *stagedWorkspaceConfig,
	cwd string,
	args sdkModuleClientUpdateArgs,
	includeTarget func(string) (bool, error),
) ([]sdkModuleClientSelection, error) {
	matched := make(map[string]bool, len(args.Modules))
	sdkNames := make([]string, 0, len(staged.Config.SDKs))
	for name := range staged.Config.SDKs {
		sdkNames = append(sdkNames, name)
	}
	sort.Strings(sdkNames)

	var selections []sdkModuleClientSelection
	for _, sdkName := range sdkNames {
		if args.SDK != "" && sdkName != args.SDK {
			continue
		}
		entry := staged.Config.SDKs[sdkName]
		configScopePaths := make([]string, 0, len(entry.Scopes))
		for configScopePath := range entry.Scopes {
			configScopePaths = append(configScopePaths, configScopePath)
		}
		sort.Strings(configScopePaths)
		for _, configScopePath := range configScopePaths {
			scope := entry.Scopes[configScopePath]
			workspaceScope, err := workspace.ResolveSDKManagedPath(staged.ConfigDir, configScopePath)
			if err != nil {
				return nil, fmt.Errorf("SDK %q scope: %w", sdkName, err)
			}
			if !args.All && !workspacePathContains(workspaceScope, cwd) {
				continue
			}
			var targets []string
			for _, target := range scope.Clients {
				if len(args.Modules) > 0 && !slices.Contains(args.Modules, target) {
					continue
				}
				if includeTarget != nil {
					include, err := includeTarget(target)
					if err != nil {
						return nil, err
					}
					if !include {
						continue
					}
				}
				matched[target] = true
				targets = append(targets, target)
			}
			if len(targets) == 0 {
				continue
			}
			selections = append(selections, sdkModuleClientSelection{
				sdkName:         sdkName,
				configScopePath: configScopePath,
				workspaceScope:  workspaceScope,
				scope:           scope,
				targets:         targets,
			})
		}
	}
	for _, target := range args.Modules {
		if !matched[target] {
			return nil, fmt.Errorf("client target %q is not recorded for the selected scopes", target)
		}
	}
	return selections, nil
}

// withUpdatedSDKModuleClients re-reads the source of each selected client
// target and regenerates every scope that owns one. The refresh resolves into
// an empty lock and merges the result, so it writes only the lock entries that
// the selected targets reach. Local targets carry no pin and are generated
// without a refresh.
func (s *workspaceSchema) withUpdatedSDKModuleClients(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args sdkModuleClientUpdateArgs,
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	selections, err := selectSDKModuleClients(staged, cleanWorkspaceRelPath(ws.Cwd), args)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if len(selections) == 0 {
		return parent, nil
	}

	operationCtx := ctx
	if ws.ClientID != "" {
		operationCtx, err = s.withWorkspaceClientContext(ctx, ws)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("workspace client context: %w", err)
		}
	}
	_, overlayLock, err := s.prepareWorkspaceOverlayLock(operationCtx, ws, staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	refreshed := workspace.NewLock()
	refreshCtx := withWorkspaceLookupLockOverride(operationCtx, refreshed)
	for _, selection := range selections {
		for _, target := range selection.targets {
			loadRef, err := resolveSDKManagedClientModule(staged.ConfigDir, target)
			if err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client target %q: %w", target, err)
			}
			if workspace.IsLocalRef(loadRef, "") {
				continue
			}
			if _, err := s.resolveClientTargetModule(refreshCtx, parent, loadRef); err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update client %q: %w", target, err)
			}
		}
	}
	if err := overlayLock.Lock.Merge(refreshed); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("merge refreshed client lock entries: %w", err)
	}

	updated, err := s.stageSDKModuleConfig(ctx, parent, staged, overlayLock)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.generateSDKModuleClientSelections(ctx, updated, staged, selections)
}

func (s *workspaceSchema) generateSDKModuleClientSelections(
	ctx context.Context,
	updated dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	selections []sdkModuleClientSelection,
) (dagql.ObjectResult[*core.Workspace], error) {
	selections, err := orderSDKModuleClientSelections(staged, selections)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	for _, selection := range selections {
		selected, err := selectSDKModule(staged.Config, selection.sdkName)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		updated, err = s.generateSDKModuleScope(
			ctx,
			updated,
			staged,
			selected,
			selection.configScopePath,
			selection.workspaceScope,
			selection.scope,
		)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}
	return updated, nil
}

func orderSDKModuleClientSelections(
	staged *stagedWorkspaceConfig,
	selections []sdkModuleClientSelection,
) ([]sdkModuleClientSelection, error) {
	if len(selections) < 2 {
		return selections, nil
	}

	selectedProviders := make(map[string]bool, len(selections))
	selectionByScope := make(map[string]sdkModuleClientSelection, len(selections))
	for _, selection := range selections {
		selectedProviders[selection.sdkName] = true
		selectionByScope[sdkModuleScopeKey(selection.sdkName, selection.workspaceScope)] = selection
	}
	plan, err := planSDKModuleScopes(".", staged.Config, staged.ConfigDir, selectedProviders)
	if err != nil {
		return nil, err
	}

	ordered := make([]sdkModuleClientSelection, 0, len(selections))
	for _, scope := range plan.ordered {
		if selection, ok := selectionByScope[scope.key]; ok {
			ordered = append(ordered, selection)
		}
	}
	if len(ordered) != len(selections) {
		return nil, fmt.Errorf("generation plan contains %d of %d selected SDK client scopes", len(ordered), len(selections))
	}
	return ordered, nil
}

func selectSDKModuleClientsForModuleSources(
	staged *stagedWorkspaceConfig,
	moduleSources []string,
) ([]sdkModuleClientSelection, error) {
	sourceKeys := make(map[string]struct{}, len(moduleSources))
	for _, source := range moduleSources {
		if source != "" {
			sourceKeys[sdkModuleRefKey(source)] = struct{}{}
		}
	}
	if len(sourceKeys) == 0 {
		return nil, nil
	}

	return selectSDKModuleClientsWhere(staged, ".", sdkModuleClientUpdateArgs{All: true}, func(target string) (bool, error) {
		loadRef, err := resolveSDKManagedClientModule(staged.ConfigDir, target)
		if err != nil {
			return false, fmt.Errorf("client target %q: %w", target, err)
		}
		_, matches := sourceKeys[sdkModuleRefKey(loadRef)]
		return matches, nil
	})
}

func sdkModuleRefKey(ref string) string {
	if workspace.IsLocalRef(ref, "") {
		return filepath.ToSlash(filepath.Clean(ref))
	}
	return ref
}

type resolvedSDKModuleScope struct {
	sdk             selectedSDKModule
	scope           string
	configScopePath string
}

// resolveSDKModuleClientScope detects scopes against the original workspace
// before selecting the one scope that client add can change.
func (s *workspaceSchema) resolveSDKModuleClientScope(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	requestedSDK string,
) (resolvedSDKModuleScope, error) {
	if len(staged.Config.SDKs) == 0 {
		return resolvedSDKModuleScope{}, fmt.Errorf("no SDK modules are installed in this workspace")
	}
	names := []string{requestedSDK}
	if requestedSDK == "" {
		names = slices.Sorted(maps.Keys(staged.Config.SDKs))
	}
	var scopes []resolvedSDKModuleScope
	for _, name := range names {
		scope, err := s.resolveCurrentSDKModuleScope(ctx, parent, staged, name)
		if err != nil {
			return resolvedSDKModuleScope{}, fmt.Errorf("detect SDK %q client scope: %w", name, err)
		}
		if scope.scope != "" {
			scopes = append(scopes, scope)
		}
	}
	return selectDeepestSDKModuleScope(scopes)
}

// All scopes must be normalized ancestors of the same workspace cwd.
func selectDeepestSDKModuleScope(scopes []resolvedSDKModuleScope) (resolvedSDKModuleScope, error) {
	if len(scopes) == 0 {
		return resolvedSDKModuleScope{}, nil
	}
	sort.Slice(scopes, func(i, j int) bool {
		if scopes[i].scope != scopes[j].scope {
			return workspacePathContains(scopes[j].scope, scopes[i].scope)
		}
		return scopes[i].sdk.name < scopes[j].sdk.name
	})
	winner := scopes[0]
	var matches []string
	for _, scope := range scopes {
		if scope.scope != winner.scope {
			break
		}
		matches = append(matches, fmt.Sprintf("SDK %q at %q", scope.sdk.name, scope.scope))
	}
	if len(matches) > 1 {
		return resolvedSDKModuleScope{}, fmt.Errorf("multiple SDKs match the deepest client scope: %s; select one with --sdk", strings.Join(matches, ", "))
	}
	return winner, nil
}

// resolveCurrentSDKModuleScope compares the selected SDK's deepest recorded
// scope with its live detection result and returns the deeper path.
func (s *workspaceSchema) resolveCurrentSDKModuleScope(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	requestedSDK string,
) (resolvedSDKModuleScope, error) {
	selected, err := selectSDKModule(staged.Config, requestedSDK)
	if err != nil {
		return resolvedSDKModuleScope{}, err
	}
	recordedScope, err := deepestRecordedSDKModuleScope(selected.entry, staged.ConfigDir, parent.Self().Cwd)
	if err != nil {
		return resolvedSDKModuleScope{}, fmt.Errorf("SDK %q recorded scope: %w", selected.name, err)
	}
	settings, err := effectiveSDKModuleSettings(ctx, parent.Self(), staged.Config, selected.name, "")
	if err != nil {
		return resolvedSDKModuleScope{}, err
	}
	provider, err := s.loadWorkspaceSDKModule(ctx, parent.Self(), staged.ConfigDir, selected.ref, settings)
	if err != nil {
		return resolvedSDKModuleScope{}, err
	}
	rawScope, err := provider.FindClientRoot(ctx, parent)
	if err != nil {
		return resolvedSDKModuleScope{}, err
	}
	scope, err := validateDetectedSDKModuleScope(rawScope, parent.Self().Cwd)
	if err != nil {
		return resolvedSDKModuleScope{}, fmt.Errorf("SDK %q scope: %w", selected.name, err)
	}
	scope = deeperSDKModuleScope(recordedScope, scope)
	configScopePath := ""
	if scope != "" {
		configScopePath, _, err = workspace.SDKScopeKey(selected.entry, staged.ConfigDir, scope)
		if err != nil {
			return resolvedSDKModuleScope{}, err
		}
	}
	return resolvedSDKModuleScope{sdk: selected, scope: scope, configScopePath: configScopePath}, nil
}

func deepestRecordedSDKModuleScope(entry workspace.SDKEntry, configDir, cwd string) (string, error) {
	cwd = cleanWorkspaceRelPath(cwd)
	deepest := ""
	for configScope := range entry.Scopes {
		scope, err := workspace.ResolveSDKManagedPath(configDir, configScope)
		if err != nil {
			return "", err
		}
		scope = cleanWorkspaceRelPath(scope)
		if workspacePathContains(scope, cwd) {
			deepest = deeperSDKModuleScope(deepest, scope)
		}
	}
	return deepest, nil
}

func deeperSDKModuleScope(first, second string) string {
	if first == "" {
		return second
	}
	if second != "" && workspacePathContains(first, second) {
		return second
	}
	return first
}

func selectSDKModule(cfg *workspace.Config, requested string) (selectedSDKModule, error) {
	if cfg == nil || len(cfg.SDKs) == 0 {
		return selectedSDKModule{}, fmt.Errorf("no SDK modules are installed in this workspace")
	}
	if requested == "" {
		return selectedSDKModule{}, fmt.Errorf("SDK name is required")
	}
	name, provider, ref, err := installedSDKSource(cfg, requested)
	if err != nil {
		return selectedSDKModule{}, err
	}
	return selectedSDKModule{
		name:     name,
		entry:    cfg.SDKs[name],
		provider: provider,
		ref:      ref,
	}, nil
}

func moduleScopeOwner(cfg *workspace.Config, configDir, workspaceScope string) (string, bool, error) {
	for sdkName, entry := range cfg.SDKs {
		for configScope, scope := range entry.Scopes {
			if !scope.IsModule {
				continue
			}
			resolved, err := workspace.ResolveSDKManagedPath(configDir, configScope)
			if err != nil {
				return "", false, fmt.Errorf("SDK %q module scope: %w", sdkName, err)
			}
			if cleanWorkspaceRelPath(resolved) == cleanWorkspaceRelPath(workspaceScope) {
				return sdkName, true, nil
			}
		}
	}
	return "", false, nil
}

// sdkDefaultModulePath asks the selected provider where a new module belongs.
// It returns "" when the provider omits the optional function or declines to
// choose, which leaves the engine default in place.
//
// The provider runs with global settings plus the explicit setting overrides
// from this command. Scope settings do not exist yet.
func (s *workspaceSchema) sdkDefaultModulePath(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	selected selectedSDKModule,
	explicitSettings map[string]any,
	moduleName string,
) (string, error) {
	settings, err := effectiveSDKModuleSettings(ctx, parent.Self(), staged.Config, selected.name, "")
	if err != nil {
		return "", err
	}
	if len(explicitSettings) > 0 {
		merged := make(map[string]any, len(settings)+len(explicitSettings))
		maps.Copy(merged, settings)
		maps.Copy(merged, explicitSettings)
		settings = merged
	}
	provider, err := s.loadWorkspaceSDKModule(ctx, parent.Self(), staged.ConfigDir, selected.ref, settings)
	if err != nil {
		return "", err
	}
	if !provider.ImplementsDefaultModulePath() {
		return "", nil
	}
	rawPath, err := provider.DefaultModulePath(ctx, parent, moduleName)
	if err != nil {
		return "", err
	}
	modulePath, err := validateSDKModuleDestination(rawPath)
	if err != nil {
		return "", fmt.Errorf("SDK %q defaultModulePath: %w", selected.name, err)
	}
	return modulePath, nil
}

// validateSDKModuleDestination validates a provider-chosen module path. The
// path must be workspace-root-relative and must stay inside the workspace. It
// does not have to contain Workspace.cwd, unlike a detected scope.
func validateSDKModuleDestination(rawPath string) (string, error) {
	if rawPath == "" {
		return "", nil
	}
	rawPath = strings.ReplaceAll(rawPath, `\`, "/")
	if filepath.IsAbs(rawPath) {
		return "", fmt.Errorf("returned path %q must be workspace-root-relative", rawPath)
	}
	modulePath, err := resolveWorkspacePath(rawPath, ".")
	if err != nil {
		return "", err
	}
	return cleanWorkspaceRelPath(filepath.ToSlash(modulePath)), nil
}

func validateDetectedSDKModuleScope(rawScope, cwd string) (string, error) {
	if rawScope == "" {
		return "", nil
	}
	rawScope = strings.ReplaceAll(rawScope, `\`, "/")
	if filepath.IsAbs(rawScope) {
		return "", fmt.Errorf("returned path %q must be workspace-root-relative", rawScope)
	}
	scope, err := resolveWorkspacePath(rawScope, ".")
	if err != nil {
		return "", err
	}
	scope = filepath.ToSlash(scope)
	cwd = filepath.ToSlash(cleanWorkspaceRelPath(cwd))
	if !workspacePathContains(scope, cwd) {
		return "", fmt.Errorf("returned path %q is not a parent of Workspace.cwd %q", scope, cwd)
	}
	return scope, nil
}

func workspacePathContains(parent, child string) bool {
	parent = filepath.ToSlash(cleanWorkspaceRelPath(parent))
	child = filepath.ToSlash(cleanWorkspaceRelPath(child))
	return parent == "." || parent == child || strings.HasPrefix(child, parent+"/")
}

func decodeSDKModuleSettings(raw core.JSON) (map[string]any, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var settings map[string]any
	decoder := json.NewDecoder(bytes.NewReader(raw.Bytes()))
	decoder.UseNumber()
	if err := decoder.Decode(&settings); err != nil {
		return nil, fmt.Errorf("decode SDK-module settings: %w", err)
	}
	return settings, nil
}

func mergeSDKModuleSettings(scope *workspace.SDKScope, settings map[string]any) {
	if len(settings) == 0 {
		return
	}
	if scope.Settings == nil {
		scope.Settings = map[string]any{}
	}
	for key, value := range settings {
		scope.Settings[key] = value
	}
}

func (s *workspaceSchema) stageSDKModuleConfig(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	lock *workspaceOverlayLock,
) (dagql.ObjectResult[*core.Workspace], error) {
	updated, err := workspace.UpdateConfigBytesAt(ctx, staged.Data, staged.Config, staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("update workspace config: %w", err)
	}
	return s.stageWorkspaceConfigAndLock(ctx, parent, staged, updated, lock)
}

func (s *workspaceSchema) generateSDKModuleScope(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
	staged *stagedWorkspaceConfig,
	selected selectedSDKModule,
	configScopePath string,
	workspaceScope string,
	scope workspace.SDKScope,
) (dagql.ObjectResult[*core.Workspace], error) {
	name := scope.Name
	if scope.IsModule {
		var err error
		name, err = resolveSDKModuleName(ws.Self(), staged.Config, staged.ConfigDir, workspaceScope, name)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK %q scope %q: %w", selected.name, workspaceScope, err)
		}
	}
	effectiveSettings, err := effectiveSDKModuleSettings(ctx, ws.Self(), staged.Config, selected.name, configScopePath)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	provider, err := s.loadWorkspaceSDKModule(ctx, ws.Self(), staged.ConfigDir, selected.ref, effectiveSettings)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	scoped, err := workspaceAtSDKModuleScope(ctx, ws, workspaceScope)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	operationCtx, clients, err := s.resolveSDKModuleScopeClients(ctx, ws, staged, scope.Clients)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	generated, err := provider.GenerateScope(operationCtx, scoped, scope.IsModule, name, clients)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	generated, err = s.validateSDKModuleWorkspace(operationCtx, ws, scoped, generated, workspaceScope, staged.ConfigFile)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if scope.IsModule {
		if err := s.validateGeneratedModuleConfig(operationCtx, generated, workspaceScope); err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
	}
	return generated, nil
}

func workspaceAtSDKModuleScope(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
	scopePath string,
) (dagql.ObjectResult[*core.Workspace], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	var rooted dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, ws, &rooted, dagql.Selector{
		Field: "withWorkdir",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(".")}},
	}); err != nil {
		return rooted, fmt.Errorf("set SDK-module workspace root: %w", err)
	}

	// A new module scope can point to a directory that does not exist yet.
	// Give the provider an existing empty directory so that it can inspect the
	// scope before it writes its first file. Do not replace an existing scope.
	prepared := rooted
	if cleanWorkspaceRelPath(scopePath) != "." {
		var rootDir dagql.ObjectResult[*core.Directory]
		if err := dag.Select(ctx, rooted, &rootDir, dagql.Selector{
			Field: "directory",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(".")}},
		}); err != nil {
			return prepared, fmt.Errorf("inspect SDK-module workspace root: %w", err)
		}
		var exists dagql.Boolean
		if err := dag.Select(ctx, rootDir, &exists, dagql.Selector{
			Field: "exists",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(scopePath)}},
		}); err != nil {
			return prepared, fmt.Errorf("inspect SDK-module scope %q: %w", scopePath, err)
		}
		if !exists.Bool() {
			var empty dagql.ObjectResult[*core.Directory]
			if err := dag.Select(ctx, dag.Root(), &empty, dagql.Selector{Field: "directory"}); err != nil {
				return prepared, fmt.Errorf("create empty SDK-module scope %q: %w", scopePath, err)
			}
			emptyID, err := empty.ID()
			if err != nil {
				return prepared, fmt.Errorf("get empty SDK-module scope %q ID: %w", scopePath, err)
			}
			if err := dag.Select(ctx, rooted, &prepared, dagql.Selector{
				Field: "withNewDirectory",
				Args: []dagql.NamedInput{
					{Name: "path", Value: dagql.String(scopePath)},
					{Name: "source", Value: dagql.NewID[*core.Directory](emptyID)},
				},
			}); err != nil {
				return prepared, fmt.Errorf("prepare SDK-module scope %q: %w", scopePath, err)
			}
		}
	}
	var scoped dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, prepared, &scoped, dagql.Selector{
		Field: "withWorkdir",
		Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String(scopePath)}},
	}); err != nil {
		return scoped, fmt.Errorf("set SDK-module scope %q: %w", scopePath, err)
	}
	return scoped, nil
}

func (s *workspaceSchema) validateSDKModuleWorkspace(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	base dagql.ObjectResult[*core.Workspace],
	generated dagql.ObjectResult[*core.Workspace],
	scopePath string,
	configFile string,
) (dagql.ObjectResult[*core.Workspace], error) {
	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	generatedID, err := generated.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK-module result ID: %w", err)
	}
	attached, err := dagql.NewID[*core.Workspace](generatedID).Load(ctx, dag)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("attach SDK-module result: %w", err)
	}
	if cleanWorkspaceRelPath(attached.Self().Cwd) != cleanWorkspaceRelPath(scopePath) {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf(
			"SDK module changed Workspace.cwd from %q to %q",
			cleanWorkspaceRelPath(scopePath),
			cleanWorkspaceRelPath(attached.Self().Cwd),
		)
	}

	changes, err := s.workspaceChangesBetween(ctx, base, attached)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("inspect SDK-module result: %w", err)
	}
	isEmpty, err := changes.Self().IsEmpty(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("inspect SDK-module result: %w", err)
	}
	if isEmpty {
		return parent, nil
	}
	touched, err := changesetTouchedPaths(ctx, changes.Self())
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("inspect SDK-module paths: %w", err)
	}
	configFile = cleanWorkspaceRelPath(configFile)
	for _, touchedPath := range touched {
		touchedPath = cleanWorkspaceRelPath(touchedPath)
		if touchedPath == configFile {
			return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK module must not modify engine-owned %s", filepath.ToSlash(configFile))
		}
	}
	changesID, err := changes.ID()
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK-module changes ID: %w", err)
	}
	var adopted dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, parent, &adopted, dagql.Selector{
		Field: "withChanges",
		Args:  []dagql.NamedInput{{Name: "changes", Value: dagql.NewID[*core.Changeset](changesID)}},
	}); err != nil {
		return adopted, fmt.Errorf("adopt SDK-module changes: %w", err)
	}
	return adopted, nil
}

func (s *workspaceSchema) validateGeneratedModuleConfig(
	ctx context.Context,
	generated dagql.ObjectResult[*core.Workspace],
	scopePath string,
) error {
	root, err := s.workspaceOverlayRootfs(ctx, generated.Self())
	if err != nil {
		return err
	}
	_, found, err := moduleConfigInDir(ctx, &core.DirectoryStatFS{Dir: root}, scopePath)
	if err != nil {
		return fmt.Errorf("validate generated module config at %q: %w", scopePath, err)
	}
	if !found {
		return fmt.Errorf("SDK module generateScope did not create a valid dagger-module.toml at %q", scopePath)
	}
	return nil
}
