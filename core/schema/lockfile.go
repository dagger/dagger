package schema

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/core"
	"github.com/dagger/dagger/core/workspace"
	"github.com/dagger/dagger/engine"
	"github.com/dagger/dagger/engine/buildkit"
)

const lockCoreNamespace = ""

type workspaceLookupLock struct {
	ctx  context.Context
	bk   *buildkit.Client
	ws   *core.Workspace
	lock *workspace.Lock
}

func loadWorkspaceLookupLock(ctx context.Context, query *core.Query) (*workspaceLookupLock, error) {
	ws, err := query.CurrentWorkspace(ctx)
	if err != nil {
		// Not all contexts have a loaded workspace; treat this as "no lock available".
		return nil, nil
	}
	if ws == nil || ws.HostPath() == "" {
		// Remote workspaces are read-only for now.
		return nil, nil
	}

	workspaceCtx, err := withWorkspaceClientContext(ctx, ws)
	if err != nil {
		return nil, fmt.Errorf("workspace client context: %w", err)
	}

	bk, err := query.Buildkit(workspaceCtx)
	if err != nil {
		return nil, fmt.Errorf("buildkit client: %w", err)
	}

	lock, err := readWorkspaceLock(workspaceCtx, bk, ws)
	if err != nil {
		return nil, err
	}

	return &workspaceLookupLock{
		ctx:  workspaceCtx,
		bk:   bk,
		ws:   ws,
		lock: lock,
	}, nil
}

func (l *workspaceLookupLock) Save() error {
	if l == nil {
		return nil
	}
	return exportLockToHost(l.ctx, l.bk, l.ws, l.lock)
}

func currentLookupLockMode(ctx context.Context) (workspace.LockMode, error) {
	clientMetadata, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return "", fmt.Errorf("client metadata: %w", err)
	}
	return workspace.ParseLockMode(clientMetadata.LockMode)
}

type lookupLockResolution struct {
	Pin         string
	Policy      workspace.LockPolicy
	ShouldWrite bool
}

func resolveLookupFromLock(
	lockMode workspace.LockMode,
	lock *workspace.Lock,
	namespace, operation string,
	inputs []any,
	requestedPolicy workspace.LockPolicy,
) (lookupLockResolution, error) {
	resolution := lookupLockResolution{
		Policy: requestedPolicy,
	}

	if lock != nil {
		if lockResult, ok, err := lock.GetLookup(namespace, operation, inputs); err != nil {
			return resolution, fmt.Errorf("invalid lock entry for %s %v: %w", operation, inputs, err)
		} else if ok {
			resolution.Policy = lockResult.Policy
			switch lockMode {
			case workspace.LockModeStrict:
				resolution.Pin = lockResult.Value
				return resolution, nil
			case workspace.LockModeAuto:
				if resolution.Policy == workspace.PolicyPin {
					resolution.Pin = lockResult.Value
				}
				return resolution, nil
			case workspace.LockModeUpdate:
				resolution.ShouldWrite = true
				return resolution, nil
			default:
				return resolution, fmt.Errorf("unsupported lock mode %q", lockMode)
			}
		}
	}

	switch lockMode {
	case workspace.LockModeStrict:
		return resolution, fmt.Errorf("missing lock entry for %s %v", operation, inputs)
	case workspace.LockModeAuto:
		if resolution.Policy == workspace.PolicyPin {
			return resolution, fmt.Errorf("missing lock entry for pinned %s %v", operation, inputs)
		}
		return resolution, nil
	case workspace.LockModeUpdate:
		resolution.ShouldWrite = true
		return resolution, nil
	default:
		return resolution, fmt.Errorf("unsupported lock mode %q", lockMode)
	}
}
