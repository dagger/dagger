package entrypoint

import (
	"context"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

// callerScopedWorkspace builds the workspace of a local module inside the
// caller's workspace, when the caller's config declares local clients for it.
//
// The tree is rooted at the caller's workspace root, so the paths the config
// records resolve unchanged, and holds: the module's loaded files, each
// declared local client's loaded files, the loaded files of their local
// dependencies, and a config that names only that SDK and that scope. The
// working directory is the module's. It holds nothing else of the caller's
// workspace, which the module was not loaded from.
//
// The caller's config decides what is in scope, and the engine reads it here,
// from the caller's own workspace, so the scope follows that configuration and
// not the module. Each client's files are the engine's own include-filtered
// load of that client, not its host tree.
//
// ok is false when this does not apply: the module is not a local module inside
// the caller's workspace, the caller has no config, or the config declares no
// scope for the module. The workspace is then built from the module's context
// alone.
func callerScopedWorkspace(
	ctx context.Context,
	dag *dagql.Server,
	src *core.ModuleSource,
) (dagql.ObjectResult[*core.Workspace], bool, error) {
	var none dagql.ObjectResult[*core.Workspace]
	if src.Kind != core.ModuleSourceKindLocal || src.Local == nil || src.Workspace.Self() != nil {
		return none, false, nil
	}

	var ws dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, dag.Root(), &ws, dagql.Selector{Field: "currentWorkspace"}); err != nil {
		// No caller workspace to read a config from.
		return none, false, nil //nolint:nilerr
	}
	modulePath, ok := moduleWorkspacePath(ws.Self(), src)
	if !ok || ws.Self().ConfigFile == "" {
		return none, false, nil
	}

	cfg, err := readConfig(ctx, dag, ws)
	if err != nil {
		return none, false, err
	}
	scoped, clientPaths, ok := scopeConfig(cfg, ws.Self().ConfigFile, modulePath, src.ModuleName)
	if !ok {
		return none, false, nil
	}

	// The module and every declared client are loaded through the caller's
	// workspace, so their files sit at workspace-root-relative paths.
	var tree dagql.ObjectResult[*core.Directory]
	if err := dag.Select(ctx, dag.Root(), &tree, dagql.Selector{Field: "directory"}); err != nil {
		return none, false, err
	}
	seen := map[string]bool{}
	queue := append([]string{modulePath}, clientPaths...)
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if seen[p] {
			continue
		}
		seen[p] = true

		var loaded dagql.ObjectResult[*core.ModuleSource]
		if err := dag.Select(ctx, ws, &loaded, dagql.Selector{
			Field: "moduleSource",
			Args:  []dagql.NamedInput{{Name: "path", Value: dagql.String("/" + p)}},
		}); err != nil {
			return none, false, fmt.Errorf("load declared client %q: %w", p, err)
		}
		ctxDir := loaded.Self().ContextDirectory
		if ctxDir.Self() == nil {
			return none, false, fmt.Errorf("declared client %q has no context directory", p)
		}
		ctxDirID, err := ctxDir.ID()
		if err != nil {
			return none, false, err
		}
		if err := dag.Select(ctx, tree, &tree, dagql.Selector{
			Field: "withDirectory",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String("/")},
				{Name: "source", Value: dagql.NewID[*core.Directory](ctxDirID)},
			},
		}); err != nil {
			return none, false, err
		}
		// A client's local dependencies resolve inside the same tree, so they
		// come along. Their paths are workspace-root-relative too.
		for _, dep := range loaded.Self().Dependencies {
			if dep.Self() != nil && dep.Self().Kind == core.ModuleSourceKindLocal {
				queue = append(queue, cleanSlash(dep.Self().SourceRootSubpath))
			}
		}
	}

	var workspaceResult dagql.ObjectResult[*core.Workspace]
	if err := dag.Select(ctx, tree, &workspaceResult,
		dagql.Selector{Field: "withNewFile", Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.String(ws.Self().ConfigFile)},
			{Name: "contents", Value: dagql.String(workspace.SerializeConfig(scoped))},
		}},
		dagql.Selector{Field: "asWorkspace", Args: []dagql.NamedInput{
			{Name: "cwd", Value: dagql.String(modulePath)},
		}},
	); err != nil {
		return none, false, fmt.Errorf("module entrypoint workspace at %q: %w", modulePath, err)
	}
	return workspaceResult, true, nil
}

// moduleWorkspacePath returns a local module's directory relative to the
// workspace root, and whether the module is under that root at all. The
// engine's own loader attaches no workspace to a source, so the path comes from
// host paths. A rootless workspace holds no files, so no module is in it.
func moduleWorkspacePath(ws *core.Workspace, src *core.ModuleSource) (string, bool) {
	if ws == nil || src.Local == nil {
		return "", false
	}
	if _, rootless := ws.BaseSource().(*core.WorkspaceSourceRootlessLocal); rootless {
		return "", false
	}
	root := ws.HostPath()
	if root == "" || src.Local.ContextDirectoryPath == "" {
		return "", false
	}
	moduleDir := filepath.Join(src.Local.ContextDirectoryPath, cleanSubpath(src.SourceRootSubpath))
	rel, err := filepath.Rel(root, moduleDir)
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return cleanSlash(rel), true
}

func readConfig(ctx context.Context, dag *dagql.Server, ws dagql.ObjectResult[*core.Workspace]) (*workspace.Config, error) {
	var contents dagql.String
	if err := dag.Select(ctx, ws, &contents,
		dagql.Selector{Field: "file", Args: []dagql.NamedInput{{Name: "path", Value: dagql.String("/" + ws.Self().ConfigFile)}}},
		dagql.Selector{Field: "contents"},
	); err != nil {
		return nil, fmt.Errorf("read workspace config: %w", err)
	}
	cfg, err := workspace.ParseConfig([]byte(contents))
	if err != nil {
		return nil, fmt.Errorf("parse workspace config: %w", err)
	}
	return cfg, nil
}

// scopeConfig keeps, of the caller's config, only the SDK scopes that register
// the module at modulePath: each such SDK with that one scope, and the SDK's
// own module entry. It returns the workspace-root-relative paths of the local
// clients those scopes declare. ok is false when no scope registers the module.
//
// The scope's name is set to the module's name, so the entrypoint matches it
// without inferring a name from a workspace it does not have.
func scopeConfig(cfg *workspace.Config, configFile, modulePath, moduleName string) (*workspace.Config, []string, bool) {
	configDir := path.Dir(filepath.ToSlash(configFile))
	scoped := &workspace.Config{
		Modules: map[string]workspace.ModuleEntry{},
		SDKs:    map[string]workspace.SDKEntry{},
	}
	var clients []string
	for sdkName, sdk := range cfg.SDKs {
		for scopePath, scope := range sdk.Scopes {
			if !scope.IsModule {
				continue
			}
			resolved, err := workspace.ResolveSDKManagedPath(configDir, scopePath)
			if err != nil || resolved != modulePath {
				continue
			}
			if scope.Name == "" {
				scope.Name = moduleName
			}
			scoped.SDKs[sdkName] = workspace.SDKEntry{
				Module: sdk.Module,
				Scopes: map[string]workspace.SDKScope{scopePath: scope},
			}
			if entry, ok := cfg.Modules[sdk.Module]; ok {
				scoped.Modules[sdk.Module] = entry
			}
			for _, target := range scope.Clients {
				// A local client is written with an explicit path marker;
				// anything else is a git reference, which needs no files.
				if !strings.HasPrefix(target, ".") && !strings.HasPrefix(target, "/") {
					continue
				}
				clientPath, err := workspace.ResolveSDKManagedPath(configDir, target)
				if err != nil {
					continue
				}
				clients = append(clients, clientPath)
			}
		}
	}
	if len(scoped.SDKs) == 0 {
		return nil, nil, false
	}
	return scoped, clients, true
}

func cleanSlash(p string) string {
	return filepath.ToSlash(cleanSubpath(p))
}
