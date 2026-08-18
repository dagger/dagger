package schema

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	lockCoreNamespace       = ""
	workspaceLockingVersion = "v1.0.0-beta.10"
)

type workspaceLookupLock struct {
	ctx   context.Context
	query *core.Query
	lock  *workspace.Lock
}

type workspaceLookupLockOverrideKey struct{}
type workspaceLookupLockDisabledKey struct{}

func withoutWorkspaceLookupLock(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, workspaceLookupLockDisabledKey{}, true)
	return dagql.WithPerClientCacheScope(ctx)
}

func workspaceLookupLockDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(workspaceLookupLockDisabledKey{}).(bool)
	return disabled
}

func withWorkspaceLookupLockOverride(ctx context.Context, lock *workspace.Lock) context.Context {
	ctx = context.WithValue(ctx, workspaceLookupLockOverrideKey{}, lock)
	return dagql.WithPerClientCacheScope(ctx)
}

func loadWorkspaceLookupLock(ctx context.Context, query *core.Query) (*workspaceLookupLock, error) {
	if lock, ok := ctx.Value(workspaceLookupLockOverrideKey{}).(*workspace.Lock); ok && lock != nil {
		return &workspaceLookupLock{lock: lock}, nil
	}

	lock, ok, err := query.CurrentWorkspaceLock(ctx, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return &workspaceLookupLock{
		ctx:   ctx,
		query: query,
		lock:  lock,
	}, nil
}

func (l *workspaceLookupLock) SetLookup(namespace, operation string, inputs []any, result workspace.LookupResult) error {
	if l == nil {
		return fmt.Errorf("workspace lock is required")
	}
	if l.query != nil {
		if err := l.query.SetCurrentWorkspaceLookup(l.ctx, namespace, operation, inputs, result); err != nil {
			return err
		}
	}
	if err := l.lock.SetLookup(namespace, operation, inputs, result); err != nil {
		return err
	}
	return nil
}

// lookupLockForAPI enables pinned locking only for API views that support it.
func lookupLockForAPI(
	ctx context.Context,
	query *core.Query,
	operation string,
) (*workspaceLookupLock, error) {
	if !core.Supports(ctx, workspaceLockingVersion) || workspaceLookupLockDisabled(ctx) {
		return nil, nil
	}

	lookupLock, err := loadWorkspaceLookupLock(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("%s lockfile: %w", operation, err)
	}
	return lookupLock, nil
}

type lookupLockResolution struct {
	Pin         any
	Policy      workspace.LockPolicy
	ShouldWrite bool
	Found       bool
}

func resolveLookupFromLoadedLock(
	lookupLock *workspaceLookupLock,
	operation string,
	inputs []any,
	requestedPolicy workspace.LockPolicy,
) (lookupLockResolution, error) {
	resolution := lookupLockResolution{
		Policy: requestedPolicy,
	}
	if lookupLock == nil {
		return resolution, nil
	}

	lockResult, ok, err := lookupLock.lock.GetLookup(lockCoreNamespace, operation, inputs)
	if err != nil {
		return resolution, fmt.Errorf("invalid lock entry for %s %v: %w", operation, inputs, err)
	}
	if !ok {
		resolution.ShouldWrite = true
		return resolution, nil
	}

	resolution.Found = true
	resolution.Policy = lockResult.Policy
	if resolution.Policy == workspace.PolicyPin {
		resolution.Pin = lockResult.Value
	} else {
		// Version 1 float entries are refreshed once before the file migrates.
		resolution.ShouldWrite = true
	}
	return resolution, nil
}

func lockHostPath(ws *core.Workspace) (string, error) {
	if ws.LockFile == "" {
		return "", fmt.Errorf("workspace lockfile is not selected")
	}
	return workspaceHostPath(ws, ws.LockFile)
}

func readWorkspaceLock(ctx context.Context, bk interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
}, ws *core.Workspace) (*workspace.Lock, error) {
	lock, _, err := readWorkspaceLockState(ctx, bk, ws)
	return lock, err
}

func readWorkspaceLockState(ctx context.Context, bk interface {
	ReadCallerHostFile(ctx context.Context, path string) ([]byte, error)
}, ws *core.Workspace) (*workspace.Lock, bool, error) {
	lockPath, err := lockHostPath(ws)
	if err != nil {
		return nil, false, err
	}

	data, err := bk.ReadCallerHostFile(ctx, lockPath)
	if err != nil {
		if isWorkspaceLockNotFound(err) {
			legacyPath, err := legacyLockHostPath(ws)
			if err != nil {
				return nil, false, err
			}
			if legacyPath == "" || legacyPath == lockPath {
				return workspace.NewLock(), false, nil
			}
			data, err = bk.ReadCallerHostFile(ctx, legacyPath)
			if err != nil {
				if isWorkspaceLockNotFound(err) {
					return workspace.NewLock(), false, nil
				}
				return nil, false, fmt.Errorf("reading legacy lock: %w", err)
			}
		} else {
			return nil, false, fmt.Errorf("reading lock: %w", err)
		}
	}

	lock, err := workspace.ParseLock(data)
	if err != nil {
		return nil, false, fmt.Errorf("parsing lock: %w", err)
	}
	return lock, true, nil
}

func isWorkspaceLockNotFound(err error) bool {
	return errors.Is(err, os.ErrNotExist) || status.Code(err) == codes.NotFound
}

func legacyLockHostPath(ws *core.Workspace) (string, error) {
	if ws == nil || ws.LockFile == "" {
		return "", nil
	}
	return workspaceHostPath(ws, workspace.LegacyLockFilePathForCanonical(ws.LockFile))
}
