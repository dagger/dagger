package core

import (
	"context"
	"fmt"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine/slog"
	"github.com/vektah/gqlparser/v2/ast"
)

// AgentMiddleware is a single @agent middleware leaf: a function
// agent(base: LLM!): LLM! contributed by a module
// (hack/designs/workspace-agents.md §3).
type AgentMiddleware struct {
	Node *ModTreeNode `json:"node"`
}

// AgentMiddlewareGroup is the rolled-up set of @agent middlewares across the
// current module and its installed deps, composable onto a single base LLM.
type AgentMiddlewareGroup struct {
	Node   *ModTreeNode       `json:"node"`
	Agents []*AgentMiddleware `json:"agents"`

	// BoundWorkspace is the Workspace this group was rolled up from — the one
	// `Workspace.agents` was called on, including any overlay edits. Compose
	// threads it into the context (WorkspaceToContext) so each @agent leaf's
	// auto-injected Workspace! (and any currentWorkspace read, e.g. a project-
	// context scan) resolves against it, rather than the session's frozen current
	// workspace. Transient (not persisted): it is re-established when `agents`
	// re-runs on replay.
	BoundWorkspace dagql.ObjectResult[*Workspace] `json:"-"`
}

func NewAgentMiddlewareGroup(ctx context.Context, mod dagql.ObjectResult[*Module], include []string) (*AgentMiddlewareGroup, error) {
	rootNode, err := NewModTree(ctx, mod)
	if err != nil {
		return nil, err
	}

	agentNodes, err := rootNode.RollupAgents(ctx, include, nil)
	if err != nil {
		return nil, err
	}
	agents := make([]*AgentMiddleware, 0, len(agentNodes))
	for _, agentNode := range agentNodes {
		agents = append(agents, &AgentMiddleware{Node: agentNode})
	}

	return &AgentMiddlewareGroup{
		Node:   rootNode,
		Agents: agents,
	}, nil
}

func (*AgentMiddlewareGroup) Type() *ast.Type {
	return &ast.Type{
		NamedType: "AgentMiddlewareGroup",
		NonNull:   true,
	}
}

func (r *AgentMiddlewareGroup) List() []*AgentMiddleware {
	return r.Agents
}

// Compose threads a single base LLM through every selected @agent middleware in
// alphabetical module:fn order, returning the composed LLM (hack/designs/workspace-agents.md §3).
// Each leaf is invoked with base explicitly set to the running accumulator; the
// composed LLM's ID records the full chain and replays deterministically.
func (r *AgentMiddlewareGroup) Compose(ctx context.Context, base dagql.ObjectResult[*LLM]) (dagql.ObjectResult[*LLM], error) {
	// Compose the agents against the workspace this group was rolled up from, so
	// each @agent leaf's auto-injected Workspace! and any currentWorkspace read
	// resolve against BoundWorkspace, not the frozen session workspace.
	if r.BoundWorkspace.Self() != nil {
		ctx = WorkspaceToContext(ctx, r.BoundWorkspace)
	}

	acc := base
	for _, agent := range r.Agents {
		next, err := agent.Node.RunAgent(ctx, acc)
		if err != nil {
			return acc, fmt.Errorf("compose agent %q: %w", agent.Name(), err)
		}
		acc = next
	}
	warnToolNameCollisions(ctx, acc.Self())
	return acc, nil
}

// warnToolNameCollisions emits a warning for each tool name contributed by more
// than one of the composed agents' toolsets. On such a collision every involved
// object's tools are served under namespaced names (`<object>_<tool>`, see
// MCP.loadObjectTools) so nothing is silently shadowed; the warning surfaces
// the renaming so an author knows why the bare names are gone. Best-effort: any
// error building the toolset is ignored, since it must not fail composition.
func warnToolNameCollisions(ctx context.Context, llm *LLM) {
	if llm == nil || llm.mcp == nil {
		return
	}
	collisions, err := llm.mcp.ToolNameCollisions(ctx)
	if err != nil || len(collisions) == 0 {
		return
	}
	logger := slog.SpanLogger(ctx, InstrumentationLibrary)
	for name, contributors := range collisions {
		served := make([]string, len(contributors))
		for i, typeName := range contributors {
			served[i] = namespacedToolName(typeName, name)
		}
		logger.Warn("agent tool name collision: serving the conflicting toolsets under namespaced names",
			"tool", name,
			"contributors", contributors,
			"served", served)
	}
}

func (r *AgentMiddlewareGroup) Clone() *AgentMiddlewareGroup {
	cp := *r
	if cp.Node != nil {
		cp.Node = cp.Node.Clone()
	}
	cp.Agents = make([]*AgentMiddleware, len(r.Agents))
	for i := range cp.Agents {
		cp.Agents[i] = r.Agents[i].Clone()
	}
	return &cp
}

func (*AgentMiddleware) Type() *ast.Type {
	return &ast.Type{
		NamedType: "AgentMiddleware",
		NonNull:   true,
	}
}

func (a *AgentMiddleware) Path() []string {
	return a.Node.Path()
}

// Name is the fully qualified module:fn path of the agent.
func (a *AgentMiddleware) Name() string {
	return a.Node.PathString()
}

func (a *AgentMiddleware) Description() string {
	return a.Node.Description
}

func (a *AgentMiddleware) OriginalModule() *Module {
	return a.Node.OriginalModule.Self()
}

func (a *AgentMiddleware) Clone() *AgentMiddleware {
	cp := *a
	cp.Node = a.Node.Clone()
	return &cp
}
