package core

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/mark3labs/mcp-go/mcp"
)

// MCPWorkspaceBridge keeps the harness's mutable working directory and its
// live MCP workspace binding aligned at serialized tool-call boundaries.
type MCPWorkspaceBridge struct {
	mcp     *MCP
	running *RunningService
	target  string
	synced  dagql.ObjectResult[*Directory]

	mu              sync.Mutex
	workspaceDigest string
}

func newMCPWorkspaceBridge(mcpEnv *MCP, process *LLMHarnessProcess, synced dagql.ObjectResult[*Directory]) (*MCPWorkspaceBridge, error) {
	if mcpEnv == nil {
		return nil, fmt.Errorf("harness MCP workspace is required")
	}
	if process == nil || process.running == nil {
		return nil, fmt.Errorf("running harness process is required")
	}
	workspaceID, err := mcpEnv.WorkspaceID()
	if err != nil {
		return nil, fmt.Errorf("get initial harness workspace ID: %w", err)
	}
	if workspaceID == nil || synced.Self() == nil {
		return nil, fmt.Errorf("bound harness workspace is required")
	}
	return &MCPWorkspaceBridge{
		mcp:             mcpEnv,
		running:         process.running,
		target:          process.workdir,
		synced:          synced,
		workspaceDigest: stableIDDigest(workspaceID).String(),
	}, nil
}

// Call synchronizes native edits into Dagger before dispatch and pushes any
// workspace returned by the Dagger tool back into the live harness mount. The
// push runs even when dispatch fails so partial Dagger state never remains
// hidden from the native process.
func (bridge *MCPWorkspaceBridge) Call(ctx context.Context, next llmToolCallHandler) (*mcp.CallToolResult, error) {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()

	if err := bridge.pullLocked(ctx); err != nil {
		return nil, fmt.Errorf("pull harness workspace: %w", err)
	}
	result, callErr := next(ctx)
	pushErr := bridge.pushLocked(ctx)
	return result, errors.Join(callErr, pushErr)
}

// Pull snapshots native edits at a terminal turn boundary. It serializes with
// MCP calls so a checkpoint cannot pair messages with an older workspace.
func (bridge *MCPWorkspaceBridge) Pull(ctx context.Context) error {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.pullLocked(ctx)
}

func (bridge *MCPWorkspaceBridge) Workspace() dagql.ObjectResult[*Workspace] {
	bridge.mu.Lock()
	defer bridge.mu.Unlock()
	return bridge.mcp.workspace
}

func (bridge *MCPWorkspaceBridge) pullLocked(ctx context.Context) error {
	srv, err := bridge.mcp.Server(ctx)
	if err != nil {
		return err
	}
	snapshot, err := bridge.running.snapshotWritableMount(ctx, bridge.target, bridge.synced, srv)
	if err != nil {
		return err
	}
	beforeID, err := bridge.synced.ID()
	if err != nil {
		return err
	}
	var changes dagql.ObjectResult[*Changeset]
	if err := srv.Select(ctx, snapshot, &changes, dagql.Selector{
		View:  srv.View,
		Field: "changes",
		Args: []dagql.NamedInput{
			{Name: "from", Value: dagql.NewID[*Directory](beforeID)},
		},
	}); err != nil {
		return err
	}
	empty, err := changes.Self().IsEmpty(ctx)
	if err != nil {
		return fmt.Errorf("inspect harness workspace changes: %w", err)
	}
	if !empty {
		if err := bridge.mcp.applyChangeset(ctx, srv, changes); err != nil {
			return err
		}
		workspaceID, err := bridge.mcp.WorkspaceID()
		if err != nil {
			return err
		}
		bridge.workspaceDigest = stableIDDigest(workspaceID).String()
	}
	bridge.synced = snapshot
	return nil
}

func (bridge *MCPWorkspaceBridge) pushLocked(ctx context.Context) error {
	workspaceID, err := bridge.mcp.WorkspaceID()
	if err != nil {
		return err
	}
	if workspaceID == nil {
		return fmt.Errorf("harness workspace became unbound")
	}
	workspaceDigest := stableIDDigest(workspaceID).String()
	if workspaceDigest == bridge.workspaceDigest {
		return nil
	}

	srv, err := bridge.mcp.Server(ctx)
	if err != nil {
		return err
	}
	workspace, err := bridge.mcp.workspaceDirectory(ctx, srv)
	if err != nil {
		return err
	}
	if err := bridge.running.replaceWritableMount(ctx, bridge.target, workspace); err != nil {
		return err
	}
	bridge.synced = workspace
	bridge.workspaceDigest = workspaceDigest
	return nil
}
