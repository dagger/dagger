package core

import (
	"context"
	"fmt"
	"sync"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/dagql/call"
	"github.com/dagger/dagger/engine"
)

// llmWorkspace is the Workspace an LLM is bound to.
//
// LLM.withWorkspace(workspace:) is a LazyRef argument: loading an LLM from its
// recipe carries the workspace by reference instead of evaluating it — and,
// through the conversation's own recipe, every workspace it was bound to
// before. A binding therefore holds either the loaded workspace (bound live)
// or only its recipe ID (bound from a recipe), and loads the latter the first
// time something needs its value. Presence checks and identity (ID) never
// load.
//
// A binding never changes what it refers to, so MCP.Clone shares it and
// rebinding installs a new one.
type llmWorkspace struct {
	// bound is the workspace an eager binding was made with. The LLM attaches
	// it as a dependency result, so it stays valid as long as the LLM does.
	bound dagql.ObjectResult[*Workspace]
	// id is a lazy binding's unevaluated recipe ID.
	id *call.ID

	mu sync.Mutex
	// loaded memoizes a lazy binding's load for the session that loaded it.
	// That session retains the result, not the LLM, and a cached LLM can be
	// shared with — and outlive — the session, so other sessions load it
	// again (a cache hit unless it was released in the meantime).
	loaded        dagql.ObjectResult[*Workspace]
	loadedSession string
}

// eagerLLMWorkspace binds an already-loaded workspace, or nothing if ws is
// unset.
func eagerLLMWorkspace(ws dagql.ObjectResult[*Workspace]) *llmWorkspace {
	if ws.Self() == nil {
		return nil
	}
	return &llmWorkspace{bound: ws}
}

// lazyLLMWorkspace binds a workspace by its recipe ID, without loading it.
func lazyLLMWorkspace(id *call.ID) *llmWorkspace {
	return &llmWorkspace{id: id}
}

// ID identifies the bound workspace without loading it: the bound result's ID
// for an eager binding, the recipe ID for a lazy one.
func (w *llmWorkspace) ID() (*call.ID, error) {
	if w.bound.Self() != nil {
		return w.bound.ID()
	}
	if w.id == nil {
		return nil, fmt.Errorf("workspace binding has neither a loaded value nor an ID")
	}
	return w.id, nil
}

// Load returns the bound workspace, loading a lazy binding on first use in
// the calling session. Concurrent callers share one load.
func (w *llmWorkspace) Load(ctx context.Context) (dagql.ObjectResult[*Workspace], error) {
	if w.bound.Self() != nil {
		return w.bound, nil
	}
	var res dagql.ObjectResult[*Workspace]
	if w.id == nil {
		return res, fmt.Errorf("workspace binding has neither a loaded value nor an ID")
	}
	clientMD, err := engine.ClientMetadataFromContext(ctx)
	if err != nil {
		return res, fmt.Errorf("load bound workspace: %w", err)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.loaded.Self() != nil && w.loadedSession == clientMD.SessionID {
		return w.loaded, nil
	}
	srv, err := CurrentDagqlServer(ctx)
	if err != nil {
		return res, fmt.Errorf("load bound workspace: %w", err)
	}
	res, err = dagql.NewID[*Workspace](w.id).Load(ctx, srv)
	if err != nil {
		return res, fmt.Errorf("load bound workspace: %w", err)
	}
	w.loaded, w.loadedSession = res, clientMD.SessionID
	return res, nil
}

// HasWorkspace reports whether a workspace is bound, without loading it.
func (m *MCP) HasWorkspace() bool {
	return m.workspace != nil
}

// Workspace returns the bound workspace, loading it if it was bound lazily.
// It returns a zero result and no error when no workspace is bound.
func (m *MCP) Workspace(ctx context.Context) (dagql.ObjectResult[*Workspace], error) {
	if m.workspace == nil {
		return dagql.ObjectResult[*Workspace]{}, nil
	}
	return m.workspace.Load(ctx)
}

// WorkspaceID returns the call.ID of the bound workspace, or nil if the LLM is
// not bound to a workspace, without loading it. Used by step() to detect (and
// persist) an in-step workspace change, e.g. a Changeset overlaid by a tool.
func (m *MCP) WorkspaceID() (*call.ID, error) {
	if m.workspace == nil {
		return nil, nil
	}
	return m.workspace.ID()
}
