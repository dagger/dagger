package schema

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

// resolveClientTargetModule loads the module a client target names, from the
// given workspace.
//
// The loaded module source retains the workspace it was selected on, and
// callers legitimately pass one built for the call in progress — overlayEdit's
// staged workspace is one. Attaching a module source that retained such a value
// publishes it as a second result of the caller's own field, so select through
// withWorkdir first: a real field call yields a result dagql produced. That is
// what the hop below is for, beyond the cwd it roots (dagger/dagger#13992).
func (s *workspaceSchema) resolveClientTargetModule(
	ctx context.Context,
	ws dagql.ObjectResult[*core.Workspace],
	ref string,
) (dagql.ObjectResult[*core.ModuleSource], error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return src, fmt.Errorf("dagql server: %w", err)
	}
	kind, err := clientModuleRefKind(ref)
	if err != nil {
		return src, err
	}
	if kind == core.ModuleSourceKindLocal {
		// Local client refs have already been normalized to workspace-root
		// coordinates. Root the workspace as well, so neither this lookup nor
		// the target's own dependency paths resolve against a scope cwd a
		// second time.
		var rooted dagql.ObjectResult[*core.Workspace]
		if err := srv.Select(ctx, ws, &rooted, dagql.Selector{
			Field: "withWorkdir",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(".")},
			},
		}); err != nil {
			return src, fmt.Errorf("root client workspace: %w", err)
		}
		if err := srv.Select(ctx, rooted, &src, dagql.Selector{
			Field: "moduleSource",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.String(filepath.ToSlash(filepath.Join("/", ref)))},
			},
		}); err != nil {
			return src, fmt.Errorf("load module source: %w", err)
		}
	} else if err := srv.Select(ctx, srv.Root(), &src, workspaceClientModuleSourceSelector(ref)); err != nil {
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

// clientModuleRefKind accepts explicit paths and module addresses. Installed
// names and unmarked paths are not client references. This check does not use
// the filesystem or contact a remote endpoint.
func clientModuleRefKind(ref string) (core.ModuleSourceKind, error) {
	path := strings.ReplaceAll(ref, `\`, "/")
	if path == "." || path == ".." || strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") || filepath.IsAbs(path) {
		return core.ModuleSourceKindLocal, nil
	}
	if ref != "" && !workspace.IsLocalRef(ref, "") {
		return core.ModuleSourceKindGit, nil
	}
	return "", fmt.Errorf("invalid client target %q: use an explicit local path such as %q or a module address; installed module names are not supported", ref, "./"+ref)
}

// explicitClientPath keeps normalized local references distinct from module
// addresses, including paths whose first component contains a dot.
func explicitClientPath(path string) string {
	path = filepath.ToSlash(path)
	if path == "." || path == ".." || strings.HasPrefix(path, "../") || strings.HasPrefix(path, "/") {
		return path
	}
	return "./" + path
}

// resolveSDKManagedClientModule resolves a saved target from the directory
// containing dagger.toml. Local load references keep an explicit path marker.
func resolveSDKManagedClientModule(configDir, ref string) (string, error) {
	kind, err := clientModuleRefKind(ref)
	if err != nil {
		return "", err
	}
	if kind == core.ModuleSourceKindGit {
		return ref, nil
	}
	resolved, err := workspace.ResolveSDKManagedPath(configDir, ref)
	if err != nil {
		return "", err
	}
	return explicitClientPath(resolved), nil
}

// resolveWorkspaceClientModuleInput returns a workspace-root-relative load
// reference and a target stored relative to dagger.toml.
func resolveWorkspaceClientModuleInput(configDir, cwd, ref string) (loadRef string, configRef string, _ error) {
	kind, err := clientModuleRefKind(ref)
	if err != nil {
		return "", "", err
	}
	if kind == core.ModuleSourceKindGit {
		return ref, ref, nil
	}
	resolved, err := resolveWorkspacePath(ref, cwd)
	if err != nil {
		return "", "", fmt.Errorf("module target %q must not escape the workspace root", ref)
	}
	configRef, err = workspace.SDKManagedPathFor(configDir, resolved)
	if err != nil {
		return "", "", err
	}
	return explicitClientPath(resolved), explicitClientPath(configRef), nil
}

func workspaceClientModuleSourceSelector(ref string) dagql.Selector {
	return dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(ref)},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
			{Name: "requireKind", Value: dagql.Opt(core.ModuleSourceKindGit)},
		},
	}
}
