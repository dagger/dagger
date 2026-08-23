package core

import (
	"context"

	"github.com/dagger/dagger/dagql"
)

type llmContextKey struct{}

// LLMToContext binds the in-flight conversation into the context so that a tool
// invoked by that conversation can receive it: a module function declaring an
// `LLM!` argument has it auto-filled from here, exactly as a `Workspace!`
// argument is auto-filled from [WorkspaceToContext].
//
// The bound value is the conversation UP TO AND INCLUDING the tool call being
// dispatched (inst + withResponse), so a tool that transforms it and returns
// the result acts as a continuation: the loop resumes from the returned LLM
// instead of the one that made the call. See MCP.adoptLLM.
//
// It is only ever set at LLM tool dispatch (MCP.Call), so an ordinary call
// never inherits a conversation.
func LLMToContext(ctx context.Context, llm dagql.ObjectResult[*LLM]) context.Context {
	return context.WithValue(ctx, llmContextKey{}, llm)
}

// LLMFromContext returns the conversation bound into the context by
// [LLMToContext], if any.
func LLMFromContext(ctx context.Context) (dagql.ObjectResult[*LLM], bool) {
	if llm, ok := ctx.Value(llmContextKey{}).(dagql.ObjectResult[*LLM]); ok && llm.Self() != nil {
		return llm, true
	}
	return dagql.ObjectResult[*LLM]{}, false
}
