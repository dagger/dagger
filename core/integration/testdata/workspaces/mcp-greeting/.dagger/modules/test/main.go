package main

import "dagger/test/internal/dagger"

type Test struct{}

func (m *Test) Greeting() string {
	return "hello from module"
}

// Compact requires the calling conversation, which only an LLM driving these
// tools can supply; served standalone (dagger mcp) it must not be offered.
func (m *Test) Compact(llm *dagger.LLM) string {
	return "compacted"
}

// Annotate takes the conversation if there is one; served standalone it is
// simply left unset.
func (m *Test) Annotate(
	// +optional
	llm *dagger.LLM,
) string {
	if llm == nil {
		return "no conversation"
	}
	return "conversation"
}

// Wave requires the calling agent, which only an agent loop can supply; served
// standalone (dagger mcp) it must not be offered.
func (m *Test) Wave(me *dagger.Agent) string {
	return "waved"
}

// Nudge takes the calling agent if there is one; served standalone it is
// passed null, and never appears in the tool's schema.
func (m *Test) Nudge(
	// +optional
	me *dagger.Agent,
) string {
	if me == nil {
		return "no agent"
	}
	return "agent"
}
