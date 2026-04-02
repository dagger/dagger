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

	cfg := &workspace.Config{Modules: map[string]workspace.ModuleEntry{}}
	if parent.HasConfig {
		cfg, err = readWorkspaceConfig(ctx, parent)
		if err != nil {
			return "", err
		}
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

	bk, err := workspaceBuildkit(ctx)
	if err != nil {
		return "", err
	}
	if err := ensureWorkspaceInitialized(ctx, bk, parent); err != nil {
		return "", fmt.Errorf("initialize workspace: %w", err)
	}

	cfg.Modules[name] = workspace.ModuleEntry{
		Source: sourcePath,
	}
	if err := writeWorkspaceConfig(ctx, parent, cfg); err != nil {
		return "", err
	}

	cfgPath, err := configHostPath(parent)
	if err != nil {
		return "", err
	}
	return dagql.String(fmt.Sprintf("Installed module %q in %s", name, cfgPath)), nil
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

	selector := workspaceInstallModuleSourceSelector(ref)

	if name == "" {
		var moduleName dagql.String
		if err := srv.Select(ctx, srv.Root(), &moduleName,
			selector,
			dagql.Selector{Field: "moduleName"},
		); err != nil {
			return "", "", fmt.Errorf("resolve module name: %w", err)
		}
		name = string(moduleName)
	}

	var kind core.ModuleSourceKind
	if err := srv.Select(ctx, srv.Root(), &kind,
		selector,
		dagql.Selector{Field: "kind"},
	); err != nil {
		return "", "", fmt.Errorf("resolve module source kind: %w", err)
	}

	sourcePath := ref
	if kind != core.ModuleSourceKindLocal {
		return name, sourcePath, nil
	}

	var contextDirPath dagql.String
	if err := srv.Select(ctx, srv.Root(), &contextDirPath,
		selector,
		dagql.Selector{Field: "localContextDirectoryPath"},
	); err != nil {
		return "", "", fmt.Errorf("resolve local context directory: %w", err)
	}

	var sourceRootSubpath dagql.String
	if err := srv.Select(ctx, srv.Root(), &sourceRootSubpath,
		selector,
		dagql.Selector{Field: "sourceRootSubpath"},
	); err != nil {
		return "", "", fmt.Errorf("resolve source root subpath: %w", err)
	}

	workspaceConfigDir, err := workspaceHostPath(ws, workspace.LockDirName)
	if err != nil {
		return "", "", err
	}

	depAbsPath := filepath.Join(string(contextDirPath), string(sourceRootSubpath))
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
