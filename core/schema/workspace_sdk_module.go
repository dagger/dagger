package schema

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/url"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type sdkModuleInitArgs struct {
	SDK      string
	Name     string    `default:""`
	Path     string    `default:""`
	Settings core.JSON `default:""`
}

type sdkModuleDetectScopeArgs struct {
	SDK string
}

type sdkModuleClientAddArgs struct {
	Module   string
	SDK      string
	Settings core.JSON `default:""`
}

type sdkModuleClientRemoveArgs struct {
	Module string
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

	configScopePath, err := workspace.SDKManagedPathFor(staged.ConfigDir, scopePath)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if err := planSDKModuleInitInstall(staged.Config, moduleName, configScopePath, explicitPath); err != nil {
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
	scope.Name = moduleName
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

func planSDKModuleInitInstall(cfg *workspace.Config, moduleName, sourcePath string, explicitPath bool) error {
	if explicitPath {
		return nil
	}
	_, err := planWorkspaceInstallConfig(cfg, workspaceInstallArgs{}, moduleName, sourcePath)
	return err
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

	moduleName := nameArg
	if moduleName == "" && pathArg != "" {
		moduleName = workspaceDirectoryName(explicitPath)
	}
	if moduleName == "" && ws.ConfigFile != "" {
		moduleName = moduleDevName(workspaceDirectoryName(filepath.Dir(cleanWorkspaceRelPath(ws.ConfigFile))))
	}
	if moduleName == "" {
		moduleName = moduleDevName(workspaceRootDirectoryName(ws))
	}
	if strings.TrimSpace(moduleName) == "" {
		return "", "", false, fmt.Errorf("cannot infer module name from the module path, active config file, or workspace root; pass --name")
	}

	scopePath := explicitPath
	if pathArg == "" {
		configDir := "."
		if ws.ConfigFile != "" {
			configDir = filepath.Dir(cleanWorkspaceRelPath(ws.ConfigFile))
		}
		scopePath = filepath.Join(configDir, ".dagger", "modules", moduleName)
	}
	scopePath = cleanWorkspaceRelPath(scopePath)
	return scopePath, moduleName, pathArg != "", nil
}

func moduleDevName(directoryName string) string {
	if directoryName == "" {
		return ""
	}
	return directoryName + "-dev"
}

func workspaceDirectoryName(workspacePath string) string {
	cleaned := path.Clean(strings.ReplaceAll(filepath.ToSlash(workspacePath), `\`, "/"))
	if cleaned == "." || cleaned == "/" || cleaned == "" {
		return ""
	}
	return path.Base(cleaned)
}

func workspaceRootDirectoryName(ws *core.Workspace) string {
	if hostPath := strings.TrimSpace(ws.HostPath()); hostPath != "" {
		return workspaceDirectoryName(hostPath)
	}

	address := strings.TrimSpace(ws.Address)
	if address == "" {
		return ""
	}
	if strings.Contains(address, "://") {
		parsed, err := url.Parse(address)
		if err != nil {
			return ""
		}
		switch parsed.Scheme {
		case "file":
			address = parsed.Path
		case "http", "https", "ssh":
			address = parsed.Host + parsed.Path
		default:
			return ""
		}
	}

	address = filepath.ToSlash(address)
	cwd := cleanWorkspaceRelPath(ws.Cwd)
	if cwd != "." {
		suffix := "/" + filepath.ToSlash(cwd)
		if versioned := strings.LastIndex(address, suffix+"@"); versioned >= 0 {
			address = address[:versioned]
		} else if strings.HasSuffix(address, suffix) {
			address = strings.TrimSuffix(address, suffix)
		} else {
			return ""
		}
	} else if version := strings.LastIndex(address, "@"); version > strings.Index(address, "/") {
		address = address[:version]
	}
	return workspaceDirectoryName(address)
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
	ws := parent.Self()
	staged, err := s.loadWorkspaceConfigForOverlay(ctx, ws, workspaceConfigMustExist, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}

	currentScope, err := s.resolveCurrentSDKModuleScope(ctx, parent, staged, args.SDK)
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
	selectedWorkspace, overlayLock, err := s.prepareWorkspaceOverlayLock(operationCtx, ws, staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	operationCtx = withWorkspaceLookupLockOverride(operationCtx, overlayLock.Lock)
	moduleLoadRef, configTarget, err := resolveWorkspaceClientModuleInput(
		selectedWorkspace,
		staged.Config,
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

	configScopePath, err := workspace.SDKManagedPathFor(staged.ConfigDir, currentScope.scope)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	explicitSettings, err := decodeSDKModuleSettings(args.Settings)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	entry := staged.Config.SDKs[currentScope.sdk.name]
	if entry.Scopes == nil {
		entry.Scopes = map[string]workspace.SDKScope{}
	}
	scope := entry.Scopes[configScopePath]
	if slices.Contains(scope.Clients, configTarget) {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client target %q already exists in scope %q", configTarget, currentScope.scope)
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

	type match struct {
		sdkName         string
		configScopePath string
		workspaceScope  string
	}
	var matches []match
	cwd := cleanWorkspaceRelPath(parent.Self().Cwd)
	for sdkName, entry := range staged.Config.SDKs {
		for configScopePath, scope := range entry.Scopes {
			if !slices.Contains(scope.Clients, args.Module) {
				continue
			}
			workspaceScope, err := workspace.ResolveSDKManagedPath(staged.ConfigDir, configScopePath)
			if err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("SDK %q scope: %w", sdkName, err)
			}
			if workspacePathContains(workspaceScope, cwd) {
				matches = append(matches, match{sdkName, configScopePath, workspaceScope})
			}
		}
	}
	if len(matches) == 0 {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client target %q is not recorded for the current scope", args.Module)
	}
	sort.Slice(matches, func(i, j int) bool { return len(matches[i].workspaceScope) > len(matches[j].workspaceScope) })
	if len(matches) > 1 && len(matches[0].workspaceScope) == len(matches[1].workspaceScope) {
		return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("client target %q matches multiple SDK scopes at %q", args.Module, matches[0].workspaceScope)
	}
	winner := matches[0]
	entry := staged.Config.SDKs[winner.sdkName]
	scope := entry.Scopes[winner.configScopePath]
	scope.Clients = slices.DeleteFunc(scope.Clients, func(target string) bool { return target == args.Module })
	if !scope.IsModule && len(scope.Clients) == 0 && len(scope.Settings) == 0 {
		delete(entry.Scopes, winner.configScopePath)
	} else {
		entry.Scopes[winner.configScopePath] = scope
	}
	if len(entry.Scopes) == 0 {
		entry.Scopes = nil
	}
	staged.Config.SDKs[winner.sdkName] = entry
	updated, err := s.stageSDKModuleConfig(ctx, parent, staged, nil)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	selected, err := selectSDKModule(staged.Config, winner.sdkName)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	return s.generateSDKModuleScope(
		ctx,
		updated,
		staged,
		selected,
		winner.configScopePath,
		winner.workspaceScope,
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
	selectedWorkspace, overlayLock, err := s.prepareWorkspaceOverlayLock(operationCtx, ws, staged.ConfigDir)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	refreshed := workspace.NewLock()
	refreshCtx := withWorkspaceLookupLockOverride(operationCtx, refreshed)
	for _, selection := range selections {
		for _, target := range selection.targets {
			loadRef, err := resolveSDKManagedClientModule(selectedWorkspace, staged.Config, staged.ConfigDir, target)
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
		loadRef, err := resolveSDKManagedClientModule(nil, staged.Config, staged.ConfigDir, target)
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
	sdk   selectedSDKModule
	scope string
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
	rawScope, err := provider.DetectScope(ctx, parent)
	if err != nil {
		return resolvedSDKModuleScope{}, err
	}
	scope, err := validateDetectedSDKModuleScope(rawScope, parent.Self().Cwd)
	if err != nil {
		return resolvedSDKModuleScope{}, fmt.Errorf("SDK %q scope: %w", selected.name, err)
	}
	return resolvedSDKModuleScope{sdk: selected, scope: deeperSDKModuleScope(recordedScope, scope)}, nil
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
	updated, err := workspace.UpdateConfigBytes(staged.Data, staged.Config)
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
	generated, err := provider.GenerateScope(operationCtx, scoped, scope.IsModule, scope.Name, clients)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	generated, err = s.validateSDKModuleWorkspace(operationCtx, scoped, generated, workspaceScope, staged.ConfigFile)
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
	if err := dag.Select(ctx, base, &adopted, dagql.Selector{
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
