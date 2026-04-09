package schema

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	lockCoreNamespace           = ""
	lockModulesResolveOperation = "modules.resolve"
)

type workspaceLookupLock struct {
	ctx   context.Context
	query *core.Query
	lock  *workspace.Lock
}

func loadWorkspaceLookupLock(ctx context.Context, query *core.Query) (*workspaceLookupLock, error) {
	lock, ok, err := query.CurrentWorkspaceLock(ctx)
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
	if err := l.query.SetCurrentWorkspaceLookup(l.ctx, namespace, operation, inputs, result); err != nil {
		return err
	}
	if err := l.lock.SetLookup(namespace, operation, inputs, result); err != nil {
		return err
	}
	return nil
}

func currentLookupLockMode(ctx context.Context) (workspace.LockMode, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", fmt.Errorf("client metadata: %w", err)
	}
	return workspace.ResolveLockMode(clientMetadata.LockMode)
}

type lookupLockResolution struct {
	Pin         string
	Policy      workspace.LockPolicy
	ShouldWrite bool
	Found       bool
}

func resolveLookupFromLock(
	lockMode workspace.LockMode,
	lock *workspace.Lock,
	operation string,
	inputs []any,
	requestedPolicy workspace.LockPolicy,
) (lookupLockResolution, error) {
	resolution := lookupLockResolution{
		Policy: requestedPolicy,
	}

	if lockMode == workspace.LockModeDisabled {
		return resolution, nil
	}

	if lock != nil {
		if lockResult, ok, err := lock.GetLookup(lockCoreNamespace, operation, inputs); err != nil {
			return resolution, fmt.Errorf("invalid lock entry for %s %v: %w", operation, inputs, err)
		} else if ok {
			resolution.Found = true
			resolution.Policy = lockResult.Policy
			switch lockMode {
			case workspace.LockModeLive:
				resolution.ShouldWrite = true
				return resolution, nil
			case workspace.LockModeFrozen:
				resolution.Pin = lockResult.Value
				return resolution, nil
			case workspace.LockModePinned:
				if resolution.Policy == workspace.PolicyPin {
					resolution.Pin = lockResult.Value
				} else {
					resolution.ShouldWrite = true
				}
				return resolution, nil
			default:
				return resolution, fmt.Errorf("unsupported lock mode %q", lockMode)
			}
		}
	}

	switch lockMode {
	case workspace.LockModeLive:
		resolution.ShouldWrite = true
		return resolution, nil
	case workspace.LockModePinned:
		resolution.ShouldWrite = true
		return resolution, nil
	case workspace.LockModeFrozen:
		return resolution, fmt.Errorf("missing lock entry for %s %v", operation, inputs)
	default:
		return resolution, fmt.Errorf("unsupported lock mode %q", lockMode)
	}
}

func lockHostPath(ws *core.Workspace) (string, error) {
	return workspaceHostPath(ws, workspace.LockDirName, workspace.LockFileName)
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
			return workspace.NewLock(), false, nil
		}
		return nil, false, fmt.Errorf("reading lock: %w", err)
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

func exportLockToHost(ctx context.Context, bk *buildkit.Client, ws *core.Workspace, lock *workspace.Lock) error {
	lockBytes, err := lock.Marshal()
	if err != nil {
		return fmt.Errorf("marshal lock: %w", err)
	}

	lockPath, err := lockHostPath(ws)
	if err != nil {
		return err
	}

	return exportWorkspaceFileToHost(ctx, bk, lockPath, lockBytes)
}

func ExportLockToHost(ctx context.Context, bk *buildkit.Client, ws *core.Workspace, lock *workspace.Lock) error {
	return exportLockToHost(ctx, bk, ws, lock)
}

func resolveModuleSourceLookupResult(
	ctx context.Context,
	query *core.Query,
	source string,
	policy workspace.LockPolicy,
) (workspace.LookupResult, error) {
	ctx = lookupRefreshContext(ctx)

	bk, err := query.Buildkit(ctx)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("buildkit client: %w", err)
	}

	parsedRef, err := core.ParseRefString(ctx, core.NewCallerStatFS(bk), source, "")
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("parse module source %q: %w", source, err)
	}
	if parsedRef.Kind != core.ModuleSourceKindGit {
		return workspace.LookupResult{}, fmt.Errorf("module source %q is not a git source", source)
	}

	dag, err := core.CurrentDagqlServer(ctx)
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("query server: %w", err)
	}

	gitRef, err := parsedRef.Git.GitRef(ctx, dag, "")
	if err != nil {
		return workspace.LookupResult{}, fmt.Errorf("resolve module source %q: %w", source, err)
	}

	if policy == "" {
		policy = moduleResolveLockPolicy(gitRef.Self().Ref)
	}

	return workspace.LookupResult{
		Value:  gitRef.Self().Ref.SHA,
		Policy: policy,
	}, nil
}

func lookupRefreshContext(ctx context.Context) context.Context {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return ctx
	}

	refreshed := *clientMetadata
	refreshed.LockMode = string(workspace.LockModeDisabled)
	return engine.ContextWithClientMetadata(ctx, &refreshed)
}
