package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/modules"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type SdkOption struct {
	Key   string `field:"true" doc:"Option key."`
	Value string `field:"true" doc:"Option value."`
}

func (SdkOption) TypeName() string {
	return "SdkOption"
}

func (SdkOption) TypeDescription() string {
	return "SDK-specific option to persist for a generated client."
}

type workspaceClientInitArgs struct {
	Path    string
	SDK     string
	Module  string
	Options []dagql.InputObject[SdkOption] `default:"[]"`
	Here    bool                           `default:"false"`
}

func (s *workspaceSchema) clientInit(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceClientInitArgs,
) (*core.Changeset, error) {
	if args.Path == "" {
		return nil, fmt.Errorf("client path is required")
	}
	if args.SDK == "" {
		return nil, fmt.Errorf("--sdk is required")
	}
	if args.Module == "" {
		return nil, fmt.Errorf("--module is required")
	}

	clientPath, err := cleanWorkspaceClientPath(args.Path)
	if err != nil {
		return nil, err
	}
	moduleRef, moduleLoadRef, err := resolveWorkspaceClientModuleRef(parent, args.Module)
	if err != nil {
		return nil, err
	}

	cfg, _, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigInitIfMissing, args.Here)
	if err != nil {
		return nil, err
	}
	if cfg.Modules == nil {
		cfg.Modules = map[string]workspace.ModuleEntry{}
	}

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("workspace client context: %w", err)
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)

	targetModule, err := s.resolveClientTargetModule(workspaceCtx, moduleLoadRef, "")
	if err != nil {
		return nil, err
	}
	modulePin := targetModule.Self().Pin()

	sdkName := workspace.ConventionalSDKShortName(args.SDK)
	options, err := sdkOptionsMap(args.Options)
	if err != nil {
		return nil, err
	}
	removeClientEntryAtPath(cfg, clientPath)
	sdkEntry := cfg.Modules[sdkName]
	if sdkEntry.Source == "" {
		sdkEntry.Source = args.SDK
	}
	if sdkEntry.AsSDK == nil {
		sdkEntry.AsSDK = &workspace.ModuleAsSDK{}
	}
	sdkEntry.AsSDK.Clients = append(sdkEntry.AsSDK.Clients, workspace.SDKManagedClient{
		Path:    clientPath,
		Module:  moduleRef,
		Pin:     modulePin,
		Options: options,
	})
	cfg.Modules[sdkName] = sdkEntry

	existingConfigBytes, err := readConfigBytes(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("read workspace config: %w", err)
	}
	newConfigBytes, err := workspace.UpdateConfigBytes(existingConfigBytes, cfg)
	if err != nil {
		return nil, fmt.Errorf("update workspace config: %w", err)
	}

	generatedClients, err := s.workspaceClientInitGeneratedDiff(workspaceCtx, targetModule, args.SDK, clientPath)
	if err != nil {
		return nil, err
	}

	configRelPath, err := workspaceConfigFile(parent)
	if err != nil {
		return nil, err
	}

	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace rootfs: %w", err)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dagql server: %w", err)
	}

	updatedDir := baseDir
	updatedDir, err = workspaceWithFile(ctx, dag, updatedDir, configRelPath, newConfigBytes, 0o644)
	if err != nil {
		return nil, fmt.Errorf("stage workspace config update: %w", err)
	}
	updatedDir, err = workspaceWithDirectoryOverlay(ctx, dag, updatedDir, generatedClients)
	if err != nil {
		return nil, fmt.Errorf("stage generated client: %w", err)
	}

	return workspaceMigrationChanges(ctx, updatedDir, baseDir)
}

func (s *workspaceSchema) clientGenerate(
	ctx context.Context,
	parent *core.Workspace,
	args struct{},
) (*core.Changeset, error) {
	if isSyntheticWorkspace(parent) {
		return core.NewEmptyChangeset(ctx)
	}

	cfg, err := workspaceConfigWithCompatFallback(ctx, parent)
	if err != nil {
		return nil, err
	}

	baseDir, err := s.resolveRootfs(ctx, parent, ".", core.CopyFilter{}, false)
	if err != nil {
		return nil, fmt.Errorf("resolve workspace rootfs: %w", err)
	}
	updatedDir := baseDir

	workspaceCtx, err := s.withWorkspaceClientContext(ctx, parent)
	if err != nil {
		return nil, fmt.Errorf("workspace client context: %w", err)
	}
	workspaceCtx = workspaceInstallLookupContext(workspaceCtx)

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, fmt.Errorf("dagql server: %w", err)
	}

	for sdkName, entry := range cfg.Modules {
		if entry.AsSDK == nil || len(entry.AsSDK.Clients) == 0 {
			continue
		}
		sdkRef := clientGeneratorSource(entry)
		if sdkRef == "" {
			return nil, fmt.Errorf("SDK module %q has no source", sdkName)
		}
		for _, client := range entry.AsSDK.Clients {
			moduleRef, moduleLoadRef, err := resolveWorkspaceClientModuleRef(parent, client.Module)
			if err != nil {
				return nil, err
			}
			targetModule, err := s.resolveClientTargetModule(workspaceCtx, moduleLoadRef, client.Pin)
			if err != nil {
				return nil, fmt.Errorf("generate client %q for module %q: %w", client.Path, moduleRef, err)
			}
			generatedClient, err := s.workspaceClientInitGeneratedDiff(workspaceCtx, targetModule, sdkRef, client.Path)
			if err != nil {
				return nil, fmt.Errorf("generate client %q: %w", client.Path, err)
			}
			updatedDir, err = workspaceWithDirectoryOverlay(ctx, dag, updatedDir, generatedClient)
			if err != nil {
				return nil, fmt.Errorf("stage generated client %q: %w", client.Path, err)
			}
		}
	}

	return workspaceMigrationChanges(ctx, updatedDir, baseDir)
}

func (s *workspaceSchema) resolveClientTargetModule(
	ctx context.Context,
	ref string,
	pin string,
) (dagql.ObjectResult[*core.ModuleSource], error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return src, fmt.Errorf("dagql server: %w", err)
	}
	if err := srv.Select(ctx, srv.Root(), &src, workspaceClientModuleSourceSelector(ref, pin)); err != nil {
		return src, fmt.Errorf("load module source: %w", err)
	}
	if src.Self() == nil {
		return src, fmt.Errorf("load module source: empty result")
	}
	if !src.Self().ConfigExists {
		return src, fmt.Errorf("ref %q does not point to an initialized module", ref)
	}
	return src, nil
}

func (s *workspaceSchema) workspaceClientInitGeneratedDiff(
	ctx context.Context,
	targetModule dagql.ObjectResult[*core.ModuleSource],
	sdkRef string,
	clientPath string,
) (dagql.ObjectResult[*core.Directory], error) {
	var out dagql.ObjectResult[*core.Directory]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return out, fmt.Errorf("dagql server: %w", err)
	}
	if err := srv.Select(ctx, srv.Root(), &out, dagql.Selector{Field: "directory"}); err != nil {
		return out, fmt.Errorf("create empty generated-client directory: %w", err)
	}

	return (&moduleSourceSchema{}).runClientGenerator(ctx, targetModule, out, &modules.ModuleConfigClient{
		Generator: sdkRef,
		Directory: clientPath,
	})
}

func cleanWorkspaceClientPath(path string) (string, error) {
	cleaned := filepath.Clean(path)
	if cleaned == "." || cleaned == string(filepath.Separator) {
		return "", fmt.Errorf("client path must point to a directory below the workspace root")
	}
	if filepath.IsAbs(cleaned) {
		return "", fmt.Errorf("client path %q must be workspace-relative, not absolute", path)
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("client path %q must not escape the workspace root", path)
	}
	return cleaned, nil
}

func resolveWorkspaceClientModuleRef(ws *core.Workspace, ref string) (configRef string, loadRef string, _ error) {
	if !workspace.IsLocalRef(ref, "") {
		return ref, ref, nil
	}
	cleaned := filepath.Clean(ref)
	if filepath.IsAbs(cleaned) {
		rel, err := filepath.Rel(ws.HostPath(), cleaned)
		if err != nil {
			return "", "", fmt.Errorf("compute workspace-relative module path: %w", err)
		}
		cleaned = rel
	}
	if cleaned == "." || cleaned == "" {
		cleaned = "."
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("--module %q must not escape the workspace root", ref)
	}
	loadPath, err := workspaceHostPath(ws, cleaned)
	if err != nil {
		return "", "", err
	}
	return cleaned, loadPath, nil
}

func workspaceClientModuleSourceSelector(ref string, pin string) dagql.Selector {
	args := []dagql.NamedInput{
		{Name: "refString", Value: dagql.String(ref)},
		{Name: "disableFindUp", Value: dagql.Boolean(true)},
	}
	if pin != "" {
		args = append(args, dagql.NamedInput{Name: "refPin", Value: dagql.String(pin)})
	}
	return dagql.Selector{
		Field: "moduleSource",
		Args:  args,
	}
}

func clientGeneratorSource(entry workspace.ModuleEntry) string {
	if entry.Pin == "" || strings.Contains(entry.Source, "@") {
		return entry.Source
	}
	return entry.Source + "@" + entry.Pin
}

func sdkOptionsMap(inputs []dagql.InputObject[SdkOption]) (map[string]string, error) {
	if len(inputs) == 0 {
		return nil, nil
	}
	options := map[string]string{}
	for _, input := range inputs {
		option := input.Value
		if option.Key == "" {
			return nil, fmt.Errorf("--option key must not be empty")
		}
		switch option.Key {
		case "path", "module", "pin":
			return nil, fmt.Errorf("--option %q is reserved", option.Key)
		}
		options[option.Key] = option.Value
	}
	return options, nil
}

func removeClientEntryAtPath(cfg *workspace.Config, clientPath string) {
	if cfg == nil {
		return
	}
	cleanPath := filepath.Clean(clientPath)
	for moduleName, entry := range cfg.Modules {
		if entry.AsSDK == nil || len(entry.AsSDK.Clients) == 0 {
			continue
		}
		entry.AsSDK.Clients = slices.DeleteFunc(entry.AsSDK.Clients, func(client workspace.SDKManagedClient) bool {
			return filepath.Clean(client.Path) == cleanPath
		})
		if len(entry.AsSDK.Modules) == 0 && len(entry.AsSDK.Clients) == 0 {
			entry.AsSDK = nil
		}
		cfg.Modules[moduleName] = entry
	}
}
