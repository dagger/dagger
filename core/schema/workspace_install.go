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
	Ref  string
	Name string `default:""`
}

func (s *workspaceSchema) install(
	ctx context.Context,
	parent *core.Workspace,
	args workspaceInstallArgs,
) (dagql.String, error) {
	name, sourcePath, err := s.resolveWorkspaceInstall(ctx, parent, args.Ref, args.Name)
	if err != nil {
		return "", err
	}

	cfg, initialized, err := loadWorkspaceConfigForMutation(ctx, parent, workspaceConfigInitIfMissing)
	if err != nil {
		return "", err
	}

	if existing, ok := cfg.Modules[name]; ok {
		if existing.Source == sourcePath {
			return dagql.String(fmt.Sprintf("Module %q is already installed", name)), nil
		}
		return "", fmt.Errorf(
			"module %q already exists in workspace config with source %q (new source %q)",
			name,
			existing.Source,
			sourcePath,
		)
	}

	cfg.Modules[name] = workspace.ModuleEntry{
		Source: sourcePath,
	}
	hints := s.collectWorkspaceConfigHints(ctx, parent, map[string]string{name: args.Ref})
	if err := writeWorkspaceConfigWithHints(ctx, parent, cfg, hints); err != nil {
		return "", err
	}

	cfgPath, err := configHostPath(parent)
	if err != nil {
		return "", err
	}

	msg := fmt.Sprintf("Installed module %q in %s", name, cfgPath)
	if initialized {
		msg = fmt.Sprintf("Initialized workspace in %s\n%s", filepath.Dir(cfgPath), msg)
	}
	return dagql.String(msg), nil
}

func (s *workspaceSchema) resolveWorkspaceInstall(
	ctx context.Context,
	ws *core.Workspace,
	ref string,
	name string,
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

	workspaceConfigDir, err := workspaceHostPath(ws, workspace.LockDirName)
	if err != nil {
		return "", "", err
	}

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
