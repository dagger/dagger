package core

import "context"

type agentToolCallKey struct{}

// IsAgentToolCall distinguishes tools executing core APIs in the owner's
// context from direct user API calls. It is internal context, not API input.
func IsAgentToolCall(ctx context.Context) bool {
	marked, _ := ctx.Value(agentToolCallKey{}).(bool)
	return marked
}
