package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/dagql"
)

const workspaceLockingVersion = "v1.0.0-beta.10"

type workspaceLookupLock struct {
	ctx     context.Context
	query   *core.Query
	lock    *workspace.Lock
	refresh bool
}

type workspaceLookupLockOverrideKey struct{}
type workspaceLookupLockDisabledKey struct{}
type workspaceLookupLockRefreshKey struct{}

func withoutWorkspaceLookupLock(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, workspaceLookupLockDisabledKey{}, true)
	return dagql.WithPerClientCacheScope(ctx)
}

func workspaceLookupLockDisabled(ctx context.Context) bool {
	disabled, _ := ctx.Value(workspaceLookupLockDisabledKey{}).(bool)
	return disabled
}

func withWorkspaceLookupLockRefresh(ctx context.Context) context.Context {
	ctx = context.WithValue(ctx, workspaceLookupLockRefreshKey{}, true)
	return dagql.WithPerClientCacheScope(ctx)
}

func workspaceLookupLockRefresh(ctx context.Context) bool {
	refresh, _ := ctx.Value(workspaceLookupLockRefreshKey{}).(bool)
	return refresh
}

func withWorkspaceLookupLockOverride(ctx context.Context, lock *workspace.Lock) context.Context {
	ctx = context.WithValue(ctx, workspaceLookupLockOverrideKey{}, lock)
	return dagql.WithPerClientCacheScope(ctx)
}

func loadWorkspaceLookupLock(ctx context.Context, query *core.Query) (*workspaceLookupLock, error) {
	if lock, ok := ctx.Value(workspaceLookupLockOverrideKey{}).(*workspace.Lock); ok && lock != nil {
		return &workspaceLookupLock{
			lock:    lock,
			refresh: workspaceLookupLockRefresh(ctx),
		}, nil
	}

	lock, ok, err := query.CurrentWorkspaceLock(ctx, true)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}

	return &workspaceLookupLock{
		ctx:     ctx,
		query:   query,
		lock:    lock,
		refresh: workspaceLookupLockRefresh(ctx),
	}, nil
}

func (l *workspaceLookupLock) SetLookup(namespace, operation string, inputs []any, value string) error {
	if l == nil {
		return fmt.Errorf("workspace lock is required")
	}
	if l.query != nil {
		if err := l.query.SetCurrentWorkspaceLookup(l.ctx, namespace, operation, inputs, value); err != nil {
			return err
		}
	}
	if err := l.lock.SetLookup(namespace, operation, inputs, value); err != nil {
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
	Pin         string
	ShouldWrite bool
}

func resolveLookupFromLoadedLock(
	lookupLock *workspaceLookupLock,
	operation string,
	inputs []any,
) lookupLockResolution {
	resolution := lookupLockResolution{}
	if lookupLock == nil {
		return resolution
	}
	if lookupLock.refresh {
		return resolution
	}

	lockValue, ok := lookupLock.lock.GetLookup(workspace.CoreLockNamespace, operation, inputs)
	if !ok {
		resolution.ShouldWrite = true
		return resolution
	}

	resolution.Pin = lockValue
	return resolution
}
