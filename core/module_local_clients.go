package core

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

// CheckModuleServes refuses to let src's code serve target, a path relative
// to the root of src's tree, unless every key covering src's calls covers it.
// A git module's commit and a directory module's context cover the whole
// tree, so they may serve any module there. Any other module may serve only
// itself and the local clients the config governing it declares: those are
// what its calls, and its callers' calls, are keyed on (see
// moduleServesListedClientsOnly and declaredClientsDigest).
func CheckModuleServes(ctx context.Context, dag *dagql.Server, src *ModuleSource, target string) error {
	scope := path.Clean(src.SourceRootSubpath)
	if !moduleServesListedClientsOnly(src) || target == scope {
		return nil
	}
	reader, err := moduleTreeReader(ctx, dag, src)
	if err != nil {
		return err
	}
	var clients []string
	configDir := "."
	if reader != nil {
		clients, configDir, err = workspace.DeclaredLocalClients(ctx, scope, reader)
		if err != nil {
			return err
		}
	}
	if slices.Contains(clients, target) {
		return nil
	}
	client := configRelative(configDir, target)
	if !strings.HasPrefix(client, "../") {
		client = "./" + client
	}
	return fmt.Errorf("%q is not one of its clients, so its cached results could not follow a change to it; add %q to clients under [sdks.<sdk>.scopes.%q] in %s",
		target, client, configRelative(configDir, scope), path.Join(configDir, workspace.ConfigFileName))
}

// configRelative is a path relative to the root of a tree as a config at
// configDir in that tree spells it.
func configRelative(configDir, rel string) string {
	r, err := filepath.Rel(configDir, rel)
	if err != nil {
		return rel
	}
	return filepath.ToSlash(r)
}

// moduleServesListedClientsOnly reports whether a module may serve only the
// clients its config lists, because some key that covers its calls leaves out
// the tree it serves them from. A module loaded from the host is keyed on its
// own files, and so is a directory module whose context leaves the rest of
// its directory out, because entrypoint sources narrowed it or because it is
// read through a workspace, filtered to the module's own files. A module
// served from a host module's tree, at any depth, has a whole-tree key of its
// own, but the host module's key covers it only as its own files and the
// clients it lists in turn (see moduleClientClosure).
func moduleServesListedClientsOnly(src *ModuleSource) bool {
	switch src.Kind {
	case ModuleSourceKindLocal:
		return src.Local != nil
	case ModuleSourceKindDir:
		return src.DirSrc != nil && (src.DirSrc.ContextNarrowed || src.Workspace.Self() != nil || src.DirSrc.OriginHostPath != "")
	default:
		return false
	}
}

// moduleClientClosure returns the local clients the config governing the
// module at scope declares, and theirs in turn, each read from the config
// governing it, as sorted paths relative to the root of the tree.
func moduleClientClosure(ctx context.Context, scope string, read workspace.TreeFileReader) ([]string, error) {
	targets := map[string]bool{}
	pending, _, err := workspace.DeclaredLocalClients(ctx, scope, read)
	if err != nil {
		return nil, err
	}
	for len(pending) > 0 {
		target := pending[0]
		pending = pending[1:]
		if targets[target] {
			continue
		}
		targets[target] = true
		clients, _, err := workspace.DeclaredLocalClients(ctx, target, read)
		if err != nil {
			return nil, err
		}
		pending = append(pending, clients...)
	}
	sorted := slices.Collect(maps.Keys(targets))
	slices.Sort(sorted)
	return sorted, nil
}

// localModuleWorkspace returns the calling client's workspace, through which
// a local module's clients are loaded to key its calls, and the workspace as
// a result, zero when it is the client's current workspace. ws is nil when
// there is none, or when it is not on a host.
func localModuleWorkspace(ctx context.Context) (ws *Workspace, wsRes dagql.ObjectResult[*Workspace], _ error) {
	if bound, ok := WorkspaceFromContext(ctx); ok {
		ws, wsRes = bound.Self(), bound
	} else {
		query, err := CurrentQuery(ctx)
		if err != nil {
			return nil, wsRes, err
		}
		ws, err = query.Server.CurrentWorkspace(ctx)
		if errors.Is(err, ErrNoCurrentWorkspace) {
			return nil, wsRes, nil
		}
		if err != nil {
			return nil, wsRes, err
		}
	}
	if ws == nil || ws.HostPath() == "" {
		return nil, wsRes, nil
	}
	return ws, wsRes, nil
}

// localClientWorkspacePath is a client's path in ws, from its path relative to
// the root of the local module's tree, or false when ws doesn't hold it.
func localClientWorkspacePath(ws *Workspace, src *ModuleSource, target string) (string, bool) {
	rel, err := filepath.Rel(ws.HostPath(), filepath.Join(src.Local.ContextDirectoryPath, filepath.FromSlash(target)))
	if err != nil || !filepath.IsLocal(rel) {
		return "", false
	}
	return filepath.ToSlash(rel), true
}
