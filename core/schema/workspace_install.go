package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
)

type workspaceInstallArgs struct {
	Ref   string
	Name  string `default:""`
	Here  bool   `default:"false"`
	AsSdk bool   `default:"false"`
}

func (s *workspaceSchema) install(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceInstallArgs,
) (dagql.String, error) {
	if err := unsupportedSyntheticWorkspaceFeature(parent, "module installation"); err != nil {
		return "", err
	}
	if parent.CompatWorkspace() != nil {
		return "", fmt.Errorf("workspace is using legacy dagger.json config; run dagger setup first")
	}

	name, sourcePath, err := s.resolveWorkspaceInstall(ctx, parent, args.Ref, args.Name, args.Here)
	if err != nil {
		return "", err
	}

	cfg, initialized, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigInitIfMissing, args.Here)
	if err != nil {
		return "", err
	}

	if existing, ok := cfg.Modules[name]; ok {
		if existing.Source != sourcePath {
			return "", fmt.Errorf(
				"module %q already exists in workspace config with source %q (new source %q)",
				name,
				existing.Source,
				sourcePath,
			)
		}
		// Idempotent re-install: same source already there. If --as-sdk was
		// passed and the install isn't already marked, stamp the marker so a
		// plain `install` followed by `sdk install` upgrades it in place.
		if args.AsSdk && existing.AsSDK == nil {
			existing.AsSDK = &workspace.ModuleAsSDK{}
			cfg.Modules[name] = existing
			if err := writeWorkspaceConfigWithHints(ctx, parent, cfg, nil); err != nil {
				return "", err
			}
			return dagql.String(fmt.Sprintf("Marked %q as an SDK", name)), nil
		}
		return dagql.String(fmt.Sprintf("Module %q is already installed", name)), nil
	}

	entry := workspace.ModuleEntry{
		Source: sourcePath,
	}
	if args.AsSdk {
		// Empty marker. Presence (not contents) is what `dagger module init`
		// / `dagger api client init` dispatch on.
		entry.AsSDK = &workspace.ModuleAsSDK{}
	}
	cfg.Modules[name] = entry
	hints := s.collectWorkspaceSettingsHints(ctx, parent, map[string]string{name: args.Ref})
	if err := writeWorkspaceConfigWithHints(ctx, parent, cfg, hints); err != nil {
		return "", err
	}

	cfgPath, err := configHostPath(parent)
	if err != nil {
		return "", err
	}

	verb := "Installed module"
	if args.AsSdk {
		verb = "Installed SDK"
	}
	msg := fmt.Sprintf("%s %q in %s", verb, name, cfgPath)
	if initialized {
		msg = fmt.Sprintf("Created workspace config in %s\n%s", filepath.Dir(cfgPath), msg)
	}
	return dagql.String(msg), nil
}

func (s *workspaceSchema) resolveWorkspaceInstall(
	ctx context.Context,
	ws *core.Workspace,
	ref string,
	name string,
	here bool,
) (string, string, error) {
	var err error
	ctx, err = withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return "", "", err
	}
	ctx = workspaceInstallLookupContext(ctx)

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return "", "", fmt.Errorf("dagql server: %w", err)
	}

	var src dagql.ObjectResult[*core.ModuleSource]
	if err := srv.Select(ctx, srv.Root(), &src, workspaceInstallModuleSourceSelector(ref)); err != nil {
		return "", "", fmt.Errorf("load module source: %w", err)
	}
	source := src.Self()
	if source == nil {
		return "", "", fmt.Errorf("load module source: empty result")
	}
	if !source.ConfigExists {
		return "", "", fmt.Errorf("ref %q does not point to an initialized module", ref)
	}
	if name == "" {
		name = source.ModuleName
	}
	if name == "" {
		return "", "", fmt.Errorf("ref %q does not point to an initialized module", ref)
	}

	sourcePath := ref
	if source.Kind != core.ModuleSourceKindLocal {
		return name, sourcePath, nil
	}
	if source.Local == nil {
		return "", "", fmt.Errorf("resolve local module source %q: missing local metadata", ref)
	}

	workspaceConfigDirRel := workspaceConfigDirectoryForWrite(ws, here)
	workspaceConfigPath, err := workspaceHostPath(ws, workspaceConfigDirRel, workspace.ConfigFileName)
	if err != nil {
		return "", "", err
	}
	workspaceConfigDir := filepath.Dir(workspaceConfigPath)

	depAbsPath := filepath.Join(source.Local.ContextDirectoryPath, source.SourceRootSubpath)
	sourcePath, err = filepath.Rel(workspaceConfigDir, depAbsPath)
	if err != nil {
		return "", "", fmt.Errorf("compute relative install path: %w", err)
	}
	return name, sourcePath, nil
}

func workspaceInstallLookupContext(ctx context.Context) context.Context {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil || clientMetadata.LockMode != "" {
		return ctx
	}

	refreshed := *clientMetadata
	refreshed.LockMode = string(workspace.LockModePinned)
	return engine.ContextWithClientMetadata(ctx, &refreshed)
}

func workspaceInstallModuleSourceSelector(ref string) dagql.Selector {
	return dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(ref)},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
		},
	}
}
