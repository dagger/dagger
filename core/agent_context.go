package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

type agentContextKey struct{}
type composingAgentContextKey struct{}

// WithComposingAgent identifies whose initial conversation spawn is composing.
// This affects only the lineage of agents spawned during that composition;
// CallerAgent remains unchanged for messages and deadlock detection.
//
// Like CallerAgent, the composing agent is execution context, never a recorded
// call input: it rides the context and the engine-side FunctionCall record
// (see ModuleFunction.Call), so module calls made during composition keep
// their ordinary cache keys. A spawn made while the composition executes gets
// this agent as its parent. A call that is a cache hit executes nothing, so a
// spawn inside a cached call is not repeated: its result still names the agent
// the first execution spawned. That is the rule for any cached function with
// side effects; a function that must spawn on every call opts out of caching
// (e.g. @cache(policy: Never)).
//
// An empty handle marks a context that is not composing, overriding the
// fallback to the current function call's record: an agent loop uses it, since
// its detached context may keep the client of a call made during another
// agent's composition.
func WithComposingAgent(ctx context.Context, handle string) context.Context {
	return context.WithValue(ctx, composingAgentContextKey{}, handle)
}

func composingAgentHandle(ctx context.Context) string {
	if handle, ok := ctx.Value(composingAgentContextKey{}).(string); ok {
		return handle
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return ""
	}
	fnCall, err := query.CurrentFunctionCall(ctx)
	if err != nil || fnCall == nil {
		return ""
	}
	return fnCall.composingAgentHandle
}

// AgentToContext binds a running agent's handle into the context, so code
// running on behalf of its evaluation loop knows which agent it serves: the
// child->parent channel of hack/designs/async-agents.md §3.1, where a spawned
// worker's tool can message (steer) the agent that called it. A tool's `Agent!`
// argument receives the conversation's owning agent (see implicitToolArgs),
// which inside the loop is this same agent.
//
// It is only ever set by the agent loop (AgentRuntime.loop), on the context
// every Step — and therefore every tool dispatch within it — descends from.
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

// CallerAgent resolves the agent on whose behalf the current execution runs:
// the ambient loop context (a tool dispatched in-process by an agent's step),
// or — for module functions and every nested API call they make — the calling
// client's function-call record, which modfunc stamps with the ambient agent
// at dispatch time. This is what lets the central enqueue path resolve
// message provenance (hack/designs/agent-messaging.md §4.1) without any
// forgeable "from" argument: a module cannot claim an agent it was not
// actually called from.
func CallerAgent(ctx context.Context) (dagql.ObjectResult[*Agent], bool) {
	if agent, ok := AgentFromContext(ctx); ok {
		return agent, true
	}
	query, err := CurrentQuery(ctx)
	if err != nil {
		return dagql.ObjectResult[*Agent]{}, false
	}
	fnCall, err := query.CurrentFunctionCall(ctx)
	if err != nil || fnCall == nil {
		return dagql.ObjectResult[*Agent]{}, false
	}
	return fnCall.CallerAgent()
}
