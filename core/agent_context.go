package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

type agentContextKey struct{}

// AgentToContext binds a running agent's handle into the evaluation context.
// Agent runtime and telemetry code use it for parentage, and the MCP object-tool
// adapter reads it to pass the caller explicitly to a hidden `Agent!` argument.
// This is the child->parent channel of hack/designs/async-agents.md §3.1: a
// spawned worker's tool can message (steer) the agent that called it.
//
// It is only ever set by the agent loop (AgentRuntime.loop), on the context
// every Step — and therefore every tool dispatch within it — descends from.
// A synchronous LLM.loop or a direct function call never carries an agent.
func AgentToContext(ctx context.Context, agent dagql.ObjectResult[*Agent]) context.Context {
	return context.WithValue(ctx, agentContextKey{}, agent)
}

// AgentFromContext returns the agent handle bound into the context by
// [AgentToContext], if any.
func AgentFromContext(ctx context.Context) (dagql.ObjectResult[*Agent], bool) {
	if agent, ok := ctx.Value(agentContextKey{}).(dagql.ObjectResult[*Agent]); ok && agent.Self() != nil {
		return agent, true
	}
	return dagql.ObjectResult[*Agent]{}, false
}
