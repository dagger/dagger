package schema

import (
	"context"
	"fmt"
	"path"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) withInitialized(
	ctx context.Context,
	parent dagql.ObjectResult[*core.Workspace],
	_ struct{},
) (dagql.ObjectResult[*core.Workspace], error) {
	ws := parent.Self()
	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	// Generic file edits do not update ConfigFile. Detect native config in the
	// current source so initialization preserves files staged by other APIs.
	// Limit the source read to config paths; do not upload the workspace tree.
	var includes []string
	if ws.ConfigFile != "" {
		includes = append(includes, filepath.ToSlash(ws.ConfigFile))
	}
	for dir := path.Clean(filepath.ToSlash(ws.Cwd)); ; dir = path.Dir(dir) {
		includes = append(includes,
			path.Join(dir, workspace.ConfigFileName),
			path.Join(dir, workspace.LegacyModuleConfigFileName),
		)
		if dir == "." {
			break
		}
	}
	configSource, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{Include: includes}, false)
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	statFS := &core.DirectoryStatFS{Dir: configSource}
	if ws.ConfigFile != "" {
		_, exists, err := core.StatFSExists(ctx, statFS, filepath.ToSlash(ws.ConfigFile))
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		if exists {
			// Keep a valid selection even if withWorkdir moved the cwd. File
			// removal, unlike a cwd change, requires detecting config again.
			return parent, nil
		}
	}
	detected, err := workspace.DetectInRoot(ctx, func(ctx context.Context, filename string) (string, bool, error) {
		return core.StatFSExists(ctx, statFS, filepath.ToSlash(filename))
	}, ws.Cwd, ".")
	if err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	if detected.ConfigFile != "" {
		updated := ws.Clone()
		setWorkspaceConfigSelection(updated, filepath.Dir(detected.ConfigFile))
		updated.SetCompatWorkspace(nil)
		return dagql.NewObjectResultForCurrentCall(ctx, srv, updated)
	}

	// A plain module may sit below legacy workspace configuration. Check every
	// ancestor up to the workspace boundary before starting a fresh workspace.
	for dir := path.Clean(filepath.ToSlash(ws.Cwd)); ; dir = path.Dir(dir) {
		configPath := path.Join(dir, workspace.LegacyModuleConfigFileName)
		_, exists, err := core.StatFSExists(ctx, statFS, configPath)
		if err != nil {
			return dagql.ObjectResult[*core.Workspace]{}, err
		}
		if exists {
			data, err := core.DirectoryReadFile(ctx, configSource, configPath)
			if err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, err
			}
			cfg, err := workspace.ParseLegacyModuleConfigTolerant(data)
			if err != nil {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("read %s: %w", configPath, err)
			}
			if workspace.RequiresWorkspaceMigration(cfg) {
				return dagql.ObjectResult[*core.Workspace]{}, fmt.Errorf("legacy Dagger workspace configuration at %s; run dagger workspace migrate instead", configPath)
			}
		}
		if dir == "." {
			break
		}
	}

	var staged dagql.ObjectResult[*core.Workspace]
	if err := srv.Select(ctx, parent, &staged, dagql.Selector{
		Field: "withNewFile",
		Args: []dagql.NamedInput{
			{Name: "path", Value: dagql.NewString("/" + workspace.ConfigFileName)},
			{Name: "contents", Value: dagql.NewString("")},
			{Name: "permissions", Value: dagql.NewInt(0o644)},
		},
	}); err != nil {
		return dagql.ObjectResult[*core.Workspace]{}, err
	}
	updated := staged.Self().Clone()
	setWorkspaceConfigSelection(updated, ".")
	updated.SetCompatWorkspace(nil)
	return dagql.NewObjectResultForCurrentCall(ctx, srv, updated)
}
