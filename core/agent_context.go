package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
	"github.com/dagger/dagger/engine"
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

// inheritedAgentHandle is the agent on whose behalf the exec that started the
// current client ran: a nested client of a withExec run from an agent's tool
// (a test binary or a CLI connecting back to the engine) has no FunctionCall
// record of its own, so neither CallerAgent nor composingAgentHandle can name
// the agent it runs for. The exec stamps the agent into the nested client's
// engine-internal metadata (engine.ClientMetadata.LineageAgent), and a module
// function called from such a client carries it on its FunctionCall record.
//
// Like composingAgentHandle, this is lineage only: agents the client spawns
// record it as their parent, while CallerAgent — message provenance and the
// waits-for guard — stays unset. A nested exec runs arbitrary code that is not
// the agent's turn: a test that spawns agents, messages them and awaits them
// must not speak as the agent that ran it, have its replies addressed to that
// agent, or record its waits as that agent's. And like composingAgentHandle
// it is execution context, never a recorded call input: a cache hit on the
// exec executes nothing and so spawns nothing.
//
// It is also captured when the process STARTS, not when it is described:
// withExec is lazy, so its clients inherit the agent of whichever context
// first forces the exec, not the one that built the container, and a
// concurrent forcer that joins that run inherits nothing of its own. A
// service (asService, withServiceBinding, up) runs nested clients the same
// way: the agent of the context that starts it, while installers that join
// the shared running service inherit nothing. This is the cache-hit rule
// again: the one execution that happens is attributed to the context that
// made it happen.
func inheritedAgentHandle(ctx context.Context) string {
	if query, err := CurrentQuery(ctx); err == nil {
		if fnCall, err := query.CurrentFunctionCall(ctx); err == nil && fnCall != nil && fnCall.inheritedAgentHandle != "" {
			return fnCall.inheritedAgentHandle
		}
	}
	if md, err := engine.ClientMetadataFromContext(ctx); err == nil && md != nil {
		return md.LineageAgent
	}
	return ""
}

// lineageAgentHandle resolves the agent that agents spawned in this execution
// belong to: the agent being composed, else the caller agent, else the agent
// inherited by a nested exec client. It is what a nested exec started here
// passes on to its clients.
func lineageAgentHandle(ctx context.Context) string {
	if composing := composingAgentHandle(ctx); composing != "" {
		return composing
	}
	if caller, ok := CallerAgent(ctx); ok {
		return caller.Self().Handle
	}
	return inheritedAgentHandle(ctx)
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
