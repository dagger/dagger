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

func (s *workspaceSchema) resolveClientTargetModule(
	ctx context.Context,
	ws *core.Workspace,
	ref string,
) (dagql.ObjectResult[*core.ModuleSource], error) {
	var src dagql.ObjectResult[*core.ModuleSource]
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return src, fmt.Errorf("dagql server: %w", err)
	}
	if workspace.IsLocalRef(ref, "") {
		workspaceResult, err := dagql.NewObjectResultForCurrentCall(ctx, srv, ws)
		if err != nil {
			return src, fmt.Errorf("load workspace: %w", err)
		}
		// Local client refs have already been normalized to workspace-root
		// coordinates. Anchor them so Workspace.moduleSource does not resolve
		// them against a non-root workspace cwd a second time.
		if err := srv.Select(ctx, workspaceResult, &src, dagql.Selector{
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

// resolveWorkspaceClientModuleRef normalizes a client target into the two
// forms it needs. loadRef is workspace-root-relative for module loading.
// configRef is the spelling persisted relative to dagger.toml.
func resolveWorkspaceClientModuleRef(ws *core.Workspace, ref, configDir string) (loadRef string, configRef string, _ error) {
	if !workspace.IsLocalRef(ref, "") {
		return ref, ref, nil
	}
	cleaned := filepath.Clean(strings.ReplaceAll(ref, `\`, "/"))
	if filepath.IsAbs(cleaned) {
		hostRoot, ok := ws.LocalSourceHostPath()
		if !ok {
			return "", "", fmt.Errorf("absolute module ref %q requires a local workspace source", ref)
		}
		rel, err := filepath.Rel(hostRoot, cleaned)
		if err != nil {
			return "", "", fmt.Errorf("compute workspace-relative module path: %w", err)
		}
		cleaned = rel
	}
	if cleaned == "." || cleaned == "" {
		cleaned = "."
	}
	if cleaned == ".." || strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("module ref %q must not escape the workspace root", ref)
	}
	loadRef = filepath.ToSlash(cleaned)
	configRef, err := workspace.SDKManagedPathFor(configDir, loadRef)
	if err != nil {
		return "", "", err
	}
	// Keep an explicit local marker if the persisted spelling would otherwise
	// classify as a Git reference.
	if !workspace.IsLocalRef(configRef, "") {
		configRef = "./" + configRef
	}
	return loadRef, configRef, nil
}

// resolveSDKManagedClientModule resolves a persisted client target for module
// loading. Installed names resolve through [modules]. Local paths are relative
// to dagger.toml. Canonical references stay unchanged.
func resolveSDKManagedClientModule(ws *core.Workspace, cfg *workspace.Config, configDir, ref string) (string, error) {
	if cfg != nil {
		if entry, ok := cfg.Modules[ref]; ok {
			resolved := resolvedModuleEntrySourceWithPin(configDir, entry)
			if filepath.IsAbs(resolved) && ws == nil {
				return resolved, nil
			}
			loadRef, _, err := resolveWorkspaceClientModuleRef(ws, resolved, ".")
			return loadRef, err
		}
	}
	if !workspace.IsLocalRef(ref, "") {
		return ref, nil
	}
	return workspace.ResolveSDKManagedPath(configDir, ref)
}

// resolveWorkspaceClientModuleInput returns the target used for module loading
// and the target stored in dagger.toml. An installed name is stored unchanged.
func resolveWorkspaceClientModuleInput(
	ws *core.Workspace,
	cfg *workspace.Config,
	configDir,
	cwd,
	ref string,
) (loadRef string, configRef string, _ error) {
	if cfg != nil {
		if _, ok := cfg.Modules[ref]; ok {
			loadRef, err := resolveSDKManagedClientModule(ws, cfg, configDir, ref)
			return loadRef, ref, err
		}
	}
	if workspace.IsLocalRef(ref, "") {
		resolved, err := resolveWorkspacePath(ref, cwd)
		if err != nil {
			return "", "", fmt.Errorf("module target %q must not escape the workspace root", ref)
		}
		ref = resolved
	}
	loadRef, configRef, err := resolveWorkspaceClientModuleRef(ws, ref, configDir)
	if err != nil {
		return "", "", err
	}
	if cfg != nil {
		if _, collides := cfg.Modules[configRef]; collides {
			configRef = "./" + configRef
		}
	}
	return loadRef, configRef, nil
}

func workspaceClientModuleSourceSelector(ref string) dagql.Selector {
	return dagql.Selector{
		Field: "moduleSource",
		Args: []dagql.NamedInput{
			{Name: "refString", Value: dagql.String(ref)},
			{Name: "disableFindUp", Value: dagql.Boolean(true)},
		},
	}
}
