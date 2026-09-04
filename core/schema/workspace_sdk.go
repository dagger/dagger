package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"sort"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) sdks(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResultArray[*core.WorkspaceSDK], error) {
	ws := parent.Self()
	if ws.ConfigFile == "" {
		return dagql.ObjectResultArray[*core.WorkspaceSDK]{}, nil
	}

	cfg, err := readWorkspaceConfig(ctx, ws)
	if err != nil {
		return nil, err
	}
	configDir, err := workspaceConfigDirectory(ws)
	if err != nil {
		return nil, err
	}

	sdks := make(core.WorkspaceSDKs, 0, len(cfg.SDKs))
	for name, entry := range cfg.SDKs {
		sdk, err := workspaceSDKFromEntry(cfg, configDir, name, entry, cfg.Modules[entry.Module])
		if err != nil {
			return nil, err
		}
		sdks = append(sdks, sdk)
	}
	sdks.Sort()

	return workspaceSDKResults(ctx, parent, sdks)
}

func (s *workspaceSchema) sdk(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	args struct {
		Name string
	},
) (dagql.ObjectResult[*core.WorkspaceSDK], error) {
	if args.Name == "" {
		return dagql.ObjectResult[*core.WorkspaceSDK]{}, fmt.Errorf("SDK name is required")
	}

	ws := parent.Self()
	cfg := &workspace.Config{}
	configDir := "."
	if ws.ConfigFile != "" {
		var err error
		cfg, err = readWorkspaceConfig(ctx, ws)
		if err != nil {
			return dagql.ObjectResult[*core.WorkspaceSDK]{}, err
		}
		configDir, err = workspaceConfigDirectory(ws)
		if err != nil {
			return dagql.ObjectResult[*core.WorkspaceSDK]{}, err
		}
	}

	sdkName, entry, _, err := installedSDKSource(cfg, args.Name)
	if err != nil {
		return dagql.ObjectResult[*core.WorkspaceSDK]{}, err
	}
	sdk, err := workspaceSDKFromEntry(cfg, configDir, sdkName, cfg.SDKs[sdkName], entry)
	if err != nil {
		return dagql.ObjectResult[*core.WorkspaceSDK]{}, err
	}
	result, err := workspaceSDKResults(ctx, parent, core.WorkspaceSDKs{sdk})
	if err != nil {
		return dagql.ObjectResult[*core.WorkspaceSDK]{}, err
	}
	return result[0], nil
}

func workspaceSDKResults(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	sdks core.WorkspaceSDKs,
) (dagql.ObjectResultArray[*core.WorkspaceSDK], error) {
	results := make(dagql.ObjectResultArray[*core.WorkspaceSDK], 0, len(sdks))
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	for _, sdk := range sdks {
		moduleNames := make(dagql.ArrayInput[dagql.String], len(sdk.Modules))
		moduleSources := make(dagql.ArrayInput[dagql.String], len(sdk.Modules))
		for i, module := range sdk.Modules {
			moduleNames[i] = dagql.String(module.Name)
			moduleSources[i] = dagql.String(module.Source)
		}
		clientNames := make(dagql.ArrayInput[dagql.String], len(sdk.Clients))
		clientSources := make(dagql.ArrayInput[dagql.String], len(sdk.Clients))
		for i, client := range sdk.Clients {
			clientNames[i] = dagql.String(client.Name)
			clientSources[i] = dagql.String(client.Source)
		}

		var result dagql.ObjectResult[*core.WorkspaceSDK]
		if err := srv.Select(ctx, parent, &result, dagql.Selector{
			Field: "__workspaceSDK",
			Args: []dagql.NamedInput{
				{Name: "name", Value: dagql.String(sdk.Name)},
				{Name: "ref", Value: dagql.String(sdk.Ref)},
				{Name: "moduleNames", Value: moduleNames},
				{Name: "moduleSources", Value: moduleSources},
				{Name: "clientNames", Value: clientNames},
				{Name: "clientSources", Value: clientSources},
			},
		}); err != nil {
			return nil, fmt.Errorf("workspace SDK list: create SDK %q: %w", sdk.Name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func (s *workspaceSchema) workspaceSDK(
	_ context.Context,
	_ *core.Workspace,
	args struct {
		Name          string
		Ref           string
		ModuleNames   []string `default:"[]"`
		ModuleSources []string `default:"[]"`
		ClientNames   []string `default:"[]"`
		ClientSources []string `default:"[]"`
	},
) (*core.WorkspaceSDK, error) {
	if len(args.ModuleNames) != len(args.ModuleSources) {
		return nil, fmt.Errorf("workspace SDK %q: module names and sources have different lengths", args.Name)
	}
	if len(args.ClientNames) != len(args.ClientSources) {
		return nil, fmt.Errorf("workspace SDK %q: client names and sources have different lengths", args.Name)
	}

	sdk := &core.WorkspaceSDK{
		Name: args.Name,
		Ref:  args.Ref,
	}
	for i, name := range args.ModuleNames {
		sdk.Modules = append(sdk.Modules, &core.WorkspaceModule{
			Name:   name,
			Source: args.ModuleSources[i],
		})
	}
	for i, name := range args.ClientNames {
		sdk.Clients = append(sdk.Clients, &core.WorkspaceModule{
			Name:   name,
			Source: args.ClientSources[i],
		})
	}
	return sdk, nil
}

func workspaceSDKFromEntry(cfg *workspace.Config, configDir, sdkName string, sdkEntry workspace.SDKEntry, moduleEntry workspace.ModuleEntry) (*core.WorkspaceSDK, error) {
	sdk := &core.WorkspaceSDK{
		Name: sdkName,
		Ref:  resolvedModuleEntrySourceWithPin(configDir, moduleEntry),
	}
	scopePaths := make([]string, 0, len(sdkEntry.Scopes))
	for scopePath := range sdkEntry.Scopes {
		scopePaths = append(scopePaths, scopePath)
	}
	sort.Strings(scopePaths)
	for _, scopePath := range scopePaths {
		scope := sdkEntry.Scopes[scopePath]
		scopeSource, err := workspace.ResolveSDKManagedPath(configDir, scopePath)
		if err != nil {
			return nil, fmt.Errorf("module managed by %q: %w", sdkName, err)
		}
		if scope.IsModule {
			sdk.Modules = append(sdk.Modules, &core.WorkspaceModule{
				Name:   scope.Name,
				Source: scopeSource,
			})
		}
		for _, target := range scope.Clients {
			ref, err := resolveSDKManagedClientModule(nil, cfg, configDir, target)
			if err != nil {
				return nil, fmt.Errorf("client managed by %q: %w", sdkName, err)
			}
			sdk.Clients = append(sdk.Clients, &core.WorkspaceModule{
				Name:   scopeSource,
				Source: ref,
			})
		}
	}
	core.WorkspaceModules(sdk.Modules).Sort()
	core.WorkspaceModules(sdk.Clients).Sort()
	return sdk, nil
}

func installedSDKSource(cfg *workspace.Config, name string) (string, workspace.ModuleEntry, string, error) {
	if cfg == nil || cfg.Modules == nil || cfg.SDKs == nil {
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("%q is not installed as an SDK in this workspace; install its module with `dagger module install <module-ref>`", name)
	}
	if err := workspace.ValidateSDKs(cfg); err != nil {
		return "", workspace.ModuleEntry{}, "", err
	}
	sdk, ok := cfg.SDKs[name]
	if !ok {
		// SDK implementations historically addressed Workspace.sdk by their own
		// installed module name. Keep that engine-level lookup working while the
		// CLI exposes only the explicit [sdks.<name>] command names.
		if sdkName, found := workspace.SDKNameForModule(cfg, name); found {
			sdk = cfg.SDKs[sdkName]
			name = sdkName
		} else {
			return "", workspace.ModuleEntry{}, "", fmt.Errorf("%q is not installed as an SDK in this workspace; install its module with `dagger module install <module-ref>`", name)
		}
	}
	return installedSDKSourceForModule(name, cfg.Modules[sdk.Module])
}

func installedSDKSourceForModule(sdkName string, entry workspace.ModuleEntry) (string, workspace.ModuleEntry, string, error) {
	source := moduleEntrySourceWithPin(entry)
	if source == "" {
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("SDK %q has no module source", sdkName)
	}
	return sdkName, entry, source, nil
}

func moduleEntrySourceWithPin(entry workspace.ModuleEntry) string {
	return sourceWithPin(entry.Source, entry.Pin)
}

func resolvedModuleEntrySourceWithPin(configDir string, entry workspace.ModuleEntry) string {
	return sourceWithPin(workspace.ResolveModuleEntrySource(configDir, entry.Source), entry.Pin)
}

func moduleEntrySourceWithPinRelativeTo(configDir, targetDir string, entry workspace.ModuleEntry) (string, error) {
	if !workspace.IsLocalRef(entry.Source, "") {
		return moduleEntrySourceWithPin(entry), nil
	}
	source := workspace.ResolveModuleEntrySource(configDir, entry.Source)
	rel, err := filepath.Rel(targetDir, source)
	if err != nil {
		return "", fmt.Errorf("resolve module source %q from %q: %w", source, targetDir, err)
	}
	return sourceWithPin(filepath.ToSlash(rel), entry.Pin), nil
}

func sourceWithPin(source, pin string) string {
	if pin == "" || strings.Contains(source, "@") {
		return source
	}
	return source + "@" + pin
}
