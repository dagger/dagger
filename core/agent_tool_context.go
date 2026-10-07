package core

import "context"

type agentToolCallKey struct{}

// IsAgentToolCall distinguishes tools executing core APIs in the owner's
// context from direct user API calls. It is internal context, not API input.
func IsAgentToolCall(ctx context.Context) bool {
	marked, _ := ctx.Value(agentToolCallKey{}).(bool)
	return marked
}

type agentAddressKey struct{}

// WithAgentAddressResolution marks ctx as resolving an external address an
// agent's model supplied as a tool argument. Git may then authenticate the
// read with the agent owner's credentials, with their approval when a module
// drives the agent (see Server.AuthorizeGitRead). Only the address lookup is
// marked, never the tool's own execution.
func WithAgentAddressResolution(ctx context.Context) context.Context {
	return context.WithValue(ctx, agentAddressKey{}, true)
}

// IsAgentAddressResolution reports whether ctx is resolving a model-supplied
// address. See WithAgentAddressResolution.
func IsAgentAddressResolution(ctx context.Context) bool {
	marked, _ := ctx.Value(agentAddressKey{}).(bool)
	return marked
}
