package schema

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

type workspaceOverlayLock struct {
	Lock         *workspace.Lock
	Path         string
	originalData []byte
}

func (s *workspaceSchema) prepareWorkspaceOverlayLock(
	ctx context.Context,
	ws *core.Workspace,
	configDir string,
) (*core.Workspace, *workspaceOverlayLock, error) {
	selected := ws.Clone()
	setWorkspaceConfigSelection(selected, configDir)
	lock, err := s.readWorkspaceLockForOverlay(ctx, selected)
	if err != nil {
		return nil, nil, err
	}
	originalData, err := lock.Marshal()
	if err != nil {
		return nil, nil, fmt.Errorf("marshal original workspace lock: %w", err)
	}
	return selected, &workspaceOverlayLock{
		Lock:         lock,
		Path:         selected.LockFile,
		originalData: originalData,
	}, nil
}

func (lock *workspaceOverlayLock) updatedFile() (string, []byte, bool, error) {
	if lock == nil || lock.Lock == nil {
		return "", nil, false, nil
	}
	data, err := lock.Lock.Marshal()
	if err != nil {
		return "", nil, false, fmt.Errorf("marshal updated workspace lock: %w", err)
	}
	return lock.Path, data, !bytes.Equal(lock.originalData, data), nil
}

func (s *workspaceSchema) workspaceLockChangeset(
	ctx context.Context,
	ws *core.Workspace,
	lock *workspace.Lock,
) (dagql.ObjectResult[*core.Changeset], error) {
	var changes dagql.ObjectResult[*core.Changeset]
	lockBytes, err := lock.Marshal()
	if err != nil {
		return changes, fmt.Errorf("marshal workspace lock: %w", err)
	}

	if ws.LockFile == "" {
		return changes, fmt.Errorf("workspace lockfile is not selected")
	}

	// The diff only ever covers the lockfile write, so a base scoped to the
	// lock path yields the same changeset without materializing the whole
	// tree for host workspaces.
	baseDir, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{
		Include: []string{filepath.ToSlash(ws.LockFile)},
	}, false)
	if err != nil {
		return changes, err
	}

	srv, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return changes, err
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
		return changes, fmt.Errorf("workspace lockfile write: %w", err)
	}

	baseDirID, err := baseDir.ID()
	if err != nil {
		return changes, fmt.Errorf("workspace lockfile base directory ID: %w", err)
	}
	if err := srv.Select(ctx, updatedDir, &changes,
		dagql.Selector{
			Field: "changes",
			Args: []dagql.NamedInput{
				{Name: "from", Value: dagql.NewID[*core.Directory](baseDirID)},
			},
		},
	); err != nil {
		return changes, fmt.Errorf("workspace lockfile changeset: %w", err)
	}

	return changes, nil
}

func (s *workspaceSchema) readWorkspaceLockForOverlay(
	ctx context.Context,
	ws *core.Workspace,
) (*workspace.Lock, error) {
	if ws.LockFile == "" {
		return nil, fmt.Errorf("workspace lockfile is not selected")
	}

	// Scope the read to the lock path candidates so host workspaces sync only
	// those files instead of materializing the whole tree just to stat a lock.
	lockPath := filepath.ToSlash(ws.LockFile)
	legacyLockPath := filepath.ToSlash(workspace.LegacyLockFilePathForCanonical(ws.LockFile))
	root, err := s.resolveRootfs(ctx, ws, ".", core.CopyFilter{
		Include: []string{lockPath, legacyLockPath},
	}, false)
	if err != nil {
		return nil, err
	}
	statFS := &core.DirectoryStatFS{Dir: root}

	_, exists, err := core.StatFSExists(ctx, statFS, lockPath)
	if err != nil {
		return nil, fmt.Errorf("stat workspace lock: %w", err)
	}
	if !exists {
		lockPath = legacyLockPath
		_, exists, err = core.StatFSExists(ctx, statFS, lockPath)
		if err != nil {
			return nil, fmt.Errorf("stat legacy workspace lock: %w", err)
		}
	}
	if !exists {
		return workspace.NewLock(), nil
	}

	data, err := core.DirectoryReadFile(ctx, root, lockPath)
	if err != nil {
		return nil, fmt.Errorf("read workspace lock: %w", err)
	}
	lock, err := workspace.ParseLock(data)
	if err != nil {
		return nil, fmt.Errorf("parse workspace lock: %w", err)
	}
	return lock, nil
}
