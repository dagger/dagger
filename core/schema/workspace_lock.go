package schema

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

func (s *workspaceSchema) workspaceLockChangeset(
	ctx context.Context,
	ws *core.Workspace,
	lock *workspace.Lock,
) (*core.Changeset, error) {
	lockBytes, err := lock.Marshal()
	if err != nil {
		return nil, fmt.Errorf("marshal workspace lock: %w", err)
	}

	if ws.LockFile == "" {
		return nil, fmt.Errorf("workspace lockfile is not selected")
	}

	baseDir, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{}, false)
	if err != nil {
		return nil, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return nil, err
	}

	var updatedDir dagql.ObjectResult[*core.Directory]
	if err := srv.Select(ctx, baseDir, &updatedDir,
		dagql.Selector{
			Field: "withNewFile",
			Args: []dagql.NamedInput{
				{Name: "path", Value: dagql.NewString(filepath.ToSlash(ws.LockFile))},
				{Name: "contents", Value: dagql.String(lockBytes)},
				{Name: "permissions", Value: dagql.Int(0o644)},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("workspace lockfile write: %w", err)
	}

	var changes dagql.ObjectResult[*core.Changeset]
	baseDirID, err := baseDir.ID()
	if err != nil {
		return nil, fmt.Errorf("workspace lockfile base directory ID: %w", err)
	}
	if err := srv.Select(ctx, updatedDir, &changes,
		dagql.Selector{
			Field: "changes",
			Args: []dagql.NamedInput{
				{Name: "from", Value: dagql.NewID[*core.Directory](baseDirID)},
			},
		},
	); err != nil {
		return nil, fmt.Errorf("workspace lockfile changeset: %w", err)
	}

	return changes.Self(), nil
}
