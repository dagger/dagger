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

	sdks := make(core.WorkspaceSDKs, 0, len(cfg.Modules))
	for name, entry := range cfg.Modules {
		if entry.AsSDK == nil {
			continue
		}
		sdks = append(sdks, workspaceSDKFromEntry(configDir, name, entry))
	}
	sdks.Sort()

	return workspaceSDKResults(ctx, sdks)
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

	moduleName, entry, _, err := installedSDKSource(cfg, args.Name)
	if err != nil {
		return dagql.ObjectResult[*core.WorkspaceSDK]{}, err
	}
	result, err := workspaceSDKResults(ctx, core.WorkspaceSDKs{
		workspaceSDKFromEntry(configDir, moduleName, entry),
	})
	if err != nil {
		return dagql.ObjectResult[*core.WorkspaceSDK]{}, err
	}
	return result[0], nil
}

func workspaceSDKResults(ctx context.Context, sdks core.WorkspaceSDKs) (dagql.ObjectResultArray[*core.WorkspaceSDK], error) {
	results := make(dagql.ObjectResultArray[*core.WorkspaceSDK], 0, len(sdks))
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}
	for _, sdk := range sdks {
		result, err := dagql.NewObjectResultForCurrentCall(ctx, srv, sdk)
		if err != nil {
			return nil, fmt.Errorf("workspace SDK %q: %w", sdk.Name, err)
		}
		results = append(results, result)
	}
	return results, nil
}

func workspaceSDKFromEntry(configDir, moduleName string, entry workspace.ModuleEntry) *core.WorkspaceSDK {
	name := entry.AsSDK.Name
	if name == "" {
		name = moduleName
	}
	sdk := &core.WorkspaceSDK{
		Name: name,
		Ref:  resolvedModuleEntrySourceWithPin(configDir, entry),
	}
	for _, mod := range entry.AsSDK.Modules {
		source := filepath.ToSlash(cleanWorkspaceRelPath(mod.Path))
		sdk.Modules = append(sdk.Modules, &core.WorkspaceModule{
			Name:   filepath.ToSlash(filepath.Base(source)),
			Source: source,
		})
	}
	for _, client := range entry.AsSDK.Clients {
		ref := client.Module
		if client.Pin != "" && !strings.Contains(ref, "@") {
			ref += "@" + client.Pin
		}
		sdk.Clients = append(sdk.Clients, &core.WorkspaceModule{
			Name:   filepath.ToSlash(cleanWorkspaceRelPath(client.Path)),
			Source: ref,
		})
	}
	core.WorkspaceModules(sdk.Modules).Sort()
	core.WorkspaceModules(sdk.Clients).Sort()
	return sdk
}

func installedSDKSource(cfg *workspace.Config, name string) (string, workspace.ModuleEntry, string, error) {
	if cfg == nil || cfg.Modules == nil {
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("%q is not installed as an SDK in this workspace; run `dagger sdk install %s` first", name, name)
	}
	if entry, ok := cfg.Modules[name]; ok && entry.AsSDK != nil {
		return installedSDKSourceForModule(name, entry)
	}

	var matches []string
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil || entry.AsSDK.Name != name {
			continue
		}
		matches = append(matches, moduleName)
	}
	sort.Strings(matches)
	switch len(matches) {
	case 0:
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("%q is not installed as an SDK in this workspace; run `dagger sdk install %s` first", name, name)
	case 1:
		entry := cfg.Modules[matches[0]]
		return installedSDKSourceForModule(matches[0], entry)
	default:
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("SDK name %q is ambiguous: matches modules.%s.as-sdk; choose a unique as-sdk.name", name, strings.Join(matches, ".as-sdk, modules."))
	}
}

func installedSDKSourceForModule(moduleName string, entry workspace.ModuleEntry) (string, workspace.ModuleEntry, string, error) {
	source := moduleEntrySourceWithPin(entry)
	if source == "" {
		return "", workspace.ModuleEntry{}, "", fmt.Errorf("SDK module %q has no source", moduleName)
	}
	return moduleName, entry, source, nil
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
