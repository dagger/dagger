package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

type agentContextKey struct{}

// AgentToContext binds a running agent's handle into the context so that a
// tool invoked from its evaluation loop can receive it: a module function
// declaring an `Agent!` argument has it auto-filled from here, exactly as an
// `LLM!` argument is auto-filled from [LLMToContext]. This is the
// child->parent channel of hack/designs/async-agents.md §3.1: a spawned
// worker's tool can message (steer) the agent that called it.
//
// It is only ever set by the agent loop (AgentRuntime.loop), on the context
// every Step — and therefore every tool dispatch within it — descends from.
// A synchronous LLM.loop or a direct function call never carries an agent,
// so such calls fail loudly rather than silently receiving nothing (see
// ModuleFunction.loadAgentArg).
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
